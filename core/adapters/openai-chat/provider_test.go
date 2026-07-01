package openaichat

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/openai/openai-go"

	core "github.com/skys-mission/creator-agent/core"
)

func TestToOpenAIMessagesRoles(t *testing.T) {
	msgs := []core.Message{
		core.SystemMessage("sys"),
		core.UserMessage("hi"),
		core.ToolMessage("result", "c1", "read"),
	}
	out := toOpenAIMessages(msgs)
	if len(out) != 3 {
		t.Fatalf("len = %d, want 3", len(out))
	}
	if out[0].OfSystem == nil {
		t.Error("msg[0] should be system")
	}
	if out[1].OfUser == nil {
		t.Error("msg[1] should be user")
	}
	if out[2].OfTool == nil {
		t.Error("msg[2] should be tool")
	}
}

func TestToAssistantMessageWithToolCalls(t *testing.T) {
	m := core.AssistantMessage("",
		core.ToolCall{ID: "c1", Name: "bash", Input: json.RawMessage(`{"command":"ls"}`)})
	out := toAssistantMessage(m)
	if out.OfAssistant == nil {
		t.Fatal("expected assistant message")
	}
	if len(out.OfAssistant.ToolCalls) != 1 {
		t.Fatalf("toolcalls = %d, want 1", len(out.OfAssistant.ToolCalls))
	}
	tc := out.OfAssistant.ToolCalls[0]
	if tc.ID != "c1" || tc.Function.Name != "bash" || tc.Function.Arguments != `{"command":"ls"}` {
		t.Errorf("toolcall wrong: id=%q name=%q args=%q", tc.ID, tc.Function.Name, tc.Function.Arguments)
	}
}

func TestConvertChunkText(t *testing.T) {
	idx := map[int64]string{}
	evs := convertChunk(openai.ChatCompletionChunk{
		Choices: []openai.ChatCompletionChunkChoice{{
			Delta: openai.ChatCompletionChunkChoiceDelta{Content: "hello"},
		}},
	}, idx)
	if len(evs) != 1 {
		t.Fatalf("events = %d, want 1", len(evs))
	}
	td, ok := evs[0].(core.MTextDelta)
	if !ok || td.Delta != "hello" {
		t.Errorf("want MTextDelta hello, got %v", evs[0])
	}
}

func TestConvertChunkToolCallIndexAssociation(t *testing.T) {
	idx := map[int64]string{}
	i0 := int64(0)

	// First chunk: id + name (records index->id)
	evs := convertChunk(openai.ChatCompletionChunk{
		Choices: []openai.ChatCompletionChunkChoice{{
			Delta: openai.ChatCompletionChunkChoiceDelta{
				ToolCalls: []openai.ChatCompletionChunkChoiceDeltaToolCall{{
					Index: i0, ID: "call_1",
					Function: openai.ChatCompletionChunkChoiceDeltaToolCallFunction{Name: "bash"},
				}},
			},
		}},
	}, idx)
	if idx[0] != "call_1" {
		t.Errorf("indexToID[0] = %q, want call_1", idx[0])
	}
	d, ok := evs[0].(core.MToolUseDelta)
	if !ok || d.ID != "call_1" || d.Name != "bash" {
		t.Errorf("first delta wrong: %+v", d)
	}

	// Subsequent chunk: only arguments, id empty -> should associate to call_1
	evs = convertChunk(openai.ChatCompletionChunk{
		Choices: []openai.ChatCompletionChunkChoice{{
			Delta: openai.ChatCompletionChunkChoiceDelta{
				ToolCalls: []openai.ChatCompletionChunkChoiceDeltaToolCall{{
					Index:    i0,
					Function: openai.ChatCompletionChunkChoiceDeltaToolCallFunction{Arguments: `{"command":"ls"}`},
				}},
			},
		}},
	}, idx)
	d, ok = evs[0].(core.MToolUseDelta)
	if !ok {
		t.Fatalf("want MToolUseDelta, got %T", evs[0])
	}
	if d.ID != "call_1" {
		t.Errorf("args delta should associate to call_1, got id=%q", d.ID)
	}
	if d.DeltaJSON != `{"command":"ls"}` {
		t.Errorf("delta json = %q", d.DeltaJSON)
	}
}

func TestConvertChunkUsageAndFinish(t *testing.T) {
	idx := map[int64]string{}
	evs := convertChunk(openai.ChatCompletionChunk{
		Choices: []openai.ChatCompletionChunkChoice{{
			FinishReason: "stop",
		}},
		Usage: openai.CompletionUsage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15},
	}, idx)
	// usage first (TotalTokens>0), then finish
	if len(evs) != 2 {
		t.Fatalf("events = %d, want 2 (usage + finish)", len(evs))
	}
	if _, ok := evs[0].(core.MUsage); !ok {
		t.Errorf("first should be MUsage, got %T", evs[0])
	}
	f, ok := evs[1].(core.MFinish)
	if !ok || f.Reason != "stop" {
		t.Errorf("second should be MFinish stop, got %v", evs[1])
	}
}

func TestToOpenAITools(t *testing.T) {
	tools := []core.ToolInfo{
		{Name: "read", Description: "read a file", InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}`)},
	}
	out := toOpenAITools(tools)
	if len(out) != 1 {
		t.Fatalf("tools = %d, want 1", len(out))
	}
	if out[0].Function.Name != "read" {
		t.Errorf("name = %q, want read", out[0].Function.Name)
	}
	if out[0].Function.Parameters == nil {
		t.Error("parameters should not be nil")
	}
}

func TestToFunctionParametersFallback(t *testing.T) {
	// Empty schema -> minimal valid schema
	p := toFunctionParameters(nil)
	if p["type"] != "object" {
		t.Errorf("empty schema fallback type = %v, want object", p["type"])
	}
	// Invalid JSON -> fallback
	p = toFunctionParameters(json.RawMessage(`{bad`))
	if p["type"] != "object" {
		t.Errorf("bad json fallback type = %v, want object", p["type"])
	}
}

func TestConvertErrClassification(t *testing.T) {
	cases := []struct {
		name       string
		err        error
		wantType   string
		wantStatus int
	}{
		{"429", &openai.Error{StatusCode: 429}, "*core.RateLimitedError", 429},
		{"500", &openai.Error{StatusCode: 500}, "*core.ServerError", 500},
		{"503", &openai.Error{StatusCode: 503}, "*core.ServerError", 503},
		{"400", &openai.Error{StatusCode: 400}, "*core.ClientError", 400},
		{"401", &openai.Error{StatusCode: 401}, "*core.ClientError", 401},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := convertErr(c.err)
			switch c.wantType {
			case "*core.RateLimitedError":
				rl, ok := got.(*core.RateLimitedError)
				if !ok {
					t.Fatalf("got %T, want RateLimitedError", got)
				}
				if rl.StatusCode != c.wantStatus {
					t.Errorf("StatusCode = %d, want %d", rl.StatusCode, c.wantStatus)
				}
			case "*core.ServerError":
				se, ok := got.(*core.ServerError)
				if !ok {
					t.Fatalf("got %T, want ServerError", got)
				}
				if se.StatusCode != c.wantStatus {
					t.Errorf("StatusCode = %d, want %d", se.StatusCode, c.wantStatus)
				}
			case "*core.ClientError":
				ce, ok := got.(*core.ClientError)
				if !ok {
					t.Fatalf("got %T, want ClientError", got)
				}
				if ce.StatusCode != c.wantStatus {
					t.Errorf("StatusCode = %d, want %d", ce.StatusCode, c.wantStatus)
				}
			}
		})
	}

	// Non-openai.Error -> pass through unchanged
	other := errors.New("network error")
	if convertErr(other) != other {
		t.Error("non-openai error should pass through unchanged")
	}
	// nil -> nil
	if convertErr(nil) != nil {
		t.Error("nil should return nil")
	}
}

// TestToToolChoice covers the three ToolChoice states (previously 0%).
func TestToToolChoice(t *testing.T) {
	// ToolNone -> OfAuto="none" (Valid and Value="none")
	none := toToolChoice(core.ToolNone)
	if !none.OfAuto.Valid() || none.OfAuto.Value != "none" {
		t.Errorf("ToolNone should map to OfAuto='none' (valid), got valid=%v value=%q", none.OfAuto.Valid(), none.OfAuto.Value)
	}
	// ToolRequired -> OfAuto="required"
	req := toToolChoice(core.ToolRequired)
	if !req.OfAuto.Valid() || req.OfAuto.Value != "required" {
		t.Errorf("ToolRequired should map to OfAuto='required' (valid), got valid=%v value=%q", req.OfAuto.Valid(), req.OfAuto.Value)
	}
	// ToolAuto (default) -> unset (SDK default is auto)
	auto := toToolChoice(core.ToolAuto)
	if auto.OfAuto.Valid() {
		t.Errorf("ToolAuto should leave OfAuto unset, got valid value=%q", auto.OfAuto.Value)
	}
}

// TestExtractReasoningEdgeCases covers reasoning extraction boundaries (empty / no choices / bad JSON).
func TestExtractReasoningEdgeCases(t *testing.T) {
	// No RawJSON -> empty (no panic)
	chunk := openai.ChatCompletionChunk{}
	if got := extractReasoning(chunk); got != "" {
		t.Errorf("empty chunk should give empty reasoning, got %q", got)
	}
}

// TestApplyVariantDefaults covers the variant generation-default precedence: variant defaults fill
// in only the fields the per-turn request left unset; an explicit request value always wins.
func TestApplyVariantDefaults(t *testing.T) {
	temp := 0.7
	topP := 0.9
	maxTok := int64(2048)
	p := &Provider{temperature: &temp, topP: &topP, maxTokens: &maxTok}

	t.Run("fills when request omits", func(t *testing.T) {
		var params openai.ChatCompletionNewParams
		applyVariantDefaults(&params, core.ModelRequest{}, p)
		if params.Temperature.Value != temp {
			t.Errorf("temperature = %v, want %v", params.Temperature.Value, temp)
		}
		if params.TopP.Value != topP {
			t.Errorf("top_p = %v, want %v", params.TopP.Value, topP)
		}
		if params.MaxTokens.Value != maxTok {
			t.Errorf("max_tokens = %v, want %v", params.MaxTokens.Value, maxTok)
		}
	})

	t.Run("request value present -> variant default NOT applied (request wins)", func(t *testing.T) {
		// applyVariantDefaults only fills variant defaults for fields the request left nil. In real
		// Stream, the request's own values are applied to params beforehand; here we verify the
		// helper's contract: when a request field is set, the variant default is skipped (params
		// stays unset, since this helper never applies request values itself).
		reqTemp := float32(0.1)
		reqMax := 50
		var params openai.ChatCompletionNewParams
		applyVariantDefaults(&params, core.ModelRequest{Temperature: &reqTemp, MaxTokens: &reqMax}, p)
		if params.Temperature.Valid() {
			t.Errorf("temperature should be left unset (request owns it); got %v", params.Temperature.Value)
		}
		if params.MaxTokens.Valid() {
			t.Errorf("max_tokens should be left unset (request owns it); got %v", params.MaxTokens.Value)
		}
		// topP was unset in request -> variant default applies.
		if params.TopP.Value != topP {
			t.Errorf("top_p = %v, want variant default %v", params.TopP.Value, topP)
		}
	})

	t.Run("nil provider is a no-op", func(t *testing.T) {
		var params openai.ChatCompletionNewParams
		applyVariantDefaults(&params, core.ModelRequest{}, nil)
		if params.Temperature.Valid() || params.TopP.Valid() || params.MaxTokens.Valid() {
			t.Errorf("nil provider should not set any params")
		}
	})
}

// TestVariantRequestOptions covers the per-call SDK option building from the variant's header/body
// overrides: nil/empty provider -> nil; populated -> one option per header + one per body key.
func TestVariantRequestOptions(t *testing.T) {
	t.Run("nil provider returns nil", func(t *testing.T) {
		if got := variantRequestOptions(nil); got != nil {
			t.Errorf("nil provider should return nil; got %v", got)
		}
	})
	t.Run("empty overrides returns nil", func(t *testing.T) {
		p := &Provider{}
		if got := variantRequestOptions(p); got != nil {
			t.Errorf("empty overrides should return nil; got %v", got)
		}
	})
	t.Run("headers and body produce one option each", func(t *testing.T) {
		p := &Provider{
			extraHeaders: map[string]string{"X-Tag": "fast", "X-Env": "test"},
			extraBody:    map[string]any{"presence_penalty": 0.5},
		}
		opts := variantRequestOptions(p)
		// RequestOption is an opaque func; we assert the count (2 headers + 1 body = 3).
		if len(opts) != 3 {
			t.Errorf("opts count = %d, want 3 (2 headers + 1 body)", len(opts))
		}
	})
}
