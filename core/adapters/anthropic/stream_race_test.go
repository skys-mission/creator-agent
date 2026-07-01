package anthropic

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/skys-mission/creator-agent/core"
)

// TestStreamConcurrentIntegrity stresses the streaming decode path: many goroutines share one
// Provider and each drains its own event stream, reconstructing text + tool-call arguments.
// Run with `-race`; content-equality assertions also catch torn reads that race instrumentation misses.
func TestStreamConcurrentIntegrity(t *testing.T) {
	textParts := make([]string, 0, 24)
	frames := make([]sseFrame, 0, 32)
	for i := 0; i < 24; i++ {
		part := "tok" + string(rune('A'+i%26)) + "_"
		textParts = append(textParts, part)
		frames = append(frames, sseFrame{"content_block_delta",
			fmt.Sprintf(`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"%s"}}`, part)})
	}
	wantText := strings.Join(textParts, "")

	frames = append(frames,
		sseFrame{"content_block_start", `{"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"t1","name":"bash"}}`},
		sseFrame{"content_block_delta", `{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"command\":"}}`},
		sseFrame{"content_block_delta", `{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"\"ls -la\"}"}}`},
	)
	const wantArgs = `{"command":"ls -la"}`

	srv := sseServer(t, frames...)
	defer srv.Close()
	p := newTestProvider(t, srv.URL, 10*time.Second)

	const workers = 64
	var wg sync.WaitGroup
	errCh := make(chan string, workers)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ch, err := p.Stream(context.Background(), core.ModelRequest{
				Messages: []core.Message{core.UserMessage("hi")},
			})
			if err != nil {
				errCh <- "Stream: " + err.Error()
				return
			}
			var text, args strings.Builder
			for ev := range ch {
				switch e := ev.(type) {
				case core.MTextDelta:
					text.WriteString(e.Delta)
				case core.MToolUseDelta:
					args.WriteString(e.DeltaJSON)
				case core.MError:
					errCh <- "MError: " + e.Err.Error()
				}
			}
			if got := text.String(); got != wantText {
				errCh <- "text mismatch: got " + got
			}
			if got := args.String(); got != wantArgs {
				errCh <- "args mismatch: got " + got
			}
		}()
	}
	wg.Wait()
	close(errCh)
	for msg := range errCh {
		t.Error(msg)
	}
}
