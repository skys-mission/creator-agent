package core

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

type execResult struct {
	callID, name string
	result       ToolResult
	err          error
}

func (r execResult) contentForModel() string {
	if r.err != nil {
		return fmt.Sprintf("Error: %v", r.err)
	}
	return r.result.Content
}

// executeTools executes all tool calls for the current turn, backfilling results in call order.
//
// Concurrent batching (partitioned by concurrency safety):
//   - Consecutive read-only and concurrency-safe tools are grouped into batches executed in parallel, limited by MaxConcurrency.
//   - A non-concurrent (write / side-effect) call acts as a fence: the current batch is drained, then the tool runs serially.
//
// Results and events: backfilled into results in original order (the loop depends on ordered messages).
// Go channels are safe for concurrent sends, so event sends do not need extra serialization.
func (a *agent) executeTools(ctx context.Context, toolUses []ToolCall, ch chan<- Event) []execResult {
	toolMap := toolsByName(a.cfg.Tools)
	results := make([]execResult, len(toolUses))

	// still delivered to a live consumer even if ctx was canceled, without deadlocking when the
	// consumer has left. All other tools use sendEvent (respects ctx cancel).
	sendToolEvent := func(t Tool, ev Event) {
		if t.Info().InterruptBehavior == InterruptBlock {
			sendEventPreferDelivery(ctx, ch, ev)
		} else {
			_ = sendEvent(ctx, ch, ev)
		}
	}

	// The tool itself may run under a non-cancellable ctx (InterruptBlock) so work is not lost on Ctrl+C.
	execOne := func(idx int, tu ToolCall) {
		t, ok := toolMap[tu.Name]
		failTool := func(res ToolResult) {
			if ok {
				sendToolEvent(t, ToolResultEvent{ID: tu.ID, Result: res})
			} else {
				_ = sendEvent(ctx, ch, ToolResultEvent{ID: tu.ID, Result: res})
			}
			results[idx] = execResult{callID: tu.ID, name: tu.Name, result: res}
		}
		defer func() {
			if r := recover(); r != nil {
				// Tool / middleware / hook panics must not crash the agent: downgrade to an error result for this tool.
				failTool(ToolResult{Content: fmt.Sprintf("tool %q panicked: %v", tu.Name, r), IsError: true})
			}
		}()
		if !ok {
			failTool(ToolResult{Content: fmt.Sprintf("Error: tool %q not found", tu.Name), IsError: true})
			return
		}

		fn := ToolFunc(func(ctx context.Context, input json.RawMessage) (ToolResult, error) {
			return t.Exec(ctx, input)
		})
		for j := len(a.cfg.Middlewares) - 1; j >= 0; j-- {
			fn = a.cfg.Middlewares[j].WrapTool(tu.Name, fn)
		}

		// InterruptBlock tools (e.g., task sub-session) use a non-cancellable ctx to avoid losing work on Ctrl+C.
		// Other tools keep the original ctx (responsive to cancellation).
		execCtx := ctx
		if t.Info().InterruptBehavior == InterruptBlock {
			execCtx = context.WithoutCancel(ctx)
		}
		res, err := fn(execCtx, tu.Input)
		if err != nil {
			debugLogExecResult(tu.ID, tu.Name, ToolResult{}, err)
			sendToolEvent(t, ToolResultEvent{ID: tu.ID, Err: err})
			results[idx] = execResult{callID: tu.ID, name: tu.Name, err: err}
			return
		}
		// Large results spill to disk (controlled by ToolInfo.MaxResultChars).
		res = maybeSpillResult(t.Info(), res)
		debugLogExecResult(tu.ID, tu.Name, res, nil)
		sendToolEvent(t, ToolResultEvent{ID: tu.ID, Result: res})
		results[idx] = execResult{callID: tu.ID, name: tu.Name, result: res}
	}

	canParallel := func(tu ToolCall) bool {
		t, ok := toolMap[tu.Name]
		if !ok {
			return false
		}
		info := t.Info()
		return info.ReadOnly && info.ConcurrencySafe
	}

	i := 0
	for i < len(toolUses) {
		if !canParallel(toolUses[i]) {
			execOne(i, toolUses[i])
			i++
			continue
		}
		start := i
		for i < len(toolUses) && canParallel(toolUses[i]) {
			i++
		}
		batch := toolUses[start:i]

		sem := make(chan struct{}, a.cfg.MaxConcurrency)
		var wg sync.WaitGroup
		for k, tu := range batch {
			wg.Add(1)
			sem <- struct{}{}
			go func(idx int, call ToolCall) {
				defer wg.Done()
				defer func() { <-sem }()
				execOne(idx, call)
			}(start+k, tu)
		}
		wg.Wait()
	}
	return results
}

func toolInfos(tools []Tool) []ToolInfo {
	out := make([]ToolInfo, len(tools))
	for i, t := range tools {
		out[i] = t.Info()
	}
	return out
}

func toolsByName(tools []Tool) map[string]Tool {
	m := make(map[string]Tool, len(tools))
	for _, t := range tools {
		m[t.Info().Name] = t
	}
	return m
}

const spillPreview = 2000

var spillTracker struct {
	mu    sync.Mutex
	paths []string
}

func CleanupSpills() error {
	spillTracker.mu.Lock()
	paths := spillTracker.paths
	spillTracker.paths = nil
	spillTracker.mu.Unlock()
	var firstErr error
	for _, p := range paths {
		if err := os.Remove(p); err != nil && !os.IsNotExist(err) && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func maybeSpillResult(info ToolInfo, r ToolResult) ToolResult {
	if r.IsError || info.MaxResultChars <= 0 || len(r.Content) <= info.MaxResultChars {
		return r
	}
	full := r.Content
	preview := TruncateBytesMaxSafe(full, spillPreview)

	path, err := spillToDisk(info.Name, full)
	if err != nil {
		runes := []rune(full)
		if len(runes) > info.MaxResultChars {
			runes = runes[:info.MaxResultChars]
		}
		return ToolResult{Content: string(runes) + "\n... (truncated, spill failed: " + err.Error() + ")"}
	}
	spillTracker.mu.Lock()
	spillTracker.paths = append(spillTracker.paths, path)
	spillTracker.mu.Unlock()
	return ToolResult{
		Content: preview + fmt.Sprintf("\n... (full output, %d bytes, spilled to %s)", len(full), path),
	}
}

// TruncateBytesMaxSafe returns s truncated to at most max bytes on a UTF-8 rune boundary, without
// appending any ellipsis. It backs off past any continuation byte so the result is always valid
// UTF-8 even when max lands inside a multi-byte rune. Use this for byte-budgeted previews where a
// naive s[:max] slice would split a rune.
func TruncateBytesMaxSafe(s string, max int) string {
	if max <= 0 {
		return ""
	}
	if len(s) <= max {
		return s
	}
	end := max
	for end > 0 && s[end]&0xC0 == 0x80 {
		end--
	}
	return s[:end]
}

func spillToDisk(toolName, content string) (string, error) {
	var buf [8]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", fmt.Errorf("gen id: %w", err)
	}
	name := fmt.Sprintf("creator-agent-%s-%s.txt", sanitizeToolName(toolName), hex.EncodeToString(buf[:]))
	dir := os.TempDir()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		return "", fmt.Errorf("write spill: %w", err)
	}
	return path, nil
}

func sanitizeToolName(name string) string {
	if s := SanitizeFilename(name); s != "" {
		return s
	}
	return "tool"
}
