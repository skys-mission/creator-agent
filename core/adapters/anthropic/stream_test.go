package anthropic

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
		BaseURL: baseURL, APIKey: "test", Model: "claude-3-5-sonnet-latest", RequestTimeout: timeout,
	})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// sseFrame is one Anthropic SSE frame: an event line plus its data JSON.
type sseFrame struct {
	event string
	data  string
}

// sseServer emits Anthropic SSE frames (event: + data:), then closes the connection (stream ends
// on EOF; Anthropic has no [DONE] sentinel — the message_stop event + close ends the stream).
func sseServer(t *testing.T, frames ...sseFrame) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		f, _ := w.(http.Flusher)
		for _, fr := range frames {
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", fr.event, fr.data)
			if f != nil {
				f.Flush()
			}
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

// Verify: text deltas stream through to MTextDelta; stop_reason -> MFinish.
func TestStream_TextAndFinish(t *testing.T) {
	srv := sseServer(t,
		sseFrame{"content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hello"}}`},
		sseFrame{"content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":" world"}}`},
		sseFrame{"message_delta", `{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":2}}`},
	)
	defer srv.Close()
	p := newTestProvider(t, srv.URL, 0)
	evs := collectEvents(t, p, context.Background())

	var text, finish string
	for _, ev := range evs {
		switch e := ev.(type) {
		case core.MTextDelta:
			text += e.Delta
		case core.MFinish:
			finish = e.Reason
		case core.MError:
			t.Fatalf("unexpected MError: %v", e.Err)
		}
	}
	if text != "hello world" {
		t.Fatalf("text = %q, want 'hello world'", text)
	}
	if finish != "end_turn" {
		t.Fatalf("finish = %q, want end_turn", finish)
	}
}

// Verify: tool_use streaming (content_block_start carries id/name; input_json_delta increments).
func TestStream_FunctionCall(t *testing.T) {
	srv := sseServer(t,
		sseFrame{"content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"t1","name":"bash"}}`},
		sseFrame{"content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"cmd\":\"ls\"}"}}`},
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
	if deltas[0].ID != "t1" || deltas[0].Name != "bash" || deltas[0].DeltaJSON != `{"cmd":"ls"}` {
		t.Fatalf("bad tool delta: %+v", deltas[0])
	}
}

// Verify: thinking + signature deltas stream through (multi-turn continuity events).
func TestStream_ThinkingAndSignature(t *testing.T) {
	srv := sseServer(t,
		sseFrame{"content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"reasoning..."}}`},
		sseFrame{"content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"SIG"}}`},
	)
	defer srv.Close()
	p := newTestProvider(t, srv.URL, 0)
	evs := collectEvents(t, p, context.Background())

	var thinking, sig string
	for _, ev := range evs {
		switch e := ev.(type) {
		case core.MThinkingDelta:
			thinking = e.Delta
		case core.MThinkingSignature:
			sig = e.Signature
		case core.MError:
			t.Fatalf("unexpected MError: %v", e.Err)
		}
	}
	if thinking != "reasoning..." {
		t.Fatalf("thinking = %q", thinking)
	}
	if sig != "SIG" {
		t.Fatalf("signature = %q", sig)
	}
}
