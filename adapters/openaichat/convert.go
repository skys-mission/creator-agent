package openaichat

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/openai/openai-go"
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
// the dialect key (pinned by Config.ReasoningKey or defaulting to reasoning_content).
var reasoningKeys = []string{defaultReasoningKey, "reasoning_details", "reasoning", "reasoning_text"}

// extractReasoning returns the reasoning text from inbound message/delta extras. The first
// string-valued key wins; non-string values (vLLM's `reasoning_content: null` placeholder,
// OpenRouter's array-shaped `reasoning_details`) are skipped. With explicitKey set (a pinned
// dialect), only that key is consulted.
func extractReasoning(extras map[string]respjson.Field, explicitKey string) (string, bool) {
	if explicitKey != "" {
		return reasoningString(extras, explicitKey)
	}
	for _, key := range reasoningKeys {
		if text, ok := reasoningString(extras, key); ok {
			return text, true
		}
	}
	return "", false
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
// silent drop. echoKey non-empty enables thinking round-trip on assistant history (see
// Client.echoKey).
func buildParams(modelID string, req *contract.ModelRequest, echoKey string) (openai.ChatCompletionNewParams, error) {
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
	return openai.ChatCompletionNewParams{
		Model:    modelID,
		Messages: msgs,
		Tools:    tools,
		// Usage is how the loop accounts cost and context budget; always ask for the totals.
		StreamOptions: openai.ChatCompletionStreamOptionsParam{IncludeUsage: param.NewOpt(true)},
	}, nil
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
// the user sees it is the UI's decision, never the wire's.
type streamMapper struct {
	toolIDs      map[int64]string // tool call index -> call ID
	reasoningKey string           // pinned wire key ("" = de-facto scan)
}

func newStreamMapper(reasoningKey string) *streamMapper {
	return &streamMapper{
		toolIDs:      map[int64]string{},
		reasoningKey: strings.TrimSpace(reasoningKey),
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
	if text, ok := extractReasoning(d.JSON.ExtraFields, s.reasoningKey); ok {
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
