// Package openaichat implements core.ModelProvider for the OpenAI Chat Completions protocol
// (/v1/chat/completions) using the official SDK github.com/openai/openai-go.
//
// This is an anti-corruption layer: core depends only on the core.ModelProvider interface.
// This package translates core messages/tools into openai-go parameters and converts
// streaming chunks back into core.ModelEvent. Breaking changes in openai-go are confined
// to this package; core and upper layers remain unaffected.
//
// Chat Completions is the de-facto standard supported by any OpenAI-compatible endpoint.
// The OpenAI Responses protocol lives in core/adapters/openai-responses; Anthropic Messages
// lives in core/adapters/anthropic.
package openaichat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"runtime/debug"
	"sort"
	"strings"
	"time"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	oshared "github.com/openai/openai-go/shared" // alias: SDK shares the name "shared" with our adapters/shared

	core "github.com/skys-mission/creator-agent/core"
	"github.com/skys-mission/creator-agent/core/adapters/shared"
)

// defaultStreamTimeout is the default total timeout for streaming calls
// (fallback when the upstream context has no deadline). Coding agents may
// produce long output; 10 minutes is the default. Users can adjust via
// ProviderConfig.RequestTimeout (0 = unlimited).
const defaultStreamTimeout = 10 * time.Minute

// Provider is a ModelProvider implementation based on the official openai-go SDK.
type Provider struct {
	client  openai.Client
	model   openai.ChatModel
	timeout time.Duration // total streaming timeout (0 = no limit, rely on upstream ctx)

	// Variant overrides applied to every request (resolved from the selected profile variant by the
	// caller at construction time). nil/empty = no overrides. They are stored here rather than read
	// per-call so a single provider instance is internally consistent for its whole lifetime; runtime
	// variant switching rebuilds a new provider.
	extraHeaders map[string]string // HTTP headers merged onto each request (option.WithHeader)
	extraBody    map[string]any    // arbitrary JSON body keys merged onto each request (option.WithJSONSet)

	// Generation defaults from the variant; applied only when the caller's ModelRequest does not
	// already set them (request-level value wins over variant default).
	temperature *float64
	topP        *float64
	maxTokens   *int64
}

// NewProvider creates a new Provider. If BaseURL is empty, the SDK default (official endpoint) is used.
// Retries use the SDK's built-in WithMaxRetries: openai-go automatically retries 408/409/429 with backoff during the setup phase.
func NewProvider(ctx context.Context, cfg shared.ProviderConfig) (*Provider, error) {
	timeout := cfg.RequestTimeout
	if timeout == 0 {
		timeout = defaultStreamTimeout
	}
	opts := []option.RequestOption{
		option.WithAPIKey(cfg.APIKey),
		option.WithMaxRetries(3),
		// Force HTTP/1.1: some OpenAI-compatible endpoints trigger HTTP/2 framing panics in
		// net/http (outside recover scope); HTTP/1.1 surfaces connection errors instead.
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
		model:        openai.ChatModel(cfg.Model),
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

// Stream implements core.ModelProvider: converts openai-go streaming chunks into a core.ModelEvent channel.
func (p *Provider) Stream(ctx context.Context, req core.ModelRequest) (<-chan core.ModelEvent, error) {
	params := openai.ChatCompletionNewParams{
		Model:    p.model,
		Messages: toOpenAIMessages(req.Messages),
		// Enable usage statistics (carried in the last chunk)
		StreamOptions: openai.ChatCompletionStreamOptionsParam{
			IncludeUsage: openai.Bool(true),
		},
	}
	if len(req.Tools) > 0 {
		params.Tools = toOpenAITools(req.Tools)
		params.ToolChoice = toToolChoice(req.ToolChoice)
	}
	// Pass through general sampling parameters
	if req.Temperature != nil {
		params.Temperature = openai.Float(float64(*req.Temperature))
	}
	if req.MaxTokens != nil {
		params.MaxTokens = openai.Int(int64(*req.MaxTokens))
	}
	if req.TopP != nil {
		params.TopP = openai.Float(float64(*req.TopP))
	}
	if len(req.Stop) > 0 {
		params.Stop = openai.ChatCompletionNewParamsStopUnion{OfStringArray: req.Stop}
	}
	// Variant generation defaults: applied only where the caller's ModelRequest did not set a value
	// (request-level wins over variant default). This keeps a single source of truth for which fields
	// were explicitly chosen per turn.
	applyVariantDefaults(&params, req, p)

	// DEBUG hook: when CREATOR_AGENT_DEBUG=1, print a summary of the real params (API key excluded)
	if os.Getenv("CREATOR_AGENT_DEBUG") == "1" {
		fmt.Fprintf(os.Stderr, "[openai] stream: model=%s msgs=%d tools=%d toolChoice=%d temp=%v maxTokens=%v topP=%v\n",
			p.model, len(req.Messages), len(req.Tools), req.ToolChoice, req.Temperature, req.MaxTokens, req.TopP)
	}

	// Total streaming timeout: if the upstream ctx has no deadline and a timeout is configured, wrap it (cancel is released inside the goroutine)
	streamCtx := ctx
	var cancel context.CancelFunc
	if p.timeout > 0 {
		if _, ok := ctx.Deadline(); !ok {
			streamCtx, cancel = context.WithTimeout(ctx, p.timeout)
		}
	}

	// Variant request overrides: headers merge onto the HTTP request, body keys merge into the JSON
	// payload. Passed as per-call SDK options so they compose with the client-level base URL/auth.
	streamOpts := variantRequestOptions(p)

	// NewStreaming returns a single-value stream; setup/stream errors are retrieved via stream.Err()
	stream := p.client.Chat.Completions.NewStreaming(streamCtx, params, streamOpts...)

	ch := make(chan core.ModelEvent)
	go func() {
		defer close(ch)
		if cancel != nil {
			defer cancel()
		}
		defer stream.Close()
		// recover: catch SDK or network layer panics to prevent process crashes.
		defer func() {
			if r := recover(); r != nil {
				stack := debug.Stack()
				// Mirror to stderr: in TUI mode fd 2 is redirected to a log file, so the
				// full panic + stack is preserved on disk for post-mortem (the adapter
				// cannot import the TUI crash logger, so stderr is the shared channel).
				// In headless/REPL mode stderr stays on the terminal and remains visible.
				fmt.Fprintf(os.Stderr, "openai stream panic: %v\n%s\n", r, stack)
				select {
				case ch <- core.MError{Err: fmt.Errorf("openai stream panicked: %v\n%s", r, stack)}:
				default:
				}
			}
		}()
		// send: context-aware send (exits when consumer disconnects or ctx is cancelled)
		send := func(ev core.ModelEvent) bool {
			select {
			case ch <- ev:
				return true
			case <-streamCtx.Done():
				return false
			}
		}
		// indexToID: maps argument increments back to the correct tool_call (first chunk carries ID, subsequent ones only carry index)
		indexToID := make(map[int64]string)
		for stream.Next() {
			chunk := stream.Current()
			for _, ev := range convertChunk(chunk, indexToID) {
				if !send(ev) {
					return
				}
			}
		}
		if err := stream.Err(); err != nil {
			// Distinguish: timeout (streamCtx deadline triggered) -> non-blocking MError (streamCtx already done, send will fail);
			// user cancellation (upstream ctx) -> silent; others -> MError
			if errors.Is(streamCtx.Err(), context.DeadlineExceeded) {
				select {
				case ch <- core.MError{Err: fmt.Errorf("openai stream timeout after %v: %w", p.timeout, err)}:
				default:
				}
			} else if !errors.Is(err, context.Canceled) {
				_ = send(core.MError{Err: convertErr(err)})
			}
		}
	}()
	return ch, nil
}

// applyVariantDefaults fills in the variant's generation defaults for any field the per-turn
// ModelRequest left unset (request value always wins). Kept as a free function over (params, req)
// for direct unit testing without a live Provider/SDK.
func applyVariantDefaults(params *openai.ChatCompletionNewParams, req core.ModelRequest, p *Provider) {
	if p == nil {
		return
	}
	if req.Temperature == nil && p.temperature != nil {
		params.Temperature = openai.Float(*p.temperature)
	}
	if req.TopP == nil && p.topP != nil {
		params.TopP = openai.Float(*p.topP)
	}
	if req.MaxTokens == nil && p.maxTokens != nil {
		params.MaxTokens = openai.Int(*p.maxTokens)
	}
}

// variantRequestOptions builds the per-call SDK options that merge the variant's header and body
// overrides onto the request. Returns nil (not an empty slice) when there is nothing to apply so
// the variadic spread stays a no-op.
func variantRequestOptions(p *Provider) []option.RequestOption {
	if p == nil || (len(p.extraHeaders) == 0 && len(p.extraBody) == 0) {
		return nil
	}
	opts := make([]option.RequestOption, 0, len(p.extraHeaders)+len(p.extraBody))
	// Headers: deterministic order for stable request shape (and stable logs/debug).
	hk := make([]string, 0, len(p.extraHeaders))
	for k := range p.extraHeaders {
		hk = append(hk, k)
	}
	sort.Strings(hk)
	for _, k := range hk {
		opts = append(opts, option.WithHeader(k, p.extraHeaders[k]))
	}
	// Body keys: order follows Go's map iteration; values are opaque to us (the endpoint decides).
	bk := make([]string, 0, len(p.extraBody))
	for k := range p.extraBody {
		bk = append(bk, k)
	}
	sort.Strings(bk)
	for _, k := range bk {
		opts = append(opts, option.WithJSONSet(k, p.extraBody[k]))
	}
	return opts
}

// convertErr classifies openai-go errors by HTTP status code into core error types
// for targeted handling by upper layers (reactive / retry). Non-openai.Error (network / timeout / ctx) are returned as-is.
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

// toToolChoice maps core.ToolChoice to openai ToolChoice parameter (Auto = default, not set).
func toToolChoice(tc core.ToolChoice) openai.ChatCompletionToolChoiceOptionUnionParam {
	switch tc {
	case core.ToolNone:
		return openai.ChatCompletionToolChoiceOptionUnionParam{OfAuto: openai.String("none")}
	case core.ToolRequired:
		return openai.ChatCompletionToolChoiceOptionUnionParam{OfAuto: openai.String("required")}
	default: // ToolAuto = default (not setting it explicitly also works; here we leave it empty)
		return openai.ChatCompletionToolChoiceOptionUnionParam{}
	}
}

// toOpenAIMessages maps core.Message to openai-go message parameters.
func toOpenAIMessages(msgs []core.Message) []openai.ChatCompletionMessageParamUnion {
	out := make([]openai.ChatCompletionMessageParamUnion, 0, len(msgs))
	for _, m := range msgs {
		switch m.Role {
		case core.RoleSystem:
			out = append(out, openai.SystemMessage(m.Content))
		case core.RoleUser:
			out = append(out, openai.UserMessage(m.Content))
		case core.RoleAssistant:
			out = append(out, toAssistantMessage(m))
		case core.RoleTool:
			out = append(out, openai.ToolMessage(m.Content, m.ToolCallID))
		}
	}
	return out
}

// toAssistantMessage builds an assistant message (may include tool_calls for multi-turn history backfill).
func toAssistantMessage(m core.Message) openai.ChatCompletionMessageParamUnion {
	if len(m.ToolCalls) == 0 {
		return openai.AssistantMessage(m.Content)
	}
	toolCalls := make([]openai.ChatCompletionMessageToolCallParam, 0, len(m.ToolCalls))
	for _, tc := range m.ToolCalls {
		toolCalls = append(toolCalls, openai.ChatCompletionMessageToolCallParam{
			ID: tc.ID,
			Function: openai.ChatCompletionMessageToolCallFunctionParam{
				Name:      tc.Name,
				Arguments: string(tc.Input),
			},
		})
	}
	return openai.ChatCompletionMessageParamUnion{
		OfAssistant: &openai.ChatCompletionAssistantMessageParam{
			Content: openai.ChatCompletionAssistantMessageParamContentUnion{
				OfString: openai.String(m.Content),
			},
			ToolCalls: toolCalls,
		},
	}
}

// toOpenAITools maps core.ToolInfo to openai-go tool parameters.
func toOpenAITools(tools []core.ToolInfo) []openai.ChatCompletionToolParam {
	out := make([]openai.ChatCompletionToolParam, 0, len(tools))
	for _, t := range tools {
		fn := oshared.FunctionDefinitionParam{
			Name:        t.Name,
			Description: openai.String(t.Description),
			Parameters:  toFunctionParameters(t.InputSchema),
		}
		out = append(out, openai.ChatCompletionToolParam{
			Function: fn,
		})
	}
	return out
}

// toFunctionParameters converts a JSON schema (json.RawMessage) into SDK FunctionParameters (map).
// On parse failure, falls back to the minimal valid schema (object + empty properties) so the tool remains usable.
func toFunctionParameters(raw json.RawMessage) oshared.FunctionParameters {
	if len(raw) == 0 {
		return oshared.FunctionParameters{"type": "object", "properties": map[string]any{}}
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil || m == nil {
		return oshared.FunctionParameters{"type": "object", "properties": map[string]any{}}
	}
	return oshared.FunctionParameters(m)
}

// convertChunk: one openai ChatCompletionChunk -> multiple core.ModelEvent.
func convertChunk(chunk openai.ChatCompletionChunk, indexToID map[int64]string) []core.ModelEvent {
	var events []core.ModelEvent

	// usage (only in the last chunk; requires StreamOptions.IncludeUsage).
	// Check prompt||completion: some compatible endpoints report TotalTokens=0 but sub-items have values, so checking only TotalTokens would miss them.
	if chunk.Usage.PromptTokens > 0 || chunk.Usage.CompletionTokens > 0 {
		events = append(events, core.MUsage{Usage: core.Usage{
			InputTokens:  int(chunk.Usage.PromptTokens),
			OutputTokens: int(chunk.Usage.CompletionTokens),
		}})
	}

	// reasoning_content (DeepSeek and other compatible endpoints' thinking; openai-go Delta lacks this field, extracted from RawJSON)
	if rc := extractReasoning(chunk); rc != "" {
		events = append(events, core.MThinkingDelta{Delta: rc})
	}

	for _, choice := range chunk.Choices {
		d := choice.Delta
		if d.Content != "" {
			events = append(events, core.MTextDelta{Delta: d.Content})
		}
		// model refusal is passed through as text so the user can see it
		if d.Refusal != "" {
			events = append(events, core.MTextDelta{Delta: d.Refusal})
		}
		for _, tc := range d.ToolCalls {
			idx := tc.Index
			if idx < 0 {
				idx = 0 // clamp (some endpoints like Bedrock return -1)
			}
			id := tc.ID
			if id == "" {
				id = indexToID[idx] // arguments increment: look up id by index
			} else {
				indexToID[idx] = id // first chunk: record index->id
			}
			events = append(events, core.MToolUseDelta{
				ID:        id,
				Name:      tc.Function.Name,
				DeltaJSON: tc.Function.Arguments,
			})
		}
		if choice.FinishReason != "" {
			events = append(events, core.MFinish{Reason: choice.FinishReason})
		}
	}
	return events
}

// extractReasoning extracts reasoning_content from the chunk's raw JSON (thinking delta from OpenAI-compatible endpoints).
// openai-go v1.x Delta struct lacks this field, but compatible endpoints (DeepSeek) return it at the delta level,
// which ends up in RawJSON. This function parses it out.
//
// Performance: called for every chunk (high-frequency streaming). Most chunks do not contain reasoning_content
// (plain text replies, or reasoning phase already ended). A cheap strings.Contains pre-filter skips full json.Unmarshal
// when the field name is absent. This saves deserialization for the majority of chunks in long replies.
func extractReasoning(chunk openai.ChatCompletionChunk) string {
	raw := chunk.RawJSON()
	if raw == "" {
		return ""
	}
	// Cheap pre-filter: skip full parsing if the field name is not in the JSON (most chunks hit this branch)
	if !strings.Contains(raw, "reasoning_content") {
		return ""
	}
	var v struct {
		Choices []struct {
			Delta struct {
				ReasoningContent string `json:"reasoning_content"`
			} `json:"delta"`
		} `json:"choices"`
	}
	if json.Unmarshal([]byte(raw), &v) != nil {
		return ""
	}
	if len(v.Choices) == 0 {
		return ""
	}
	return v.Choices[0].Delta.ReasoningContent
}
