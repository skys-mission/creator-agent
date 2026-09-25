package openaichat

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/packages/respjson"

	"github.com/skys-mission/creator-agent/contract"
)

// wire* probe structs mirror the JSON actually sent on the wire, so tests assert protocol
// reality rather than SDK internals.

type wireToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type wireMessage struct {
	Role       string         `json:"role"`
	Content    string         `json:"content"`
	ToolCallID string         `json:"tool_call_id"`
	ToolCalls  []wireToolCall `json:"tool_calls"`
}

type wireTool struct {
	Type     string `json:"type"`
	Function struct {
		Name        string         `json:"name"`
		Description string         `json:"description"`
		Parameters  map[string]any `json:"parameters"`
	} `json:"function"`
}

type wireParams struct {
	Model           string        `json:"model"`
	Messages        []wireMessage `json:"messages"`
	Tools           []wireTool    `json:"tools"`
	ReasoningEffort string        `json:"reasoning_effort"`
	StreamOptions   struct {
		IncludeUsage bool `json:"include_usage"`
	} `json:"stream_options"`
}

func marshalParams(t *testing.T, params openai.ChatCompletionNewParams) (wire wireParams, raw string) {
	t.Helper()
	data, err := json.Marshal(params)
	if err != nil {
		t.Fatalf("marshal params: %v", err)
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		t.Fatalf("unmarshal wire probe: %v", err)
	}
	return wire, string(data)
}

func TestBuildParamsMessageMapping(t *testing.T) {
	req := &contract.ModelRequest{
		Messages: []contract.Message{
			contract.SystemMessage("sys"),
			contract.UserMessage("hi"),
			contract.AssistantMessageWithReasoning(
				"let me read", "thinking...", "sig-token",
				contract.ToolCall{ID: "call_1", Name: "read", Input: json.RawMessage(`{"p":"a.txt"}`)}),
			contract.ToolMessage("file content", "call_1", "read"),
		},
	}
	params, err := buildParams("test-model", req, "", contract.Reasoning{})
	if err != nil {
		t.Fatalf("buildParams() = %v, want nil", err)
	}
	wire, raw := marshalParams(t, params)

	if wire.Model != "test-model" {
		t.Fatalf("model = %q, want test-model", wire.Model)
	}
	if !wire.StreamOptions.IncludeUsage {
		t.Fatal("stream_options.include_usage must be true so usage flows back")
	}
	if len(wire.Messages) != 4 {
		t.Fatalf("got %d messages, want 4", len(wire.Messages))
	}
	if got := wire.Messages[0]; got.Role != "system" || got.Content != "sys" {
		t.Fatalf("message 0 = %+v, want system/sys", got)
	}
	if got := wire.Messages[1]; got.Role != "user" || got.Content != "hi" {
		t.Fatalf("message 1 = %+v, want user/hi", got)
	}
	assistant := wire.Messages[2]
	if assistant.Role != "assistant" || assistant.Content != "let me read" {
		t.Fatalf("message 2 = %+v, want assistant with text", assistant)
	}
	if len(assistant.ToolCalls) != 1 {
		t.Fatalf("message 2 tool calls = %d, want 1", len(assistant.ToolCalls))
	}
	tc := assistant.ToolCalls[0]
	if tc.ID != "call_1" || tc.Type != "function" || tc.Function.Name != "read" ||
		tc.Function.Arguments != `{"p":"a.txt"}` {
		t.Fatalf("message 2 tool call = %+v", tc)
	}
	// Reasoning has no home in this protocol and must not leak into the request.
	if strings.Contains(raw, "sig-token") || strings.Contains(raw, "thinking...") {
		t.Fatalf("reasoning fields leaked into the request: %s", raw)
	}
	if got := wire.Messages[3]; got.Role != "tool" || got.Content != "file content" || got.ToolCallID != "call_1" {
		t.Fatalf("message 3 = %+v, want tool result bound to call_1", got)
	}
}

func TestBuildParamsReasoningEffort(t *testing.T) {
	req := &contract.ModelRequest{Messages: []contract.Message{contract.UserMessage("hi")}}

	// kind=effort: the default level goes out as the top-level wire enum reasoning_effort.
	params, err := buildParams("m", req, "", contract.Reasoning{
		Kind: contract.ReasoningKindEffort,
		Efforts: []string{
			contract.ReasoningEffortLow, contract.ReasoningEffortHigh,
		},
		Default: contract.ReasoningEffortHigh,
	})
	if err != nil {
		t.Fatalf("buildParams() = %v, want nil", err)
	}
	wire, _ := marshalParams(t, params)
	if wire.ReasoningEffort != "high" {
		t.Fatalf("reasoning_effort = %q, want high", wire.ReasoningEffort)
	}

	// Levels without an SDK constant ("max") still pass through as the wire string.
	params, err = buildParams("m", req, "", contract.Reasoning{
		Kind:    contract.ReasoningKindEffort,
		Efforts: []string{contract.ReasoningEffortMax},
		Default: contract.ReasoningEffortMax,
	})
	if err != nil {
		t.Fatalf("buildParams() = %v, want nil", err)
	}
	wire, _ = marshalParams(t, params)
	if wire.ReasoningEffort != "max" {
		t.Fatalf("reasoning_effort = %q, want max", wire.ReasoningEffort)
	}

	// kind=none: nothing is sent (fail-safe default).
	params, err = buildParams("m", req, "", contract.Reasoning{})
	if err != nil {
		t.Fatalf("buildParams() = %v, want nil", err)
	}
	wire, _ = marshalParams(t, params)
	if wire.ReasoningEffort != "" {
		t.Fatalf("reasoning_effort = %q, want absent", wire.ReasoningEffort)
	}

	// kind=toggle: no standard field exists on this protocol (gateways name their own switch),
	// so the declaration is preserved but not sent — the documented carve-out.
	params, err = buildParams("m", req, "", contract.Reasoning{
		Kind:    contract.ReasoningKindToggle,
		Default: contract.ReasoningToggleOn,
	})
	if err != nil {
		t.Fatalf("buildParams() = %v, want nil", err)
	}
	wire, _ = marshalParams(t, params)
	if wire.ReasoningEffort != "" {
		t.Fatalf("reasoning_effort = %q, want absent for toggle kind", wire.ReasoningEffort)
	}
}

func TestBuildParamsTools(t *testing.T) {
	req := &contract.ModelRequest{
		Messages: []contract.Message{contract.UserMessage("hi")},
		Tools: []contract.ToolInfo{
			{Name: "read", Description: "read a file", InputSchema: json.RawMessage(`{"type":"object"}`)},
			{Name: "noop"},
		},
	}
	params, err := buildParams("m", req, "", contract.Reasoning{})
	if err != nil {
		t.Fatalf("buildParams() = %v, want nil", err)
	}
	wire, _ := marshalParams(t, params)
	if len(wire.Tools) != 2 {
		t.Fatalf("got %d tools, want 2", len(wire.Tools))
	}
	read := wire.Tools[0]
	if read.Type != "function" || read.Function.Name != "read" || read.Function.Description != "read a file" {
		t.Fatalf("tool 0 = %+v", read)
	}
	if got := read.Function.Parameters; got == nil || got["type"] != "object" {
		t.Fatalf("tool 0 parameters = %v, want parsed schema", got)
	}
	noop := wire.Tools[1]
	if noop.Function.Name != "noop" || noop.Function.Parameters != nil {
		t.Fatalf("tool 1 = %+v, want name only and no parameters", noop)
	}
}

func TestBuildParamsErrors(t *testing.T) {
	tests := []struct {
		name    string
		req     *contract.ModelRequest
		wantErr string
	}{
		{"nil request", nil, "no messages"},
		{"empty messages", &contract.ModelRequest{}, "no messages"},
		{"multimodal parts", &contract.ModelRequest{
			Messages: []contract.Message{contract.UserMessage("hi", contract.Part{Type: "image"})},
		}, "multimodal parts"},
		{"empty assistant", &contract.ModelRequest{
			Messages: []contract.Message{contract.AssistantMessage("")},
		}, "neither content nor tool calls"},
		{"tool without id", &contract.ModelRequest{
			Messages: []contract.Message{contract.ToolMessage("r", "", "t")},
		}, "missing ToolCallID"},
		{"unknown role", &contract.ModelRequest{
			Messages: []contract.Message{{Role: "wizard", Content: "x"}},
		}, "unknown role"},
		{"broken schema", &contract.ModelRequest{
			Messages: []contract.Message{contract.UserMessage("hi")},
			Tools:    []contract.ToolInfo{{Name: "t", InputSchema: json.RawMessage(`{bad`)}},
		}, "invalid input schema"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := buildParams("m", tt.req, "", contract.Reasoning{})
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("buildParams() = %v, want error containing %q", err, tt.wantErr)
			}
		})
	}
}

// chunkFromJSON builds a chunk the way the SDK would decode it from the wire.
func chunkFromJSON(t *testing.T, raw string) *openai.ChatCompletionChunk {
	t.Helper()
	var c openai.ChatCompletionChunk
	if err := json.Unmarshal([]byte(raw), &c); err != nil {
		t.Fatalf("decode chunk: %v", err)
	}
	return &c
}

func TestStreamMapperTextAndUsage(t *testing.T) {
	s := newStreamMapper(newReasoningDialect("", ""))
	evs := s.Map(chunkFromJSON(t,
		`{"id":"1","object":"chat.completion.chunk","created":1,"model":"m","choices":[{"index":0,"delta":{"role":"assistant","content":"Hel"}}]}`))
	want := []contract.Event{contract.TextEvent{Delta: "Hel"}}
	if !reflect.DeepEqual(evs, want) {
		t.Fatalf("first chunk events = %#v, want %#v", evs, want)
	}

	evs = s.Map(chunkFromJSON(t,
		`{"id":"1","object":"chat.completion.chunk","created":1,"model":"m","choices":[],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15,"prompt_tokens_details":{"cached_tokens":3}}}`))
	want = []contract.Event{contract.UsageEvent{Usage: contract.Usage{InputTokens: 10, OutputTokens: 5, CacheRead: 3}}}
	if !reflect.DeepEqual(evs, want) {
		t.Fatalf("usage chunk events = %#v, want %#v", evs, want)
	}

	// A normal chunk without usage must not emit a usage event.
	evs = s.Map(chunkFromJSON(t,
		`{"id":"1","object":"chat.completion.chunk","created":1,"model":"m","choices":[{"index":0,"delta":{"content":"lo"}}],"usage":null}`))
	want = []contract.Event{contract.TextEvent{Delta: "lo"}}
	if !reflect.DeepEqual(evs, want) {
		t.Fatalf("chunk events = %#v, want %#v", evs, want)
	}
}

func TestStreamMapperToolCallFragments(t *testing.T) {
	s := newStreamMapper(newReasoningDialect("", ""))
	var got []contract.Event
	for _, raw := range []string{
		`{"id":"1","object":"chat.completion.chunk","created":1,"model":"m","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_a","type":"function","function":{"name":"read","arguments":""}}]}}]}`,
		`{"id":"1","object":"chat.completion.chunk","created":1,"model":"m","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"p\":"}}]}}]}`,
		`{"id":"1","object":"chat.completion.chunk","created":1,"model":"m","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"a.txt\"}"}}]}}]}`,
		`{"id":"1","object":"chat.completion.chunk","created":1,"model":"m","choices":[{"index":0,"delta":{"tool_calls":[{"index":1,"type":"function","function":{"name":"list"}}]}}]}`,
	} {
		got = append(got, s.Map(chunkFromJSON(t, raw))...)
	}
	want := []contract.Event{
		contract.ToolUseStartEvent{ID: "call_a", Name: "read"},
		contract.ToolUseDeltaEvent{ID: "call_a", DeltaJSON: `{"p":`},
		contract.ToolUseDeltaEvent{ID: "call_a", DeltaJSON: `"a.txt"}`},
		// Second call has no ID on the wire: a stable one is synthesized from the index.
		contract.ToolUseStartEvent{ID: "call_1", Name: "list"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("events = %#v, want %#v", got, want)
	}
}

func TestStreamMapperReasoningExtension(t *testing.T) {
	s := newStreamMapper(newReasoningDialect("", ""))
	evs := s.Map(chunkFromJSON(t,
		`{"id":"1","object":"chat.completion.chunk","created":1,"model":"m","choices":[{"index":0,"delta":{"reasoning_content":"hmm","content":"answer"}}]}`))
	want := []contract.Event{
		contract.ThinkingEvent{Delta: "hmm"},
		contract.TextEvent{Delta: "answer"},
	}
	if !reflect.DeepEqual(evs, want) {
		t.Fatalf("events = %#v, want %#v", evs, want)
	}
}

func TestBuildParamsThinkingEcho(t *testing.T) {
	req := &contract.ModelRequest{
		Messages: []contract.Message{
			contract.AssistantMessageWithReasoning("answer", "the thought", ""),
		},
	}
	messageKey := func(t *testing.T, echoKey string) map[string]any {
		t.Helper()
		params, err := buildParams("m", req, echoKey, contract.Reasoning{})
		if err != nil {
			t.Fatalf("buildParams() = %v, want nil", err)
		}
		data, err := json.Marshal(params)
		if err != nil {
			t.Fatalf("marshal params: %v", err)
		}
		var msg map[string]any
		if err := json.Unmarshal(data, &msg); err != nil {
			t.Fatalf("unmarshal message map: %v", err)
		}
		msgs := msg["messages"].([]any)
		return msgs[0].(map[string]any)
	}

	// Thinking on: thinking round-trips under the de-facto key.
	if got := messageKey(t, defaultReasoningKey); got["reasoning_content"] != "the thought" {
		t.Fatalf("echoed message = %v, want reasoning_content=the thought", got)
	}
	// Pinned dialect: the pinned key wins.
	if got := messageKey(t, "my_think"); got["my_think"] != "the thought" {
		t.Fatalf("echoed message = %v, want my_think=the thought", got)
	}
	// Thinking off: nothing thinking-shaped reaches the wire.
	for key, val := range messageKey(t, "") {
		if key == "reasoning_content" || key == "my_think" || val == "the thought" {
			t.Fatalf("thinking leaked with echo disabled: %v", messageKey(t, ""))
		}
	}
}

func TestExtractReasoning(t *testing.T) {
	deltaExtras := func(t *testing.T, raw string) map[string]respjson.Field {
		t.Helper()
		return chunkFromJSON(t,
			`{"id":"1","object":"chat.completion.chunk","created":1,"model":"m","choices":[{"index":0,"delta":`+raw+`}]}`,
		).Choices[0].Delta.JSON.ExtraFields
	}
	tests := []struct {
		name      string
		delta     string
		explicit  string
		wantText  string
		wantFound bool
	}{
		{"de-facto key first", `{"reasoning_content":"a","reasoning":"b"}`, "", "a", true},
		{"falls through to reasoning", `{"reasoning":"b"}`, "", "b", true},
		{"array-shaped reasoning_details skipped", `{"reasoning_details":[{"text":"x"}],"reasoning":"b"}`, "", "b", true},
		{"null placeholder skipped", `{"reasoning_content":null,"reasoning_text":"t"}`, "", "t", true},
		{"empty string skipped", `{"reasoning_content":"","reasoning":"b"}`, "", "b", true},
		{"explicit key only", `{"reasoning_content":"a","my_think":"c"}`, "my_think", "c", true},
		{"explicit key ignores others", `{"reasoning_content":"a"}`, "my_think", "", false},
		{"no reasoning", `{"content":"hi"}`, "", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			text, _, found := extractReasoning(deltaExtras(t, tt.delta), tt.explicit)
			if found != tt.wantFound || text != tt.wantText {
				t.Fatalf("extractReasoning() = (%q, %v), want (%q, %v)", text, found, tt.wantText, tt.wantFound)
			}
		})
	}
}
