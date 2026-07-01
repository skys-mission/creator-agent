package openaichat

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	core "github.com/skys-mission/creator-agent/core"
	"github.com/skys-mission/creator-agent/core/adapters/shared"
)

// newTestProvider creates a provider pointing to a mock server. timeout<=0 uses the default.
func newTestProvider(t *testing.T, baseURL string, timeout time.Duration) *Provider {
	t.Helper()
	p, err := NewProvider(context.Background(), shared.ProviderConfig{
		BaseURL: baseURL, APIKey: "test", Model: "gpt-4o-mini", RequestTimeout: timeout,
	})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// sseServer returns an SSE server with the given data lines (200 + text/event-stream).
func sseServer(t *testing.T, bodies ...string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		f, _ := w.(http.Flusher)
		for _, b := range bodies {
			fmt.Fprintf(w, "data: %s\n\n", b)
			if f != nil {
				f.Flush()
			}
		}
		fmt.Fprintf(w, "data: [DONE]\n\n")
		if f != nil {
			f.Flush()
		}
	}))
}

// errServer returns the specified status code + JSON error body.
func errServer(t *testing.T, status int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		fmt.Fprintf(w, `{"error":{"message":"test error","type":"test"}}`)
	}))
}

func collectEvents(t *testing.T, p *Provider, ctx context.Context) []core.ModelEvent {
	t.Helper()
	ch, err := p.Stream(ctx, core.ModelRequest{Messages: []core.Message{core.UserMessage("hi")}})
	if err != nil {
		t.Fatal(err)
	}
	var evs []core.ModelEvent
	for ev := range ch {
		evs = append(evs, ev)
	}
	return evs
}

// chunkJSON builds a minimal valid ChatCompletionChunk JSON.
func chunkJSON(delta, finish string, usage string) string {
	u := ""
	if usage != "" {
		u = `,"usage":` + usage
	}
	return fmt.Sprintf(`{"id":"t","object":"chat.completion.chunk","created":1700000000,"model":"gpt-4o-mini","choices":[{"index":0,"delta":%s,"finish_reason":"%s"}]%s}`,
		delta, finish, u)
}

// Verify: normal streaming (text delta + usage + finish).
func TestStreamTextUsageFinish(t *testing.T) {
	srv := sseServer(t,
		chunkJSON(`{"content":"hello"}`, "", ""),
		chunkJSON(`{"content":" world"}`, "", ""),
		chunkJSON(`{}`, "stop", `{"prompt_tokens":5,"completion_tokens":2,"total_tokens":7}`),
	)
	defer srv.Close()
	p := newTestProvider(t, srv.URL, 0)
	evs := collectEvents(t, p, context.Background())

	var text, finish string
	var usage *core.Usage
	for _, ev := range evs {
		switch e := ev.(type) {
		case core.MTextDelta:
			text += e.Delta
		case core.MFinish:
			finish = e.Reason
		case core.MUsage:
			u := e.Usage
			usage = &u
		case core.MError:
			t.Fatalf("unexpected MError: %v", e.Err)
		}
	}
	if text != "hello world" {
		t.Errorf("text = %q, want 'hello world'", text)
	}
	if finish != "stop" {
		t.Errorf("finish = %q, want stop", finish)
	}
	if usage == nil || usage.InputTokens != 5 || usage.OutputTokens != 2 {
		t.Errorf("usage = %+v, want in=5 out=2", usage)
	}
}

// Verify: tool call streaming (first chunk id+name, subsequent arguments increment, index association).
func TestStreamToolCall(t *testing.T) {
	srv := sseServer(t,
		chunkJSON(`{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"bash","arguments":""}}]}`, "", ""),
		chunkJSON(`{"tool_calls":[{"index":0,"function":{"arguments":"{\"command\":\"ls\"}"}}]}`, "", ""),
	)
	defer srv.Close()
	p := newTestProvider(t, srv.URL, 0)
	evs := collectEvents(t, p, context.Background())

	var deltas []core.MToolUseDelta
	for _, ev := range evs {
		if d, ok := ev.(core.MToolUseDelta); ok {
			deltas = append(deltas, d)
		}
	}
	if len(deltas) != 2 {
		t.Fatalf("deltas = %d, want 2", len(deltas))
	}
	if deltas[0].ID != "call_1" || deltas[0].Name != "bash" {
		t.Errorf("delta0 = %+v", deltas[0])
	}
	if deltas[1].ID != "call_1" || deltas[1].DeltaJSON != `{"command":"ls"}` {
		t.Errorf("delta1 = %+v (id should associate to call_1)", deltas[1])
	}
}

// Verify: reasoning_content (DeepSeek thinking, extracted from RawJSON) -> MThinkingDelta.
func TestStreamReasoning(t *testing.T) {
	srv := sseServer(t,
		chunkJSON(`{"reasoning_content":"thinking..."}`, "", ""),
		chunkJSON(`{"content":"answer"}`, "", ""),
	)
	defer srv.Close()
	p := newTestProvider(t, srv.URL, 0)
	evs := collectEvents(t, p, context.Background())

	var thinking, text string
	for _, ev := range evs {
		switch e := ev.(type) {
		case core.MThinkingDelta:
			thinking += e.Delta
		case core.MTextDelta:
			text += e.Delta
		}
	}
	if thinking != "thinking..." {
		t.Errorf("thinking = %q, want 'thinking...'", thinking)
	}
	if text != "answer" {
		t.Errorf("text = %q, want 'answer'", text)
	}
}

// Verify: 4xx error (400 client error, no retry) -> MError + ClientError classification.
func TestStreamClientError(t *testing.T) {
	srv := errServer(t, 400)
	defer srv.Close()
	p := newTestProvider(t, srv.URL, 0)
	evs := collectEvents(t, p, context.Background())

	var merr *core.MError
	for _, ev := range evs {
		if e, ok := ev.(core.MError); ok {
			merr = &e
		}
	}
	if merr == nil {
		t.Fatal("expected MError for 400")
	}
	var ce *core.ClientError
	if !errors.As(merr.Err, &ce) {
		t.Errorf("expected ClientError, got %T: %v", merr.Err, merr.Err)
	} else if ce.StatusCode != 400 {
		t.Errorf("StatusCode = %d, want 400", ce.StatusCode)
	}
}

// Verify: Stream exits promptly when ctx is cancelled (no goroutine leak, no infinite blocking).
func TestStreamCtxCancel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		f, _ := w.(http.Flusher)
		fmt.Fprintf(w, "data: %s\n\n", chunkJSON(`{"content":"h"}`, "", ""))
		if f != nil {
			f.Flush()
		}
		// Slow enough for cancel to fire; respond to r.Context() so the handler exits when the client disconnects (avoiding Close blocking)
		select {
		case <-time.After(2 * time.Second):
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()
	p := newTestProvider(t, srv.URL, 0)

	ctx, cancel := context.WithCancel(context.Background())
	ch, err := p.Stream(ctx, core.ModelRequest{Messages: []core.Message{core.UserMessage("hi")}})
	if err != nil {
		t.Fatal(err)
	}

	go func() {
		time.Sleep(300 * time.Millisecond)
		cancel()
	}()

	started := time.Now()
	for range ch {
		// drain
	}
	elapsed := time.Since(started)
	if elapsed > 2*time.Second {
		t.Errorf("ctx cancel did not exit promptly: %v (goroutine may leak)", elapsed)
	}
}

// Verify: timeout (short RequestTimeout + slow server) -> MError.
func TestStreamTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		f, _ := w.(http.Flusher)
		// Send one chunk then nothing (no [DONE]), so the client waits for the next chunk and times out
		fmt.Fprintf(w, "data: %s\n\n", chunkJSON(`{}`, "", ""))
		f.Flush()
		time.Sleep(500 * time.Millisecond) // keep handler alive briefly to avoid long Close blocking
	}))
	defer srv.Close()
	// 100ms timeout
	p := newTestProvider(t, srv.URL, 100*time.Millisecond)
	evs := collectEvents(t, p, context.Background())

	var hasErr bool
	for _, ev := range evs {
		if _, ok := ev.(core.MError); ok {
			hasErr = true
		}
	}
	if !hasErr {
		t.Error("expected MError on timeout, got none")
	}
}
