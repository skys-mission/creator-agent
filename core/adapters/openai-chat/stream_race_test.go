package openaichat

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/skys-mission/creator-agent/core"
)

// TestStreamConcurrentIntegrity stresses the streaming decode path: many goroutines share one
// Provider (as production does) and each opens its own SSE stream, drains the event channel, and
// reconstructs the text + tool-call arguments. It guards against data races / buffer-aliasing in the
// SSE scanner, JSON decode, convertChunk, and the channel handoff — the active path during the
// observed "found bad pointer in Go heap" crashes. Run with `-race`; the content-equality assertions
// also catch torn reads that race instrumentation can miss (e.g. unsafe zero-copy in a dependency).
func TestStreamConcurrentIntegrity(t *testing.T) {
	// Build a long-ish reply across many chunks so the scanner buffer is exercised heavily.
	textParts := make([]string, 0, 24)
	bodies := make([]string, 0, 32)
	for i := 0; i < 24; i++ {
		part := "tok" + string(rune('A'+i%26)) + "_"
		textParts = append(textParts, part)
		bodies = append(bodies, chunkJSON(`{"content":"`+part+`"}`, "", ""))
	}
	wantText := strings.Join(textParts, "")

	// A streamed tool call whose arguments arrive split across chunks.
	bodies = append(bodies,
		chunkJSON(`{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"bash","arguments":""}}]}`, "", ""),
		chunkJSON(`{"tool_calls":[{"index":0,"function":{"arguments":"{\"command\":"}}]}`, "", ""),
		chunkJSON(`{"tool_calls":[{"index":0,"function":{"arguments":"\"ls -la\"}"}}]}`, "", ""),
		chunkJSON(`{}`, "stop", `{"prompt_tokens":3,"completion_tokens":5,"total_tokens":8}`),
	)
	const wantArgs = `{"command":"ls -la"}`

	srv := sseServer(t, bodies...)
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
