// Package anthropic implements core.ModelProvider for the Anthropic Messages API
// (/v1/messages) using the official SDK github.com/anthropics/anthropic-sdk-go.
//
// This is an anti-corruption layer: core depends only on core.ModelProvider. This package
// translates core messages/tools into Messages API parameters (content blocks / tools) and
// converts the streaming event union back into core.ModelEvent.
//
// Extended thinking is fully supported across turns: thinking deltas and their signature are
// accumulated by the loop onto the assistant message (Reasoning + ReasoningToken), and replayed
// here as a thinking content block so multi-turn thinking continuity is preserved.
package anthropic

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"runtime/debug"
	"sort"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"

	core "github.com/skys-mission/creator-agent/core"
	"github.com/skys-mission/creator-agent/core/adapters/shared"
)

// defaultStreamTimeout is the default total timeout for streaming calls
// (fallback when the upstream context has no deadline). Coding agents may
// produce long output; 10 minutes is the default. Users can adjust via
// ProviderConfig.RequestTimeout (0 = unlimited).
const defaultStreamTimeout = 10 * time.Minute

// defaultMaxTokens is the Anthropic required max_tokens fallback when neither the request nor the
// variant sets it (Anthropic rejects requests without max_tokens).
const defaultMaxTokens int64 = 4096

// Provider is a ModelProvider implementation for the Anthropic Messages API.
type Provider struct {
	client  anthropic.Client
	model   string // anthropic.Model is a string alias
	timeout time.Duration

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
		option.WithHTTPClient(shared.HTTP1Client(timeout)),
	}
	if timeout > 0 {
		opts = append(opts, option.WithRequestTimeout(timeout))
	}
	if cfg.BaseURL != "" {
		opts = append(opts, option.WithBaseURL(cfg.BaseURL))
	}
	p := &Provider{
		client:       anthropic.NewClient(opts...),
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

// Stream implements core.ModelProvider: converts the Anthropic streaming event union into a
// core.ModelEvent channel.
func (p *Provider) Stream(ctx context.Context, req core.ModelRequest) (<-chan core.ModelEvent, error) {
	system, conv := toAnthropicMessages(req.Messages)
	params := anthropic.MessageNewParams{
		Model:     anthropic.Model(p.model),
		Messages:  conv,
		MaxTokens: resolveMaxTokens(req, p),
	}
	if len(system) > 0 {
		params.System = system
	}
	if len(req.Tools) > 0 {
		params.Tools = toAnthropicTools(req.Tools)
		params.ToolChoice = toAnthropicToolChoice(req.ToolChoice)
	}
	if req.Temperature != nil {
		params.Temperature = anthropic.Float(float64(*req.Temperature))
	} else if p.temperature != nil {
		params.Temperature = anthropic.Float(*p.temperature)
	}
	if req.TopP != nil {
		params.TopP = anthropic.Float(float64(*req.TopP))
	} else if p.topP != nil {
		params.TopP = anthropic.Float(*p.topP)
	}

	if os.Getenv("CREATOR_AGENT_DEBUG") == "1" {
		fmt.Fprintf(os.Stderr, "[anthropic] stream: model=%s msgs=%d tools=%d toolChoice=%d\n",
			p.model, len(conv), len(req.Tools), req.ToolChoice)
	}

	streamCtx := ctx
	var cancel context.CancelFunc
	if p.timeout > 0 {
		if _, ok := ctx.Deadline(); !ok {
			streamCtx, cancel = context.WithTimeout(ctx, p.timeout)
		}
	}

	streamOpts := variantRequestOptions(p.extraHeaders, p.extraBody)
	stream := p.client.Messages.NewStreaming(streamCtx, params, streamOpts...)

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
				fmt.Fprintf(os.Stderr, "anthropic stream panic: %v\n%s\n", r, stack)
				select {
				case ch <- core.MError{Err: fmt.Errorf("anthropic stream panicked: %v\n%s", r, stack)}:
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
		// blocks maps a content block index to its {id, name}; argument deltas carry only the index.
		blocks := make(map[int64]callInfo)
		for stream.Next() {
			ev := stream.Current()
			for _, me := range convertEvent(ev, blocks) {
				if !send(me) {
					return
				}
			}
		}
		if err := stream.Err(); err != nil {
			if errors.Is(streamCtx.Err(), context.DeadlineExceeded) {
				select {
				case ch <- core.MError{Err: fmt.Errorf("anthropic stream timeout after %v: %w", p.timeout, err)}:
				default:
				}
			} else if !errors.Is(err, context.Canceled) {
				_ = send(core.MError{Err: convertErr(err)})
			}
		}
	}()
	return ch, nil
}

// callInfo records a tool_use block's id and name, keyed by content block index so input_json
// deltas (which carry only the index) resolve the call id used to replay tool_result.
type callInfo struct {
	id   string
	name string
}

// convertEvent maps one Anthropic stream event into zero or more core.ModelEvent.
func convertEvent(ev anthropic.MessageStreamEventUnion, blocks map[int64]callInfo) []core.ModelEvent {
	var out []core.ModelEvent
	switch ev.Type {
	case "message_start":
		u := ev.Message.Usage
		out = append(out, core.MUsage{Usage: core.Usage{
			InputTokens: int(u.InputTokens),
			CacheRead:   int(u.CacheReadInputTokens),
			CacheWrite:  int(u.CacheCreationInputTokens),
		}})
	case "content_block_start":
		if ev.ContentBlock.Type == "tool_use" {
			blocks[ev.Index] = callInfo{id: ev.ContentBlock.ID, name: ev.ContentBlock.Name}
		}
	case "content_block_delta":
		ci := blocks[ev.Index]
		switch ev.Delta.Type {
		case "text_delta":
			out = append(out, core.MTextDelta{Delta: ev.Delta.Text})
		case "input_json_delta":
			out = append(out, core.MToolUseDelta{ID: ci.id, Name: ci.name, DeltaJSON: ev.Delta.PartialJSON})
		case "thinking_delta":
			out = append(out, core.MThinkingDelta{Delta: ev.Delta.Thinking})
		case "signature_delta":
			out = append(out, core.MThinkingSignature{Signature: ev.Delta.Signature})
		}
	case "message_delta":
		u := ev.Usage
		out = append(out, core.MUsage{Usage: core.Usage{
			OutputTokens: int(u.OutputTokens),
			CacheRead:    int(u.CacheReadInputTokens),
			CacheWrite:   int(u.CacheCreationInputTokens),
		}})
		if ev.Delta.StopReason != "" {
			out = append(out, core.MFinish{Reason: string(ev.Delta.StopReason)})
		}
	}
	return out
}

// toAnthropicMessages splits core messages into Anthropic system blocks (Messages API system
// parameter) and conversation messages. Tool results (role=tool) become user messages containing
// tool_result blocks (Anthropic has no separate tool role). Consecutive tool results from one
// assistant turn are merged into a single user message, as required for parallel tool use.
func toAnthropicMessages(msgs []core.Message) (system []anthropic.TextBlockParam, conv []anthropic.MessageParam) {
	for i := 0; i < len(msgs); i++ {
		m := msgs[i]
		switch m.Role {
		case core.RoleSystem:
			system = append(system, anthropic.TextBlockParam{Text: m.Content})
		case core.RoleUser:
			conv = append(conv, anthropic.NewUserMessage(anthropic.NewTextBlock(m.Content)))
		case core.RoleAssistant:
			var blocks []anthropic.ContentBlockParamUnion
			// Replay the thinking block first (signature required for multi-turn continuity).
			if m.ReasoningToken != "" {
				blocks = append(blocks, anthropic.NewThinkingBlock(m.ReasoningToken, m.Reasoning))
			} else if m.Reasoning != "" {
				blocks = append(blocks, anthropic.NewThinkingBlock("", m.Reasoning))
			}
			if m.Content != "" {
				blocks = append(blocks, anthropic.NewTextBlock(m.Content))
			}
			for _, tc := range m.ToolCalls {
				blocks = append(blocks, anthropic.NewToolUseBlock(tc.ID, jsonBytesToAny(tc.Input), tc.Name))
			}
			conv = append(conv, anthropic.NewAssistantMessage(blocks...))
		case core.RoleTool:
			toolBlocks := []anthropic.ContentBlockParamUnion{
				anthropic.NewToolResultBlock(m.ToolCallID, m.Content, m.ToolIsError),
			}
			for i++; i < len(msgs) && msgs[i].Role == core.RoleTool; i++ {
				tm := msgs[i]
				toolBlocks = append(toolBlocks, anthropic.NewToolResultBlock(tm.ToolCallID, tm.Content, tm.ToolIsError))
			}
			i--
			conv = append(conv, anthropic.NewUserMessage(toolBlocks...))
		}
	}
	return system, conv
}

// toAnthropicTools maps core.ToolInfo to Anthropic tool declarations.
func toAnthropicTools(tools []core.ToolInfo) []anthropic.ToolUnionParam {
	out := make([]anthropic.ToolUnionParam, 0, len(tools))
	for _, t := range tools {
		out = append(out, anthropic.ToolUnionParam{
			OfTool: &anthropic.ToolParam{
				Name:        t.Name,
				Description: anthropic.String(t.Description),
				InputSchema: toAnthropicSchema(t.InputSchema),
			},
		})
	}
	return out
}

// toAnthropicSchema converts a JSON schema (json.RawMessage) into the SDK ToolInputSchemaParam
// (type=object + properties + required). On parse failure it falls back to an empty object.
func toAnthropicSchema(raw json.RawMessage) anthropic.ToolInputSchemaParam {
	p := anthropic.ToolInputSchemaParam{Properties: map[string]any{}}
	if len(raw) == 0 {
		return p
	}
	var s struct {
		Properties map[string]any `json:"properties"`
		Required   []string       `json:"required"`
	}
	if json.Unmarshal(raw, &s) == nil {
		if s.Properties != nil {
			p.Properties = s.Properties
		}
		p.Required = s.Required
	}
	return p
}

// toAnthropicToolChoice maps core.ToolChoice to the Anthropic tool_choice parameter. Auto is the
// default (left unset).
func toAnthropicToolChoice(tc core.ToolChoice) anthropic.ToolChoiceUnionParam {
	switch tc {
	case core.ToolNone:
		return anthropic.ToolChoiceUnionParam{OfNone: &anthropic.ToolChoiceNoneParam{}}
	case core.ToolRequired:
		return anthropic.ToolChoiceUnionParam{OfAny: &anthropic.ToolChoiceAnyParam{}}
	default:
		return anthropic.ToolChoiceUnionParam{}
	}
}

// resolveMaxTokens returns the effective max_tokens (required by Anthropic): request value wins,
// then variant default, then the built-in fallback.
func resolveMaxTokens(req core.ModelRequest, p *Provider) int64 {
	if req.MaxTokens != nil {
		return int64(*req.MaxTokens)
	}
	if p.maxTokens != nil {
		return *p.maxTokens
	}
	return defaultMaxTokens
}

// jsonBytesToAny unmarshals a JSON byte slice into a generic value (map/slice/scalar) for the
// SDK's tool_use input (any). Falls back to an empty object on failure.
func jsonBytesToAny(b []byte) any {
	if len(b) == 0 {
		return map[string]any{}
	}
	var v any
	if json.Unmarshal(b, &v) == nil {
		return v
	}
	return map[string]any{}
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

// convertErr classifies anthropic SDK errors by HTTP status code into core error types for
// targeted handling by upper layers (reactive / retry). Non-anthropic.Error pass through unchanged.
func convertErr(err error) error {
	if err == nil {
		return nil
	}
	var apiErr *anthropic.Error
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
