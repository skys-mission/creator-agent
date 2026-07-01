// Package openairesponses implements core.ModelProvider for the OpenAI Responses API
// (/v1/responses) using the official SDK github.com/openai/openai-go.
//
// This is an anti-corruption layer: core depends only on core.ModelProvider. This package
// translates core messages/tools into Responses API parameters (input items / function tools)
// and converts the streaming event union back into core.ModelEvent.
//
// Stateless by default (store=false): the agent maintains conversation history and replays it as
// input items each turn. The Chat Completions protocol lives in core/adapters/openai-chat;
// Anthropic Messages lives in core/adapters/anthropic.
package openairesponses

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"runtime/debug"
	"sort"
	"time"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/openai/openai-go/packages/param"
	"github.com/openai/openai-go/responses"

	core "github.com/skys-mission/creator-agent/core"
	"github.com/skys-mission/creator-agent/core/adapters/shared"
)

// defaultStreamTimeout is the default total timeout for streaming calls
// (fallback when the upstream context has no deadline). Coding agents may
// produce long output; 10 minutes is the default. Users can adjust via
// ProviderConfig.RequestTimeout (0 = unlimited).
const defaultStreamTimeout = 10 * time.Minute

// Provider is a ModelProvider implementation for the OpenAI Responses API.
type Provider struct {
	client  openai.Client
	model   string // shared.ResponsesModel is a string alias
	timeout time.Duration

	// Variant overrides resolved at construction time; a single provider instance is internally
	// consistent for its whole lifetime (runtime variant switching rebuilds a new provider).
	extraHeaders map[string]string
	extraBody    map[string]any
	temperature  *float64
	topP         *float64
	maxTokens    *int64
}

// NewProvider creates a new Provider. If BaseURL is empty, the SDK default (official endpoint) is
// used. Retries use the SDK's built-in WithMaxRetries (408/409/429 with backoff during setup).
func NewProvider(ctx context.Context, cfg shared.ProviderConfig) (*Provider, error) {
	timeout := cfg.RequestTimeout
	if timeout == 0 {
		timeout = defaultStreamTimeout
	}
	opts := []option.RequestOption{
		option.WithAPIKey(cfg.APIKey),
		option.WithMaxRetries(3),
		// Force HTTP/1.1 to avoid http2 Framer panic paths in net/http (shared with other adapters).
		option.WithHTTPClient(shared.HTTP1Client(timeout)),
	}
	if timeout > 0 {
		opts = append(opts, option.WithRequestTimeout(timeout))
	}
	if cfg.BaseURL != "" {
		opts = append(opts, option.WithBaseURL(cfg.BaseURL))
	}
	p := &Provider{
		client:       openai.NewClient(opts...),
		model:        cfg.Model,
		extraHeaders: cfg.ExtraHeaders,
		extraBody:    cfg.ExtraBody,
		temperature:  cfg.Temperature,
		topP:         cfg.TopP,
		maxTokens:    cfg.MaxTokens,
	}
	if timeout > 0 {
		p.timeout = timeout
	}
	return p, nil
}

// Stream implements core.ModelProvider: converts the Responses streaming event union into a
// core.ModelEvent channel.
func (p *Provider) Stream(ctx context.Context, req core.ModelRequest) (<-chan core.ModelEvent, error) {
	sys, items := toResponsesInput(req.Messages)
	params := responses.ResponseNewParams{
		Model: p.model,
		Input: responses.ResponseNewParamsInputUnion{OfInputItemList: items},
		// Stateless: the agent maintains history and replays it as input items each turn.
		Store: openai.Bool(false),
	}
	if sys != "" {
		params.Instructions = openai.String(sys)
	}
	if len(req.Tools) > 0 {
		params.Tools = toResponsesTools(req.Tools)
		params.ToolChoice = toResponsesToolChoice(req.ToolChoice)
	}
	// Per-request sampling parameters win over variant defaults.
	if req.Temperature != nil {
		params.Temperature = openai.Float(float64(*req.Temperature))
	} else if p.temperature != nil {
		params.Temperature = openai.Float(*p.temperature)
	}
	if req.MaxTokens != nil {
		params.MaxOutputTokens = openai.Int(int64(*req.MaxTokens))
	} else if p.maxTokens != nil {
		params.MaxOutputTokens = openai.Int(*p.maxTokens)
	}
	if req.TopP != nil {
		params.TopP = openai.Float(float64(*req.TopP))
	} else if p.topP != nil {
		params.TopP = openai.Float(*p.topP)
	}

	if os.Getenv("CREATOR_AGENT_DEBUG") == "1" {
		fmt.Fprintf(os.Stderr, "[openai-responses] stream: model=%s items=%d tools=%d toolChoice=%d\n",
			p.model, len(items), len(req.Tools), req.ToolChoice)
	}

	// Total streaming timeout: if the upstream ctx has no deadline and a timeout is configured, wrap it.
	streamCtx := ctx
	var cancel context.CancelFunc
	if p.timeout > 0 {
		if _, ok := ctx.Deadline(); !ok {
			streamCtx, cancel = context.WithTimeout(ctx, p.timeout)
		}
	}

	streamOpts := variantRequestOptions(p.extraHeaders, p.extraBody)
	stream := p.client.Responses.NewStreaming(streamCtx, params, streamOpts...)

	ch := make(chan core.ModelEvent)
	go func() {
		defer close(ch)
		if cancel != nil {
			defer cancel()
		}
		defer stream.Close()
		defer func() {
			if r := recover(); r != nil {
				stack := debug.Stack()
				fmt.Fprintf(os.Stderr, "openai-responses stream panic: %v\n%s\n", r, stack)
				select {
				case ch <- core.MError{Err: fmt.Errorf("openai-responses stream panicked: %v\n%s", r, stack)}:
				default:
				}
			}
		}()
		send := func(ev core.ModelEvent) bool {
			select {
			case ch <- ev:
				return true
			case <-streamCtx.Done():
				return false
			}
		}
		// callKey maps an item id (and call_id) to its {callID, name}, so argument deltas (which
		// carry only item_id) can resolve the tool call id used to replay function_call_output.
		calls := make(map[string]callInfo)
		for stream.Next() {
			ev := stream.Current()
			for _, me := range convertEvent(ev, calls) {
				if !send(me) {
					return
				}
			}
		}
		if err := stream.Err(); err != nil {
			if errors.Is(streamCtx.Err(), context.DeadlineExceeded) {
				select {
				case ch <- core.MError{Err: fmt.Errorf("openai-responses stream timeout after %v: %w", p.timeout, err)}:
				default:
				}
			} else if !errors.Is(err, context.Canceled) {
				_ = send(core.MError{Err: convertErr(err)})
			}
		}
	}()
	return ch, nil
}

// callInfo records a function tool call's replay id (call_id) and name, keyed by both item id and
// call_id so argument deltas resolve regardless of which identifier the event carries.
type callInfo struct {
	callID string
	name   string
}

// convertEvent maps one Responses stream event into zero or more core.ModelEvent.
func convertEvent(ev responses.ResponseStreamEventUnion, calls map[string]callInfo) []core.ModelEvent {
	var out []core.ModelEvent
	switch ev.Type {
	case "response.output_text.delta":
		if d := ev.Delta.OfString; d != "" {
			out = append(out, core.MTextDelta{Delta: d})
		}
	case "response.refusal.delta":
		if d := ev.Delta.OfString; d != "" {
			out = append(out, core.MTextDelta{Delta: d})
		}
	case "response.output_item.added":
		if ev.Item.Type == "function_call" {
			ci := callInfo{callID: ev.Item.CallID, name: ev.Item.Name}
			if ev.Item.ID != "" {
				calls[ev.Item.ID] = ci
			}
			if ev.Item.CallID != "" {
				calls[ev.Item.CallID] = ci
			}
		}
	case "response.function_call_arguments.delta":
		ci := calls[ev.ItemID]
		out = append(out, core.MToolUseDelta{ID: ci.callID, Name: ci.name, DeltaJSON: ev.Delta.OfString})
	case "response.reasoning_summary_text.delta":
		if d := ev.Delta.OfString; d != "" {
			out = append(out, core.MThinkingDelta{Delta: d})
		}
	case "response.completed":
		u := ev.Response.Usage
		out = append(out, core.MUsage{Usage: core.Usage{
			InputTokens:  int(u.InputTokens),
			OutputTokens: int(u.OutputTokens),
		}})
		out = append(out, core.MFinish{Reason: "stop"})
	case "response.failed", "response.incomplete":
		// Surface the failure reason (Response.Error is required on these terminal events).
		out = append(out, core.MError{Err: fmt.Errorf("responses %s: %s", ev.Type, ev.Response.Error.Message)})
		out = append(out, core.MFinish{Reason: "stop"})
	case "error":
		out = append(out, core.MError{Err: fmt.Errorf("responses error %s: %s", ev.Code, ev.Message)})
	}
	return out
}

// toResponsesInput splits core messages into a system instruction (Responses Instructions field)
// and an ordered list of input items (messages, function_call, function_call_output).
func toResponsesInput(msgs []core.Message) (system string, items responses.ResponseInputParam) {
	items = make(responses.ResponseInputParam, 0, len(msgs))
	for _, m := range msgs {
		switch m.Role {
		case core.RoleSystem:
			if system == "" {
				system = m.Content
			} else {
				system += "\n" + m.Content
			}
		case core.RoleUser:
			items = append(items, responses.ResponseInputItemUnionParam{
				OfMessage: &responses.EasyInputMessageParam{
					Role:    responses.EasyInputMessageRole("user"),
					Content: responses.EasyInputMessageContentUnionParam{OfString: openai.String(m.Content)},
				},
			})
		case core.RoleAssistant:
			if m.Content != "" {
				items = append(items, responses.ResponseInputItemUnionParam{
					OfMessage: &responses.EasyInputMessageParam{
						Role:    responses.EasyInputMessageRole("assistant"),
						Content: responses.EasyInputMessageContentUnionParam{OfString: openai.String(m.Content)},
					},
				})
			}
			for _, tc := range m.ToolCalls {
				items = append(items, responses.ResponseInputItemUnionParam{
					OfFunctionCall: &responses.ResponseFunctionToolCallParam{
						CallID:    tc.ID,
						Name:      tc.Name,
						Arguments: string(tc.Input),
					},
				})
			}
		case core.RoleTool:
			items = append(items, responses.ResponseInputItemUnionParam{
				OfFunctionCallOutput: &responses.ResponseInputItemFunctionCallOutputParam{
					CallID: m.ToolCallID,
					Output: m.Content,
				},
			})
		}
	}
	return system, items
}

// toResponsesTools maps core.ToolInfo to Responses function tool declarations.
func toResponsesTools(tools []core.ToolInfo) []responses.ToolUnionParam {
	out := make([]responses.ToolUnionParam, 0, len(tools))
	for _, t := range tools {
		out = append(out, responses.ToolUnionParam{
			OfFunction: &responses.FunctionToolParam{
				Name:        t.Name,
				Description: openai.String(t.Description),
				Parameters:  toSchemaMap(t.InputSchema),
				// Strict=false: our tool schemas are not guaranteed to satisfy strict structured
				// outputs (additionalProperties etc.); relax so non-strict schemas still work.
				Strict: openai.Bool(false),
			},
		})
	}
	return out
}

// toResponsesToolChoice maps core.ToolChoice to the Responses tool_choice parameter. Auto is the
// default (left unset).
func toResponsesToolChoice(tc core.ToolChoice) responses.ResponseNewParamsToolChoiceUnion {
	switch tc {
	case core.ToolNone:
		return responses.ResponseNewParamsToolChoiceUnion{
			OfToolChoiceMode: param.Opt[responses.ToolChoiceOptions]{Value: responses.ToolChoiceOptionsNone},
		}
	case core.ToolRequired:
		return responses.ResponseNewParamsToolChoiceUnion{
			OfToolChoiceMode: param.Opt[responses.ToolChoiceOptions]{Value: responses.ToolChoiceOptionsRequired},
		}
	default:
		return responses.ResponseNewParamsToolChoiceUnion{}
	}
}

// toSchemaMap converts a JSON schema (json.RawMessage) into a map for the SDK. On parse failure it
// falls back to the minimal valid schema (object + empty properties) so the tool stays usable.
func toSchemaMap(raw json.RawMessage) map[string]any {
	if len(raw) == 0 {
		return map[string]any{"type": "object", "properties": map[string]any{}}
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil || m == nil {
		return map[string]any{"type": "object", "properties": map[string]any{}}
	}
	return m
}

// variantRequestOptions builds per-call SDK options that merge variant header/body overrides onto
// the request. Returns nil when there is nothing to apply so the variadic spread is a no-op.
func variantRequestOptions(headers map[string]string, body map[string]any) []option.RequestOption {
	if len(headers) == 0 && len(body) == 0 {
		return nil
	}
	opts := make([]option.RequestOption, 0, len(headers)+len(body))
	hk := make([]string, 0, len(headers))
	for k := range headers {
		hk = append(hk, k)
	}
	sort.Strings(hk)
	for _, k := range hk {
		opts = append(opts, option.WithHeader(k, headers[k]))
	}
	bk := make([]string, 0, len(body))
	for k := range body {
		bk = append(bk, k)
	}
	sort.Strings(bk)
	for _, k := range bk {
		opts = append(opts, option.WithJSONSet(k, body[k]))
	}
	return opts
}

// convertErr classifies openai-go errors by HTTP status code into core error types for targeted
// handling by upper layers (reactive / retry). Non-openai.Error (network / timeout / ctx) pass through.
func convertErr(err error) error {
	if err == nil {
		return nil
	}
	var apiErr *openai.Error
	if errors.As(err, &apiErr) {
		switch {
		case apiErr.StatusCode == 429:
			return &core.RateLimitedError{Err: err, StatusCode: apiErr.StatusCode}
		case apiErr.StatusCode >= 500:
			return &core.ServerError{Err: err, StatusCode: apiErr.StatusCode}
		case apiErr.StatusCode >= 400:
			return &core.ClientError{Err: err, StatusCode: apiErr.StatusCode}
		}
	}
	return err
}
