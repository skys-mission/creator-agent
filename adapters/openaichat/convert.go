package openaichat

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/openai/openai-go/packages/param"
	"github.com/openai/openai-go/packages/respjson"
	"github.com/openai/openai-go/shared"

	"github.com/skys-mission/creator-agent/contract"
)

// defaultReasoningKey is the de-facto wire field for thinking content: DeepSeek's original
// convention, adopted by Moonshot Kimi, pre-rename vLLM and most OpenAI-compatible gateways.
const defaultReasoningKey = "reasoning_content"

// reasoningKeys is the de-facto wire field set for thinking content on OpenAI-compatible
// endpoints, in inbound scan priority order (see docs/architecture.md §4 for the survey):
//
//	reasoning_content — DeepSeek's convention; the ecosystem default
//	reasoning_details — OpenRouter (sometimes array-shaped)
//	reasoning         — OpenAI GPT-OSS guidance; current vLLM (and its request side)
//	reasoning_text    — observed dialect (minimax-code scans it)
//
// The protocol never standardized a field, so inbound accepts any of them and outbound echoes
// the dialect the endpoint actually spoke (learned per endpoint by reasoningDialect, pinned by
// Config.ReasoningKey).
var reasoningKeys = []string{defaultReasoningKey, "reasoning_details", "reasoning", "reasoning_text"}

// extractReasoning returns the reasoning text from inbound message/delta extras together with
// the wire key it was found under. The first string-valued key wins; non-string values (vLLM's
// `reasoning_content: null` placeholder, OpenRouter's array-shaped `reasoning_details`) are
// skipped. With explicitKey set (a pinned dialect), only that key is consulted.
func extractReasoning(extras map[string]respjson.Field, explicitKey string) (text, key string, ok bool) {
	if explicitKey != "" {
		text, ok = reasoningString(extras, explicitKey)
		return text, explicitKey, ok
	}
	for _, k := range reasoningKeys {
		if text, ok := reasoningString(extras, k); ok {
			return text, k, true
		}
	}
	return "", "", false
}

// reasoningDialect is the per-endpoint wire-field dialect for thinking content ("reply in the
// dialect the peer spoke"). It observes inbound responses, remembers which wire key carried
// reasoning, and hands that key to outbound history serialization. Detection never clears: a
// response without reasoning keeps the last known dialect, and an endpoint that switches
// dialects mid-session is adapted to on its next observation. An explicit (pinned) key always
// wins and disables detection. The dialect is a property of the endpoint, so one instance is
// shared by reference across streams.
type reasoningDialect struct {
	explicit string     // pinned by config; disables detection when non-empty
	mu       sync.Mutex // guards detected (Stream may be called concurrently)
	detected string     // last observed inbound key
}

func newReasoningDialect(explicit string) *reasoningDialect {
	return &reasoningDialect{explicit: explicit}
}

// observe extracts the reasoning text from inbound extras, learning the wire key it arrived
// under unless the dialect is pinned.
func (d *reasoningDialect) observe(extras map[string]respjson.Field) (string, bool) {
	text, key, ok := extractReasoning(extras, d.explicit)
	if ok && d.explicit == "" {
		d.mu.Lock()
		d.detected = key
		d.mu.Unlock()
	}
	return text, ok
}

// outboundKey returns the wire key to serialize thinking content into on outbound messages:
// the pinned key, else the last observed key, else the de-facto default.
func (d *reasoningDialect) outboundKey() string {
	if d.explicit != "" {
		return d.explicit
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.detected != "" {
		return d.detected
	}
	return defaultReasoningKey
}

// reasoningString reads one candidate key, accepting string values only.
func reasoningString(extras map[string]respjson.Field, key string) (string, bool) {
	f, ok := extras[key]
	if !ok {
		return "", false
	}
	var text string
	if err := json.Unmarshal([]byte(f.Raw()), &text); err != nil || text == "" {
		return "", false
	}
	return text, true
}

// buildParams translates a protocol-neutral model request into the Chat Completions request.
// Only what the protocol can faithfully carry is mapped; anything else is an error, never a
// silent drop (one documented carve-out: kind=toggle reasoning, see below). echoKey non-empty
// enables thinking round-trip on assistant history (see Client.echoKey).
func buildParams(modelID string, req *contract.ModelRequest, echoKey string, reasoning contract.Reasoning) (openai.ChatCompletionNewParams, error) {
	if req == nil || len(req.Messages) == 0 {
		return openai.ChatCompletionNewParams{}, errors.New("openaichat: request has no messages")
	}
	msgs := make([]openai.ChatCompletionMessageParamUnion, 0, len(req.Messages))
	for i, m := range req.Messages {
		p, err := messageParam(m, echoKey)
		if err != nil {
			return openai.ChatCompletionNewParams{}, fmt.Errorf("openaichat: message %d: %w", i, err)
		}
		msgs = append(msgs, p)
	}
	tools, err := toolParams(req.Tools)
	if err != nil {
		return openai.ChatCompletionNewParams{}, err
	}
	out := openai.ChatCompletionNewParams{
		Model:    modelID,
		Messages: msgs,
		Tools:    tools,
		// Usage is how the loop accounts cost and context budget; always ask for the totals.
		StreamOptions: openai.ChatCompletionStreamOptionsParam{IncludeUsage: param.NewOpt(true)},
	}
	// Thinking-depth control. kind=effort sends the model's default level as the top-level enum
	// field reasoning_effort (survey in docs/architecture.md §4); kind=none sends nothing (a
	// stray parameter 400s models that don't take one); kind=toggle has no standard field here
	// and is carried by request options instead — see reasoningRequestOptions.
	if reasoning.Kind == contract.ReasoningKindEffort && reasoning.Default != "" {
		out.ReasoningEffort = openai.ReasoningEffort(reasoning.Default)
	}
	return out, nil
}

// toggleWirePaths maps the boolean-shaped toggle dialects onto their dotted wire path. Dots
// nest ("a.b" -> {"a": {"b": ...}}). The surveyed shapes (docs/architecture.md §4) disagree on
// both the field name and the nesting, hence dialects instead of one universal field:
//
//	enable_thinking: true|false                    DashScope/Qwen and most CN-compatible gateways
//	think: true|false                              Ollama and its gateways
//	chat_template_kwargs.enable_thinking           vLLM/SGLang serving Qwen3, Gemma
//	chat_template_kwargs.thinking                  vLLM serving Granite, DeepSeek-V3.1, Holo2
//	reasoning.enabled                              OpenRouter's reasoning object, boolean arm
//
// ToggleDialectThinkingType is not boolean-shaped and stays in reasoningRequestOptions itself.
var toggleWirePaths = map[contract.ReasoningToggleDialect]string{
	contract.ToggleDialectEnableThinking:             "enable_thinking",
	contract.ToggleDialectThink:                      "think",
	contract.ToggleDialectChatTemplateEnableThinking: "chat_template_kwargs.enable_thinking",
	contract.ToggleDialectChatTemplateThinking:       "chat_template_kwargs.thinking",
	contract.ToggleDialectReasoningEnabled:           "reasoning.enabled",
}

// reasoningRequestOptions maps kind=toggle onto the gateway's switch field (see toggleWirePaths
// for the surveyed shapes; ToggleDialectCustom sends a boolean at the user-named path).
// kind=effort needs no options (buildParams carries reasoning_effort); kind=none sends nothing.
func reasoningRequestOptions(r contract.Reasoning) []option.RequestOption {
	if r.Kind != contract.ReasoningKindToggle {
		return nil
	}
	on := r.Default == contract.ReasoningToggleOn
	if r.ToggleDialect == contract.ToggleDialectThinkingType {
		typ := "disabled"
		if on {
			typ = "enabled"
		}
		return []option.RequestOption{option.WithJSONSet("thinking", map[string]any{"type": typ})}
	}
	path := r.ToggleField
	if r.ToggleDialect != contract.ToggleDialectCustom {
		path = toggleWirePaths[r.ToggleDialect]
	}
	return boolFieldOptions(path, on)
}

// boolFieldOptions sends on/off as a boolean at the given dotted wire path. An empty or unknown
// path sends nothing rather than a stray field: a wrong switch 400s some gateways.
func boolFieldOptions(path string, on bool) []option.RequestOption {
	if path == "" {
		return nil
	}
	parts := strings.Split(path, ".")
	v := any(on)
	for i := len(parts) - 1; i > 0; i-- {
		v = map[string]any{parts[i]: v}
	}
	return []option.RequestOption{option.WithJSONSet(parts[0], v)}
}

// messageParam maps one conversation message. Request-side fields with no home in this protocol
// (ReasoningToken, ToolIsError, Extra) are the loop's or another protocol's concern and are not
// sent. When echoKey is non-empty, assistant thinking is round-tripped into outbound history
// under that wire key — servers that don't understand the field ignore it (the de-facto
// ecosystem behavior); gateways that require thinking in history need it.
func messageParam(m contract.Message, echoKey string) (openai.ChatCompletionMessageParamUnion, error) {
	if len(m.Parts) > 0 {
		return openai.ChatCompletionMessageParamUnion{},
			fmt.Errorf("role %s: multimodal parts are not supported by the openai-chat-completions adapter yet", m.Role)
	}
	switch m.Role {
	case contract.RoleSystem:
		return openai.SystemMessage(m.Content), nil
	case contract.RoleUser:
		return openai.UserMessage(m.Content), nil
	case contract.RoleAssistant:
		if m.Content == "" && len(m.ToolCalls) == 0 {
			return openai.ChatCompletionMessageParamUnion{}, errors.New("assistant message has neither content nor tool calls")
		}
		assistant := openai.ChatCompletionAssistantMessageParam{}
		if m.Content != "" {
			assistant.Content = openai.ChatCompletionAssistantMessageParamContentUnion{OfString: param.NewOpt(m.Content)}
		}
		for _, tc := range m.ToolCalls {
			assistant.ToolCalls = append(assistant.ToolCalls, openai.ChatCompletionMessageToolCallParam{
				ID: tc.ID,
				Function: openai.ChatCompletionMessageToolCallFunctionParam{
					Name:      tc.Name,
					Arguments: string(tc.Input),
				},
			})
		}
		if echoKey != "" && m.Reasoning != "" {
			assistant.SetExtraFields(map[string]any{echoKey: m.Reasoning})
		}
		return openai.ChatCompletionMessageParamUnion{OfAssistant: &assistant}, nil
	case contract.RoleTool:
		if m.ToolCallID == "" {
			return openai.ChatCompletionMessageParamUnion{}, errors.New("tool message is missing ToolCallID")
		}
		return openai.ToolMessage(m.Content, m.ToolCallID), nil
	default:
		return openai.ChatCompletionMessageParamUnion{}, fmt.Errorf("unknown role %q", m.Role)
	}
}

// toolParams maps the tool surface offered to the model.
func toolParams(tools []contract.ToolInfo) ([]openai.ChatCompletionToolParam, error) {
	if len(tools) == 0 {
		return nil, nil
	}
	out := make([]openai.ChatCompletionToolParam, 0, len(tools))
	for _, t := range tools {
		fn := shared.FunctionDefinitionParam{Name: t.Name}
		if t.Description != "" {
			fn.Description = param.NewOpt(t.Description)
		}
		if len(t.InputSchema) > 0 {
			var schema map[string]any
			if err := json.Unmarshal(t.InputSchema, &schema); err != nil {
				return nil, fmt.Errorf("openaichat: tool %q: invalid input schema: %w", t.Name, err)
			}
			fn.Parameters = schema
		}
		out = append(out, openai.ChatCompletionToolParam{Function: fn})
	}
	return out, nil
}

// streamMapper converts stream chunks into contract events. It carries the per-stream state:
// tool calls stream as fragments keyed by index. Inbound reasoning is always extracted — whether
// the user sees it is the UI's decision, never the wire's — and the wire key it arrived under
// feeds the shared dialect learner (see reasoningDialect).
type streamMapper struct {
	toolIDs map[int64]string // tool call index -> call ID
	dialect *reasoningDialect
}

func newStreamMapper(dialect *reasoningDialect) *streamMapper {
	return &streamMapper{
		toolIDs: map[int64]string{},
		dialect: dialect,
	}
}

// Map translates one chunk into zero or more events. Only choice 0 is used (the client never
// requests n>1).
func (s *streamMapper) Map(chunk *openai.ChatCompletionChunk) []contract.Event {
	var evs []contract.Event

	// A usage-only chunk (include_usage) has no choices; some servers instead attach usage to
	// the last chunk that still carries choices.
	if len(chunk.Choices) == 0 || usagePresent(chunk.Usage) {
		evs = append(evs, contract.UsageEvent{Usage: contract.Usage{
			InputTokens:  int(chunk.Usage.PromptTokens),
			OutputTokens: int(chunk.Usage.CompletionTokens),
			CacheRead:    int(chunk.Usage.PromptTokensDetails.CachedTokens),
		}})
	}
	if len(chunk.Choices) == 0 {
		return evs
	}

	d := chunk.Choices[0].Delta
	// Thinking text rides delta extension fields (see reasoningKeys). Emitted before content
	// because it conceptually precedes the answer within the same chunk. Note: SDK response
	// extras are always recorded with Valid()==false; Raw() is where the wire value lives.
	if text, ok := s.dialect.observe(d.JSON.ExtraFields); ok {
		evs = append(evs, contract.ThinkingEvent{Delta: text})
	}
	if d.Content != "" {
		evs = append(evs, contract.TextEvent{Delta: d.Content})
	}
	if d.Refusal != "" {
		evs = append(evs, contract.TextEvent{Delta: d.Refusal})
	}
	for _, tc := range d.ToolCalls {
		id := s.toolIDs[tc.Index]
		if id == "" {
			id = tc.ID
			if id == "" {
				// Broken dialects may omit the ID; synthesize a stable one for correlation.
				id = fmt.Sprintf("call_%d", tc.Index)
			}
			s.toolIDs[tc.Index] = id
			evs = append(evs, contract.ToolUseStartEvent{ID: id, Name: tc.Function.Name})
		}
		if tc.Function.Arguments != "" {
			evs = append(evs, contract.ToolUseDeltaEvent{ID: id, DeltaJSON: tc.Function.Arguments})
		}
	}
	return evs
}

// usagePresent reports whether a chunk carries real usage numbers.
func usagePresent(u openai.CompletionUsage) bool {
	return u.PromptTokens > 0 || u.CompletionTokens > 0 || u.TotalTokens > 0 ||
		u.PromptTokensDetails.CachedTokens > 0
}
