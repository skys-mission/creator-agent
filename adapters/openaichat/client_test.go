package openaichat

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/skys-mission/creator-agent/contract"
)

// capturedRequest records what the fake server saw, so assertions run on the test goroutine.
type capturedRequest struct {
	path   string
	auth   string
	body   map[string]any
	broken bool
}

// sseServer runs a fake Chat Completions endpoint. reply writes the raw SSE payload.
func sseServer(t *testing.T, reply func(w http.ResponseWriter, r *http.Request)) (*httptest.Server, *capturedRequest) {
	t.Helper()
	seen := &capturedRequest{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen.path = r.URL.Path
		seen.auth = r.Header.Get("authorization")
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			seen.broken = true
			return
		}
		if err := json.Unmarshal(raw, &seen.body); err != nil {
			seen.broken = true
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		reply(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv, seen
}

func writeSSE(w http.ResponseWriter, flusher http.Flusher, payloads ...string) {
	for _, p := range payloads {
		fmt.Fprintf(w, "data: %s\n\n", p)
		if flusher != nil {
			flusher.Flush()
		}
	}
}

// describe flattens events into comparable strings.
func describe(evs []contract.Event) []string {
	out := make([]string, 0, len(evs))
	for _, ev := range evs {
		switch e := ev.(type) {
		case contract.TextEvent:
			out = append(out, "text:"+e.Delta)
		case contract.ThinkingEvent:
			out = append(out, "think:"+e.Delta)
		case contract.ToolUseStartEvent:
			out = append(out, "toolstart:"+e.ID+":"+e.Name)
		case contract.ToolUseDeltaEvent:
			out = append(out, "tooldelta:"+e.ID+":"+e.DeltaJSON)
		case contract.UsageEvent:
			out = append(out, fmt.Sprintf("usage:%d/%d/%d", e.Usage.InputTokens, e.Usage.OutputTokens, e.Usage.CacheRead))
		case contract.FinishEvent:
			out = append(out, "finish:"+string(e.Reason))
		case contract.ErrorEvent:
			out = append(out, "error:"+e.Err.Error())
		default:
			out = append(out, fmt.Sprintf("other:%T", ev))
		}
	}
	return out
}

// collect drains the event channel with a timeout guard (the adapter contract says the channel
// always closes; the guard turns a contract violation into a test failure instead of a hang).
func collect(t *testing.T, ch <-chan contract.Event, timeout time.Duration) []contract.Event {
	t.Helper()
	var evs []contract.Event
	deadline := time.After(timeout)
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				return evs
			}
			evs = append(evs, ev)
		case <-deadline:
			t.Fatalf("timeout collecting events after %v; got %v", timeout, describe(evs))
			return evs
		}
	}
}

const testChunkPrefix = `{"id":"chatcmpl-1","object":"chat.completion.chunk","created":1,"model":"test-model",`

func TestStreamEndToEnd(t *testing.T) {
	srv, cap := sseServer(t, func(w http.ResponseWriter, r *http.Request) {
		flusher, _ := w.(http.Flusher)
		writeSSE(w, flusher,
			testChunkPrefix+`"choices":[{"index":0,"delta":{"role":"assistant","reasoning_content":"think!"}}]}`,
			testChunkPrefix+`"choices":[{"index":0,"delta":{"content":"Hel"}}]}`,
			testChunkPrefix+`"choices":[{"index":0,"delta":{"content":"lo"}}]}`,
			testChunkPrefix+`"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_a","type":"function","function":{"name":"read","arguments":"{\"p\":\"a\"}"}}]}}]}`,
			testChunkPrefix+`"choices":[],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15,"prompt_tokens_details":{"cached_tokens":3}}}`,
			`[DONE]`,
		)
	})

	c, err := New(Config{ModelID: "test-model", APIKey: "test-key", BaseURL: srv.URL + "/v1"})
	if err != nil {
		t.Fatalf("New() = %v", err)
	}
	ch, err := c.Stream(context.Background(), &contract.ModelRequest{
		Messages: []contract.Message{contract.UserMessage("hi")},
	})
	if err != nil {
		t.Fatalf("Stream() = %v", err)
	}
	got := describe(collect(t, ch, 5*time.Second))

	want := []string{
		"think:think!",
		"text:Hel",
		"text:lo",
		"toolstart:call_a:read",
		`tooldelta:call_a:{"p":"a"}`,
		"usage:10/5/3",
		"finish:stop",
	}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("events = %v, want %v", got, want)
	}

	if cap.broken {
		t.Fatal("server could not decode the request body")
	}
	if cap.path != "/v1/chat/completions" {
		t.Fatalf("path = %q, want /v1/chat/completions (BaseURL must keep its API prefix)", cap.path)
	}
	if cap.auth != "Bearer test-key" {
		t.Fatalf("authorization = %q, want Bearer test-key", cap.auth)
	}
	if cap.body["model"] != "test-model" {
		t.Fatalf("model = %v, want test-model", cap.body["model"])
	}
	if cap.body["stream"] != true {
		t.Fatalf("stream = %v, want true", cap.body["stream"])
	}
	so, _ := cap.body["stream_options"].(map[string]any)
	if so == nil || so["include_usage"] != true {
		t.Fatalf("stream_options = %v, want include_usage true", cap.body["stream_options"])
	}
	msgs, _ := cap.body["messages"].([]any)
	if len(msgs) != 1 {
		t.Fatalf("messages = %v, want 1", cap.body["messages"])
	}
}

func TestStreamToggleDialectWire(t *testing.T) {
	// kind=toggle must land on the wire in the exact shape of the pinned dialect — and must not
	// smuggle in reasoning_effort (that is the effort kind's field). Every surveyed wire shape is
	// pinned here, plus the custom dialect's full freedom: dotted-path nesting, JSON-literal
	// values (bool/number/string), and omit-when-off.
	cases := []struct {
		name       string
		dialect    contract.ReasoningToggleDialect
		field      string
		onVal      string
		offVal     string
		def        string
		wantKey    string
		wantVal    any
		wantAbsent bool
	}{
		{name: "dashscope on", dialect: contract.ToggleDialectEnableThinking, def: contract.ReasoningToggleOn, wantKey: "enable_thinking", wantVal: true},
		{name: "dashscope off", dialect: contract.ToggleDialectEnableThinking, def: contract.ReasoningToggleOff, wantKey: "enable_thinking", wantVal: false},
		{name: "ollama on", dialect: contract.ToggleDialectThink, def: contract.ReasoningToggleOn, wantKey: "think", wantVal: true},
		{name: "ollama off", dialect: contract.ToggleDialectThink, def: contract.ReasoningToggleOff, wantKey: "think", wantVal: false},
		{name: "glm on", dialect: contract.ToggleDialectThinkingType, def: contract.ReasoningToggleOn, wantKey: "thinking", wantVal: map[string]any{"type": "enabled"}},
		{name: "glm off", dialect: contract.ToggleDialectThinkingType, def: contract.ReasoningToggleOff, wantKey: "thinking", wantVal: map[string]any{"type": "disabled"}},
		{name: "vllm qwen3 on", dialect: contract.ToggleDialectChatTemplateEnableThinking, def: contract.ReasoningToggleOn, wantKey: "chat_template_kwargs", wantVal: map[string]any{"enable_thinking": true}},
		{name: "vllm qwen3 off", dialect: contract.ToggleDialectChatTemplateEnableThinking, def: contract.ReasoningToggleOff, wantKey: "chat_template_kwargs", wantVal: map[string]any{"enable_thinking": false}},
		{name: "vllm granite on", dialect: contract.ToggleDialectChatTemplateThinking, def: contract.ReasoningToggleOn, wantKey: "chat_template_kwargs", wantVal: map[string]any{"thinking": true}},
		{name: "vllm granite off", dialect: contract.ToggleDialectChatTemplateThinking, def: contract.ReasoningToggleOff, wantKey: "chat_template_kwargs", wantVal: map[string]any{"thinking": false}},
		{name: "openrouter on", dialect: contract.ToggleDialectReasoningEnabled, def: contract.ReasoningToggleOn, wantKey: "reasoning", wantVal: map[string]any{"enabled": true}},
		{name: "openrouter off", dialect: contract.ToggleDialectReasoningEnabled, def: contract.ReasoningToggleOff, wantKey: "reasoning", wantVal: map[string]any{"enabled": false}},
		{name: "custom bool on", dialect: contract.ToggleDialectCustom, field: "my_flag", onVal: "true", offVal: "false", def: contract.ReasoningToggleOn, wantKey: "my_flag", wantVal: true},
		{name: "custom bool off", dialect: contract.ToggleDialectCustom, field: "my_flag", onVal: "true", offVal: "false", def: contract.ReasoningToggleOff, wantKey: "my_flag", wantVal: false},
		{name: "custom nested bool", dialect: contract.ToggleDialectCustom, field: "a.b", onVal: "true", offVal: "false", def: contract.ReasoningToggleOn, wantKey: "a", wantVal: map[string]any{"b": true}},
		{name: "custom string value", dialect: contract.ToggleDialectCustom, field: "mode", onVal: "enabled", offVal: "disabled", def: contract.ReasoningToggleOn, wantKey: "mode", wantVal: "enabled"},
		{name: "custom nested string value", dialect: contract.ToggleDialectCustom, field: "thinking.type", onVal: "adaptive", offVal: "disabled", def: contract.ReasoningToggleOn, wantKey: "thinking", wantVal: map[string]any{"type": "adaptive"}},
		{name: "custom number value", dialect: contract.ToggleDialectCustom, field: "level", onVal: "1", offVal: "0", def: contract.ReasoningToggleOn, wantKey: "level", wantVal: float64(1)},
		{name: "custom quoted string value", dialect: contract.ToggleDialectCustom, field: "flag", onVal: `"true"`, offVal: "false", def: contract.ReasoningToggleOn, wantKey: "flag", wantVal: "true"},
		{name: "custom off omits field", dialect: contract.ToggleDialectCustom, field: "my_flag", onVal: "1", def: contract.ReasoningToggleOff, wantKey: "my_flag", wantAbsent: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv, cap := sseServer(t, func(w http.ResponseWriter, r *http.Request) {
				flusher, _ := w.(http.Flusher)
				writeSSE(w, flusher,
					testChunkPrefix+`"choices":[{"index":0,"delta":{"content":"ok"}}]}`,
					`[DONE]`,
				)
			})
			c, err := New(Config{
				ModelID: "test-model", APIKey: "k", BaseURL: srv.URL + "/v1",
				Reasoning: contract.Reasoning{
					Kind: contract.ReasoningKindToggle, ToggleDialect: tc.dialect, ToggleField: tc.field,
					ToggleOnValue: tc.onVal, ToggleOffValue: tc.offVal, Default: tc.def,
				},
			})
			if err != nil {
				t.Fatalf("New() = %v", err)
			}
			ch, err := c.Stream(context.Background(), &contract.ModelRequest{
				Messages: []contract.Message{contract.UserMessage("hi")},
			})
			if err != nil {
				t.Fatalf("Stream() = %v", err)
			}
			collect(t, ch, 5*time.Second)
			if cap.broken {
				t.Fatal("server could not decode the request body")
			}
			got, present := cap.body[tc.wantKey]
			if tc.wantAbsent {
				if present {
					t.Fatalf("wire %s = %#v, want the field omitted", tc.wantKey, got)
				}
			} else if !reflect.DeepEqual(got, tc.wantVal) {
				t.Fatalf("wire %s = %#v, want %#v", tc.wantKey, got, tc.wantVal)
			}
			if _, ok := cap.body["reasoning_effort"]; ok {
				t.Fatalf("toggle kind must not send reasoning_effort: %v", cap.body["reasoning_effort"])
			}
		})
	}
}

func TestStreamHTTPErrorIsImmediate(t *testing.T) {
	srv, _ := sseServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"error":{"message":"bad key"}}`)
	})
	c, err := New(Config{ModelID: "test-model", APIKey: "bad", BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("New() = %v", err)
	}
	ch, err := c.Stream(context.Background(), &contract.ModelRequest{
		Messages: []contract.Message{contract.UserMessage("hi")},
	})
	if err == nil {
		if ch != nil {
			t.Fatal("Stream() returned a channel alongside a nil error")
		}
		t.Fatal("Stream() = nil error, want HTTP status error before any output")
	}
}

func TestStreamMidStreamError(t *testing.T) {
	srv, _ := sseServer(t, func(w http.ResponseWriter, r *http.Request) {
		flusher, _ := w.(http.Flusher)
		writeSSE(w, flusher,
			testChunkPrefix+`"choices":[{"index":0,"delta":{"content":"partial"}}]}`,
			`{"error":{"message":"boom","type":"server_error"}}`,
		)
	})
	c, err := New(Config{ModelID: "test-model", BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("New() = %v", err)
	}
	ch, err := c.Stream(context.Background(), &contract.ModelRequest{
		Messages: []contract.Message{contract.UserMessage("hi")},
	})
	if err != nil {
		t.Fatalf("Stream() = %v", err)
	}
	got := describe(collect(t, ch, 5*time.Second))
	if len(got) != 3 || got[0] != "text:partial" || !strings.HasPrefix(got[1], "error:") ||
		!strings.Contains(got[1], "boom") || got[2] != "finish:error" {
		t.Fatalf("events = %v, want [text:partial error:*boom* finish:error]", got)
	}
}

func TestStreamCancel(t *testing.T) {
	srv, _ := sseServer(t, func(w http.ResponseWriter, r *http.Request) {
		flusher, _ := w.(http.Flusher)
		writeSSE(w, flusher, testChunkPrefix+`"choices":[{"index":0,"delta":{"content":"first"}}]}`)
		// Hold the stream open until the client goes away.
		<-r.Context().Done()
	})
	c, err := New(Config{ModelID: "test-model", BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("New() = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch, err := c.Stream(ctx, &contract.ModelRequest{
		Messages: []contract.Message{contract.UserMessage("hi")},
	})
	if err != nil {
		t.Fatalf("Stream() = %v", err)
	}

	var evs []contract.Event
	deadline := time.After(5 * time.Second)
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				t.Fatalf("channel closed before cancellation; events = %v", describe(evs))
			}
			evs = append(evs, ev)
			if _, seen := ev.(contract.TextEvent); seen {
				cancel()
			}
			if _, seen := ev.(contract.FinishEvent); seen {
				got := describe(evs)
				if got[len(got)-1] != "finish:canceled" {
					t.Fatalf("events = %v, want terminal finish:canceled", got)
				}
				return
			}
		case <-deadline:
			t.Fatalf("timeout waiting for cancel termination; events = %v", describe(evs))
		}
	}
}

func TestNewRequiresModelID(t *testing.T) {
	if _, err := New(Config{}); err == nil {
		t.Fatal("New() without ModelID = nil, want error")
	}
	c, err := New(Config{ModelID: "m1"})
	if err != nil {
		t.Fatalf("New() = %v", err)
	}
	if c.Name() != "m1" {
		t.Fatalf("Name() = %q, want m1", c.Name())
	}
	c2, err := New(Config{ModelID: "m1", Name: "friendly"})
	if err != nil {
		t.Fatalf("New() = %v", err)
	}
	if c2.Name() != "friendly" {
		t.Fatalf("Name() = %q, want friendly", c2.Name())
	}
}

func TestStreamThinkingIndependentOfEcho(t *testing.T) {
	// Disabling the history echo must not mute inbound reasoning: showing thinking is the
	// UI's decision, so the wire always delivers it.
	srv, _ := sseServer(t, func(w http.ResponseWriter, r *http.Request) {
		flusher, _ := w.(http.Flusher)
		writeSSE(w, flusher,
			testChunkPrefix+`"choices":[{"index":0,"delta":{"reasoning_content":"visible thought"}}]}`,
			testChunkPrefix+`"choices":[{"index":0,"delta":{"content":"answer"}}]}`,
			`[DONE]`,
		)
	})
	c, err := New(Config{ModelID: "test-model", BaseURL: srv.URL, DisableThinkingEcho: true})
	if err != nil {
		t.Fatalf("New() = %v", err)
	}
	ch, err := c.Stream(context.Background(), &contract.ModelRequest{
		Messages: []contract.Message{contract.UserMessage("hi")},
	})
	if err != nil {
		t.Fatalf("Stream() = %v", err)
	}
	got := describe(collect(t, ch, 5*time.Second))
	want := []string{"think:visible thought", "text:answer", "finish:stop"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("events = %v, want %v", got, want)
	}
}

func TestStreamEchoControlsHistory(t *testing.T) {
	// The echo switch decides whether assistant thinking rides outbound history.
	history := &contract.ModelRequest{
		Messages: []contract.Message{
			contract.UserMessage("hi"),
			contract.AssistantMessageWithReasoning("answer", "the thought", ""),
		},
	}
	echoOnWire := func(t *testing.T, disable bool) bool {
		t.Helper()
		srv, seen := sseServer(t, func(w http.ResponseWriter, r *http.Request) {
			writeSSE(w, nil, `[DONE]`)
		})
		c, err := New(Config{ModelID: "test-model", BaseURL: srv.URL, DisableThinkingEcho: disable})
		if err != nil {
			t.Fatalf("New() = %v", err)
		}
		ch, err := c.Stream(context.Background(), history)
		if err != nil {
			t.Fatalf("Stream() = %v", err)
		}
		collect(t, ch, 5*time.Second)
		if seen.broken {
			t.Fatal("server could not decode the request body")
		}
		msgs, _ := seen.body["messages"].([]any)
		for _, m := range msgs {
			if msg, ok := m.(map[string]any); ok {
				if val, ok := msg["reasoning_content"]; ok && val == "the thought" {
					return true
				}
			}
		}
		return false
	}
	if !echoOnWire(t, false) {
		t.Fatal("default config must round-trip thinking in history")
	}
	if echoOnWire(t, true) {
		t.Fatal("DisableThinkingEcho must keep thinking out of history")
	}
}
func TestStreamReasoningKeyPin(t *testing.T) {
	srv, _ := sseServer(t, func(w http.ResponseWriter, r *http.Request) {
		flusher, _ := w.(http.Flusher)
		writeSSE(w, flusher,
			testChunkPrefix+`"choices":[{"index":0,"delta":{"my_think":"pinned thought"}}]}`,
			testChunkPrefix+`"choices":[{"index":0,"delta":{"reasoning_content":"de-facto thought"}}]}`,
			testChunkPrefix+`"choices":[{"index":0,"delta":{"content":"answer"}}]}`,
			`[DONE]`,
		)
	})
	c, err := New(Config{ModelID: "test-model", BaseURL: srv.URL, ReasoningKey: " my_think "})
	if err != nil {
		t.Fatalf("New() = %v", err)
	}
	ch, err := c.Stream(context.Background(), &contract.ModelRequest{
		Messages: []contract.Message{contract.UserMessage("hi")},
	})
	if err != nil {
		t.Fatalf("Stream() = %v", err)
	}
	got := describe(collect(t, ch, 5*time.Second))
	want := []string{"think:pinned thought", "text:answer", "finish:stop"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("events = %v, want %v (pinned key must be the only consulted key)", got, want)
	}
}

// assistantHistory is a request whose assistant turn carries thinking, used to observe which
// wire key the echo lands under.
var assistantHistory = &contract.ModelRequest{
	Messages: []contract.Message{
		contract.UserMessage("hi"),
		contract.AssistantMessageWithReasoning("answer", "the thought", ""),
	},
}

// echoedKeys returns the extra field names (besides the standard ones) of the outbound
// assistant message.
func echoedKeys(t *testing.T, body map[string]any) []string {
	t.Helper()
	if body == nil {
		t.Fatal("no request body captured")
	}
	msgs, _ := body["messages"].([]any)
	for _, m := range msgs {
		msg, ok := m.(map[string]any)
		if !ok || msg["role"] != "assistant" {
			continue
		}
		var keys []string
		for k := range msg {
			switch k {
			case "role", "content", "tool_calls", "name", "refusal":
			default:
				keys = append(keys, k)
			}
		}
		sort.Strings(keys)
		return keys
	}
	t.Fatal("no assistant message in the outbound request")
	return nil
}

func TestStreamLearnsReasoningDialect(t *testing.T) {
	// The endpoint speaks `reasoning`, not the de-facto `reasoning_content`. After observing
	// the dialect, outbound history must echo thinking under the same key ("reply in the
	// dialect the peer spoke") — and a later response without reasoning must not clear it.
	var calls atomic.Int32
	srv, seen := sseServer(t, func(w http.ResponseWriter, r *http.Request) {
		flusher, _ := w.(http.Flusher)
		if calls.Add(1) == 1 {
			writeSSE(w, flusher,
				testChunkPrefix+`"choices":[{"index":0,"delta":{"reasoning":"thought-a"}}]}`,
				testChunkPrefix+`"choices":[{"index":0,"delta":{"content":"answer"}}]}`,
				`[DONE]`,
			)
			return
		}
		writeSSE(w, flusher,
			testChunkPrefix+`"choices":[{"index":0,"delta":{"content":"answer"}}]}`,
			`[DONE]`,
		)
	})
	c, err := New(Config{ModelID: "test-model", BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("New() = %v", err)
	}

	run := func(req *contract.ModelRequest) {
		t.Helper()
		ch, err := c.Stream(context.Background(), req)
		if err != nil {
			t.Fatalf("Stream() = %v", err)
		}
		collect(t, ch, 5*time.Second)
	}

	// Call 1: observe the dialect.
	run(&contract.ModelRequest{Messages: []contract.Message{contract.UserMessage("hi")}})
	// Call 2: the echo must ride the learned key, never the de-facto default.
	run(assistantHistory)
	if seen.broken {
		t.Fatal("server could not decode the request body")
	}
	if got := echoedKeys(t, seen.body); len(got) != 1 || got[0] != "reasoning" {
		t.Fatalf("echoed keys = %v, want [reasoning]", got)
	}

	// Detection never clears: call 2 answered without reasoning, so call 3 must still echo
	// under the learned key.
	run(assistantHistory)
	if got := echoedKeys(t, seen.body); len(got) != 1 || got[0] != "reasoning" {
		t.Fatalf("echoed keys after a silent response = %v, want [reasoning]", got)
	}
}

func TestStreamPinnedKeyDisablesLearning(t *testing.T) {
	// A pinned key consults nothing else inbound and always wins outbound, even though the
	// endpoint speaks a different dialect.
	srv, seen := sseServer(t, func(w http.ResponseWriter, r *http.Request) {
		flusher, _ := w.(http.Flusher)
		writeSSE(w, flusher,
			testChunkPrefix+`"choices":[{"index":0,"delta":{"reasoning_content":"not mine"}}]}`,
			`[DONE]`,
		)
	})
	c, err := New(Config{ModelID: "test-model", BaseURL: srv.URL, ReasoningKey: "my_think"})
	if err != nil {
		t.Fatalf("New() = %v", err)
	}
	ch, err := c.Stream(context.Background(), assistantHistory)
	if err != nil {
		t.Fatalf("Stream() = %v", err)
	}
	got := describe(collect(t, ch, 5*time.Second))
	want := []string{"finish:stop"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("events = %v, want %v (pinned key must not consult reasoning_content)", got, want)
	}
	if keys := echoedKeys(t, seen.body); len(keys) != 1 || keys[0] != "my_think" {
		t.Fatalf("echoed keys = %v, want [my_think]", keys)
	}
}
