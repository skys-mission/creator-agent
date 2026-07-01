package openairesponses

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	core "github.com/skys-mission/creator-agent/core"
	"github.com/skys-mission/creator-agent/core/adapters/shared"
)

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

// sseServer emits SSE data lines then [DONE], matching the OpenAI streaming wire format.
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

// Verify: text deltas stream through to MTextDelta.
func TestStream_TextDelta(t *testing.T) {
	srv := sseServer(t,
		`{"type":"response.output_text.delta","delta":"hello"}`,
		`{"type":"response.output_text.delta","delta":" world"}`,
	)
	defer srv.Close()
	p := newTestProvider(t, srv.URL, 0)
	evs := collectEvents(t, p, context.Background())

	var text string
	for _, ev := range evs {
		switch e := ev.(type) {
		case core.MTextDelta:
			text += e.Delta
		case core.MError:
			t.Fatalf("unexpected MError: %v", e.Err)
		}
	}
	if text != "hello world" {
		t.Fatalf("text = %q, want 'hello world'", text)
	}
}

// Verify: function call streaming (output_item.added carries call id/name; arguments.delta increments).
func TestStream_FunctionCall(t *testing.T) {
	srv := sseServer(t,
		`{"type":"response.output_item.added","item":{"type":"function_call","id":"item_1","call_id":"call_1","name":"bash"}}`,
		`{"type":"response.function_call_arguments.delta","item_id":"item_1","delta":"{\"cmd\":\"ls\"}"}`,
	)
	defer srv.Close()
	p := newTestProvider(t, srv.URL, 0)
	evs := collectEvents(t, p, context.Background())

	var deltas []core.MToolUseDelta
	for _, ev := range evs {
		if e, ok := ev.(core.MToolUseDelta); ok {
			deltas = append(deltas, e)
		}
	}
	if len(deltas) != 1 {
		t.Fatalf("deltas = %d, want 1", len(deltas))
	}
	if deltas[0].ID != "call_1" || deltas[0].Name != "bash" || deltas[0].DeltaJSON != `{"cmd":"ls"}` {
		t.Fatalf("bad tool delta: %+v", deltas[0])
	}
}
