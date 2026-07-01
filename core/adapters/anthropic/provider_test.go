package anthropic

import (
	"encoding/json"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"

	core "github.com/skys-mission/creator-agent/core"
)

func TestConvertEvent_MessageStart(t *testing.T) {
	ev := anthropic.MessageStreamEventUnion{
		Type:    "message_start",
		Message: anthropic.Message{Usage: anthropic.Usage{InputTokens: 10, CacheReadInputTokens: 4, CacheCreationInputTokens: 2}},
	}
	got := convertEvent(ev, map[int64]callInfo{})
	if len(got) != 1 {
		t.Fatalf("expected 1 event, got %d", len(got))
	}
	u, ok := got[0].(core.MUsage)
	if !ok || u.Usage.InputTokens != 10 || u.Usage.CacheRead != 4 || u.Usage.CacheWrite != 2 {
		t.Fatalf("bad usage: %+v", got[0])
	}
}

func TestConvertEvent_TextDelta(t *testing.T) {
	ev := anthropic.MessageStreamEventUnion{
		Type:  "content_block_delta",
		Index: 0,
		Delta: anthropic.MessageStreamEventUnionDelta{Type: "text_delta", Text: "hello"},
	}
	got := convertEvent(ev, map[int64]callInfo{})
	if len(got) != 1 {
		t.Fatalf("expected 1 event, got %d", len(got))
	}
	if td, ok := got[0].(core.MTextDelta); !ok || td.Delta != "hello" {
		t.Fatalf("expected MTextDelta{hello}, got %#v", got[0])
	}
}

func TestConvertEvent_ThinkingAndSignature(t *testing.T) {
	blocks := map[int64]callInfo{}
	think := anthropic.MessageStreamEventUnion{
		Type:  "content_block_delta",
		Index: 0,
		Delta: anthropic.MessageStreamEventUnionDelta{Type: "thinking_delta", Thinking: "reasoning..."},
	}
	sig := anthropic.MessageStreamEventUnion{
		Type:  "content_block_delta",
		Index: 0,
		Delta: anthropic.MessageStreamEventUnionDelta{Type: "signature_delta", Signature: "SIGN"},
	}
	got := convertEvent(think, blocks)
	if len(got) != 1 {
		t.Fatalf("thinking: expected 1 event, got %d", len(got))
	}
	if td, ok := got[0].(core.MThinkingDelta); !ok || td.Delta != "reasoning..." {
		t.Fatalf("expected MThinkingDelta, got %#v", got[0])
	}
	got = convertEvent(sig, blocks)
	if len(got) != 1 {
		t.Fatalf("signature: expected 1 event, got %d", len(got))
	}
	if sd, ok := got[0].(core.MThinkingSignature); !ok || sd.Signature != "SIGN" {
		t.Fatalf("expected MThinkingSignature{SIGN}, got %#v", got[0])
	}
}

func TestConvertEvent_FunctionCall(t *testing.T) {
	blocks := map[int64]callInfo{}
	start := anthropic.MessageStreamEventUnion{
		Type:         "content_block_start",
		Index:        1,
		ContentBlock: anthropic.ContentBlockStartEventContentBlockUnion{Type: "tool_use", ID: "t1", Name: "bash"},
	}
	convertEvent(start, blocks)
	if ci, ok := blocks[1]; !ok || ci.id != "t1" || ci.name != "bash" {
		t.Fatalf("block not recorded: %+v", blocks)
	}
	delta := anthropic.MessageStreamEventUnion{
		Type:  "content_block_delta",
		Index: 1,
		Delta: anthropic.MessageStreamEventUnionDelta{Type: "input_json_delta", PartialJSON: `{"cmd":"ls"}`},
	}
	got := convertEvent(delta, blocks)
	if len(got) != 1 {
		t.Fatalf("expected 1 event, got %d", len(got))
	}
	if tud, ok := got[0].(core.MToolUseDelta); !ok || tud.ID != "t1" || tud.Name != "bash" || tud.DeltaJSON != `{"cmd":"ls"}` {
		t.Fatalf("bad tool delta: %#v", got[0])
	}
}

func TestConvertEvent_MessageDelta(t *testing.T) {
	ev := anthropic.MessageStreamEventUnion{
		Type:  "message_delta",
		Delta: anthropic.MessageStreamEventUnionDelta{StopReason: "end_turn"},
		Usage: anthropic.MessageDeltaUsage{OutputTokens: 8},
	}
	got := convertEvent(ev, map[int64]callInfo{})
	if len(got) != 2 {
		t.Fatalf("expected usage+finish (2), got %d", len(got))
	}
	if u, ok := got[0].(core.MUsage); !ok || u.Usage.OutputTokens != 8 {
		t.Fatalf("bad output usage: %+v", got[0])
	}
	if f, ok := got[1].(core.MFinish); !ok || f.Reason != "end_turn" {
		t.Fatalf("expected MFinish{end_turn}, got %#v", got[1])
	}
}

func TestToAnthropicToolChoice(t *testing.T) {
	if (toAnthropicToolChoice(core.ToolAuto) != anthropic.ToolChoiceUnionParam{}) {
		t.Fatal("auto should be zero value")
	}
	if toAnthropicToolChoice(core.ToolNone).OfNone == nil {
		t.Fatal("none expected")
	}
	if toAnthropicToolChoice(core.ToolRequired).OfAny == nil {
		t.Fatal("any (required) expected")
	}
}

func TestToAnthropicSchema_Fallback(t *testing.T) {
	p := toAnthropicSchema(json.RawMessage("bad"))
	if p.Properties == nil {
		t.Fatal("fallback should yield non-nil properties map")
	}
	p = toAnthropicSchema(json.RawMessage(`{"properties":{"x":{}},"required":["x"]}`))
	if p.Required == nil || p.Required[0] != "x" {
		t.Fatalf("required not parsed: %v", p.Required)
	}
}

func TestToAnthropicMessages_ToolResult(t *testing.T) {
	_, conv := toAnthropicMessages([]core.Message{
		core.ToolMessage("result", "t1", "bash"),
	})
	if len(conv) != 1 {
		t.Fatalf("expected 1 message, got %d", len(conv))
	}
	// Tool result becomes a user message; the tool_result block is the first content block.
	if conv[0].Role != "user" {
		t.Fatalf("expected user role for tool result, got %q", conv[0].Role)
	}
}

func TestToAnthropicMessages_ParallelToolResults(t *testing.T) {
	_, conv := toAnthropicMessages([]core.Message{
		core.AssistantMessage("", core.ToolCall{ID: "t1", Name: "read", Input: []byte(`{}`)}, core.ToolCall{ID: "t2", Name: "grep", Input: []byte(`{}`)}),
		core.ToolMessage("file contents", "t1", "read"),
		core.ToolMessage("matches", "t2", "grep"),
	})
	if len(conv) != 2 {
		t.Fatalf("expected assistant + merged user (2), got %d", len(conv))
	}
	if conv[0].Role != "assistant" {
		t.Fatalf("expected assistant first, got %q", conv[0].Role)
	}
	if conv[1].Role != "user" {
		t.Fatalf("expected merged user for tool results, got %q", conv[1].Role)
	}
	if len(conv[1].Content) != 2 {
		t.Fatalf("expected 2 tool_result blocks in one user message, got %d", len(conv[1].Content))
	}
}

func TestToAnthropicMessages_ToolResultsNotMergedAcrossTurns(t *testing.T) {
	_, conv := toAnthropicMessages([]core.Message{
		core.AssistantMessage("", core.ToolCall{ID: "t1", Name: "read", Input: []byte(`{}`)}),
		core.ToolMessage("first", "t1", "read"),
		core.AssistantMessage("", core.ToolCall{ID: "t2", Name: "grep", Input: []byte(`{}`)}),
		core.ToolMessage("second", "t2", "grep"),
	})
	if len(conv) != 4 {
		t.Fatalf("expected 4 messages (2 assistant + 2 user), got %d", len(conv))
	}
	if len(conv[1].Content) != 1 || len(conv[3].Content) != 1 {
		t.Fatalf("each turn's tool results should stay in separate user messages")
	}
}

func TestToAnthropicMessages_ThinkingReplay(t *testing.T) {
	_, conv := toAnthropicMessages([]core.Message{
		core.AssistantMessageWithReasoning("answer", "my thoughts", "SIG", core.ToolCall{ID: "c1", Name: "bash", Input: []byte(`{"cmd":"ls"}`)}),
	})
	if len(conv) != 1 {
		t.Fatalf("expected 1 assistant message, got %d", len(conv))
	}
	if conv[0].Role != "assistant" {
		t.Fatalf("expected assistant role, got %q", conv[0].Role)
	}
}
