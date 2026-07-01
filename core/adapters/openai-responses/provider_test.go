package openairesponses

import (
	"encoding/json"
	"testing"

	"github.com/openai/openai-go/responses"

	core "github.com/skys-mission/creator-agent/core"
)

func TestConvertEvent_TextDelta(t *testing.T) {
	ev := responses.ResponseStreamEventUnion{
		Type:  "response.output_text.delta",
		Delta: responses.ResponseStreamEventUnionDelta{OfString: "hello"},
	}
	got := convertEvent(ev, map[string]callInfo{})
	if len(got) != 1 {
		t.Fatalf("expected 1 event, got %d", len(got))
	}
	td, ok := got[0].(core.MTextDelta)
	if !ok || td.Delta != "hello" {
		t.Fatalf("expected MTextDelta{hello}, got %#v", got[0])
	}
}

func TestConvertEvent_FunctionCall(t *testing.T) {
	calls := map[string]callInfo{}
	// output_item.added carries the call id + name; arguments.delta carries only item_id.
	added := responses.ResponseStreamEventUnion{
		Type: "response.output_item.added",
		Item: responses.ResponseOutputItemUnion{
			Type:   "function_call",
			ID:     "item_1",
			CallID: "call_abc",
			Name:   "bash",
		},
	}
	convertEvent(added, calls)
	if ci, ok := calls["item_1"]; !ok || ci.callID != "call_abc" || ci.name != "bash" {
		t.Fatalf("call info not recorded: %+v", calls)
	}

	delta := responses.ResponseStreamEventUnion{
		Type:   "response.function_call_arguments.delta",
		ItemID: "item_1",
		Delta:  responses.ResponseStreamEventUnionDelta{OfString: `{"cmd":"ls"}`},
	}
	got := convertEvent(delta, calls)
	if len(got) != 1 {
		t.Fatalf("expected 1 event, got %d", len(got))
	}
	tud, ok := got[0].(core.MToolUseDelta)
	if !ok || tud.ID != "call_abc" || tud.Name != "bash" || tud.DeltaJSON != `{"cmd":"ls"}` {
		t.Fatalf("expected MToolUseDelta{call_abc,bash}, got %#v", got[0])
	}
}

func TestConvertEvent_Reasoning(t *testing.T) {
	ev := responses.ResponseStreamEventUnion{
		Type:  "response.reasoning_summary_text.delta",
		Delta: responses.ResponseStreamEventUnionDelta{OfString: "thinking..."},
	}
	got := convertEvent(ev, map[string]callInfo{})
	if len(got) != 1 {
		t.Fatalf("expected 1 event, got %d", len(got))
	}
	td, ok := got[0].(core.MThinkingDelta)
	if !ok || td.Delta != "thinking..." {
		t.Fatalf("expected MThinkingDelta, got %#v", got[0])
	}
}

func TestConvertEvent_Completed(t *testing.T) {
	ev := responses.ResponseStreamEventUnion{
		Type: "response.completed",
		Response: responses.Response{
			Usage: responses.ResponseUsage{InputTokens: 12, OutputTokens: 7},
		},
	}
	got := convertEvent(ev, map[string]callInfo{})
	if len(got) != 2 {
		t.Fatalf("expected usage+finish (2 events), got %d", len(got))
	}
	u, ok := got[0].(core.MUsage)
	if !ok || u.Usage.InputTokens != 12 || u.Usage.OutputTokens != 7 {
		t.Fatalf("expected MUsage{12,7}, got %#v", got[0])
	}
	if _, ok := got[1].(core.MFinish); !ok {
		t.Fatalf("expected MFinish second, got %#v", got[1])
	}
}

func TestToSchemaMap_Fallback(t *testing.T) {
	m := toSchemaMap(json.RawMessage("not json"))
	if m["type"] != "object" {
		t.Fatalf("expected fallback object schema, got %v", m)
	}
	m = toSchemaMap(json.RawMessage(`{"type":"object","properties":{"x":{"type":"string"}}}`))
	if m["type"] != "object" {
		t.Fatalf("expected parsed schema, got %v", m)
	}
}

func TestToResponsesToolChoice(t *testing.T) {
	if (toResponsesToolChoice(core.ToolAuto) != responses.ResponseNewParamsToolChoiceUnion{}) {
		t.Fatal("auto should yield zero value (unset)")
	}
	none := toResponsesToolChoice(core.ToolNone)
	if none.OfToolChoiceMode.Value != responses.ToolChoiceOptionsNone {
		t.Fatalf("none expected, got %v", none.OfToolChoiceMode.Value)
	}
	req := toResponsesToolChoice(core.ToolRequired)
	if req.OfToolChoiceMode.Value != responses.ToolChoiceOptionsRequired {
		t.Fatalf("required expected, got %v", req.OfToolChoiceMode.Value)
	}
}

func TestToResponsesInput_ToolResult(t *testing.T) {
	_, items := toResponsesInput([]core.Message{
		core.ToolMessage("result", "call_1", "bash"),
	})
	if len(items) != 1 || items[0].OfFunctionCallOutput == nil {
		t.Fatalf("expected one function_call_output item, got %+v", items)
	}
	if items[0].OfFunctionCallOutput.CallID != "call_1" || items[0].OfFunctionCallOutput.Output != "result" {
		t.Fatalf("bad tool result mapping: %+v", items[0].OfFunctionCallOutput)
	}
}
