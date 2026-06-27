package core

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// delayTool simulates a tool with configurable delay; used for concurrency behavior tests.
type delayTool struct {
	name       string
	readOnly   bool
	concurrent bool
	delay      time.Duration
	inflight   *int32 // optional: current concurrent executions
	peak       *int32 // optional: historical max inflight (for concurrency degree tests)
}

func (d *delayTool) Info() ToolInfo {
	return ToolInfo{
		Name:            d.name,
		Description:     "delay tool for concurrency tests",
		InputSchema:     json.RawMessage(`{"type":"object"}`),
		ReadOnly:        d.readOnly,
		ConcurrencySafe: d.concurrent,
	}
}

func (d *delayTool) Exec(ctx context.Context, input json.RawMessage) (ToolResult, error) {
	if d.inflight != nil {
		cur := atomic.AddInt32(d.inflight, 1)
		defer atomic.AddInt32(d.inflight, -1)
		// Update historical peak (monotonically increasing; may slightly underestimate under race, but stable enough for >=2 checks).
		if d.peak != nil {
			for {
				old := atomic.LoadInt32(d.peak)
				if cur <= old || atomic.CompareAndSwapInt32(d.peak, old, cur) {
					break
				}
			}
		}
	}
	time.Sleep(d.delay)
	return ToolResult{Content: d.name + ":done"}, nil
}

// collectResults gathers the event stream into an ordered list of result IDs.
func collectResults(t *testing.T, events <-chan Event) []string {
	t.Helper()
	var out []string
	for ev := range events {
		switch e := ev.(type) {
		case ToolResultEvent:
			out = append(out, e.ID)
		case ErrorEvent:
			t.Fatalf("unexpected error: %v", e.Err)
		}
	}
	return out
}

// TestExecuteToolsParallelReadOnly verifies: two read-only tools run in parallel (total time ~ single, not 2x).
func TestExecuteToolsParallelReadOnly(t *testing.T) {
	var inflight, peak int32
	tool := &delayTool{name: "rd", readOnly: true, concurrent: true, delay: 80 * time.Millisecond, inflight: &inflight, peak: &peak}
	ag := &agent{cfg: AgentConfig{Tools: []Tool{tool}, MaxConcurrency: 10}}

	uses := []ToolCall{{ID: "a", Name: "rd"}, {ID: "b", Name: "rd"}, {ID: "c", Name: "rd"}}
	ch := make(chan Event, 10)
	start := time.Now()
	results := ag.executeTools(context.Background(), uses, ch)
	close(ch)
	elapsed := time.Since(start)

	if len(results) != 3 {
		t.Fatalf("results = %d, want 3", len(results))
	}
	// Parallel: 3x80ms should be well under serial 240ms. Tolerance: <200ms.
	if elapsed > 200*time.Millisecond {
		t.Errorf("not parallel: elapsed=%v (3x80ms serial would be ~240ms)", elapsed)
	}
	if peak < 2 {
		t.Errorf("peak=%d, want >=2 (proves concurrency)", peak)
	}
}

// TestExecuteToolsSerialWrites verifies: write tools (non-parallel) run serially (total time ~ N x single).
func TestExecuteToolsSerialWrites(t *testing.T) {
	tool := &delayTool{name: "wr", readOnly: false, concurrent: false, delay: 50 * time.Millisecond}
	ag := &agent{cfg: AgentConfig{Tools: []Tool{tool}, MaxConcurrency: 10}}

	uses := []ToolCall{{ID: "a", Name: "wr"}, {ID: "b", Name: "wr"}, {ID: "c", Name: "wr"}}
	ch := make(chan Event, 10)
	start := time.Now()
	results := ag.executeTools(context.Background(), uses, ch)
	close(ch)
	elapsed := time.Since(start)

	if len(results) != 3 {
		t.Fatalf("results = %d, want 3", len(results))
	}
	// Serial: 3x50ms = 150ms; concurrent would be <130ms.
	if elapsed < 130*time.Millisecond {
		t.Errorf("writes ran concurrently: elapsed=%v (expected ~150ms serial)", elapsed)
	}
}

// TestExecuteToolsMixedPartitionAndOrder verifies: [read, read, write, read] -> first two reads parallel, write fence, last read alone (batch size 1).
// Also verifies results are backfilled in original order.
func TestExecuteToolsMixedPartitionAndOrder(t *testing.T) {
	read := &delayTool{name: "rd", readOnly: true, concurrent: true, delay: 40 * time.Millisecond}
	write := &delayTool{name: "wr", readOnly: false, concurrent: false, delay: 40 * time.Millisecond}
	ag := &agent{cfg: AgentConfig{Tools: []Tool{read, write}, MaxConcurrency: 10}}

	uses := []ToolCall{
		{ID: "r1", Name: "rd"},
		{ID: "r2", Name: "rd"},
		{ID: "w1", Name: "wr"},
		{ID: "r3", Name: "rd"},
	}
	ch := make(chan Event, 10)
	results := ag.executeTools(context.Background(), uses, ch)
	close(ch)

	if len(results) != 4 {
		t.Fatalf("results = %d, want 4", len(results))
	}
	// Ordered backfill
	wantNames := []string{"rd", "rd", "wr", "rd"}
	for i, w := range wantNames {
		if results[i].name != w {
			t.Errorf("results[%d].name = %q, want %q", i, results[i].name, w)
		}
	}
	// ID order
	wantIDs := []string{"r1", "r2", "w1", "r3"}
	for i, w := range wantIDs {
		if results[i].callID != w {
			t.Errorf("results[%d].callID = %q, want %q", i, results[i].callID, w)
		}
	}
}

// TestExecuteToolsEventsEmitted verifies: all event IDs are emitted (order insensitive for UI, but the full set must arrive).
func TestExecuteToolsEventsEmitted(t *testing.T) {
	tool := &delayTool{name: "rd", readOnly: true, concurrent: true}
	ag := &agent{cfg: AgentConfig{Tools: []Tool{tool}, MaxConcurrency: 10}}

	uses := []ToolCall{{ID: "x1", Name: "rd"}, {ID: "x2", Name: "rd"}}
	ch := make(chan Event, 10)
	_ = ag.executeTools(context.Background(), uses, ch)
	close(ch)

	ids := collectResults(t, ch)
	if len(ids) != 2 {
		t.Fatalf("events = %v, want 2", ids)
	}
	got := strings.Join(ids, ",")
	if !strings.Contains(got, "x1") || !strings.Contains(got, "x2") {
		t.Errorf("missing IDs in events: %v", ids)
	}
}

// TestExecuteToolsUnknownToolInBatch verifies: unknown tools fail-closed (IsError result) without crashing, even inside a batch.
func TestExecuteToolsUnknownToolInBatch(t *testing.T) {
	read := &delayTool{name: "rd", readOnly: true, concurrent: true}
	ag := &agent{cfg: AgentConfig{Tools: []Tool{read}, MaxConcurrency: 10}}

	uses := []ToolCall{{ID: "r1", Name: "rd"}, {ID: "u1", Name: "unknown"}, {ID: "r2", Name: "rd"}}
	ch := make(chan Event, 10)
	results := ag.executeTools(context.Background(), uses, ch)
	close(ch)

	var errCount int
	for ev := range ch {
		if r, ok := ev.(ToolResultEvent); ok && r.Result.IsError {
			errCount++
		}
	}
	if errCount != 1 {
		t.Errorf("IsError results = %d, want 1 (unknown tool)", errCount)
	}
	if results[1].result.Content == "" || !results[1].result.IsError {
		t.Errorf("unknown tool result not marked error: %+v", results[1].result)
	}
}

// TestExecuteToolsMaxConcurrencyRespected verifies: MaxConcurrency actually limits concurrency (10 parallel calls, cap 3 -> peak <=3).
func TestExecuteToolsMaxConcurrencyRespected(t *testing.T) {
	var inflight, peak int32
	tool := &delayTool{name: "rd", readOnly: true, concurrent: true, delay: 50 * time.Millisecond, inflight: &inflight, peak: &peak}
	ag := &agent{cfg: AgentConfig{Tools: []Tool{tool}, MaxConcurrency: 3}}

	uses := make([]ToolCall, 10)
	for i := range uses {
		uses[i] = ToolCall{ID: "c", Name: "rd"}
	}
	ch := make(chan Event, 20)
	_ = ag.executeTools(context.Background(), uses, ch)
	close(ch)

	if peak > 3 {
		t.Errorf("peak=%d, want <=3 (MaxConcurrency)", peak)
	}
}

// Defensive: concurrent execution has no data race (run under -race; the race detector is the authority).
func TestExecuteToolsNoRace(t *testing.T) {
	tool := &delayTool{name: "rd", readOnly: true, concurrent: true, delay: 5 * time.Millisecond}
	ag := &agent{cfg: AgentConfig{Tools: []Tool{tool}, MaxConcurrency: 5}}

	uses := make([]ToolCall, 8)
	for i := range uses {
		uses[i] = ToolCall{ID: "c", Name: "rd"}
	}
	ch := make(chan Event, 20)
	_ = ag.executeTools(context.Background(), uses, ch)
	close(ch)
}

// panicTool panics on execution; verifies execOne recover downgrades panic to an error result instead of crashing the agent.
type panicTool struct{}

func (panicTool) Info() ToolInfo {
	return ToolInfo{Name: "panic", Description: "panics on exec", InputSchema: json.RawMessage(`{"type":"object"}`)}
}
func (panicTool) Exec(context.Context, json.RawMessage) (ToolResult, error) { panic("boom") }

func TestExecuteToolsRecoversPanic(t *testing.T) {
	ag := &agent{cfg: AgentConfig{Tools: []Tool{panicTool{}}, MaxConcurrency: 4}}
	uses := []ToolCall{{ID: "p1", Name: "panic"}}
	ch := make(chan Event, 4)
	results := ag.executeTools(context.Background(), uses, ch)
	close(ch)

	if len(results) != 1 {
		t.Fatalf("results = %d, want 1", len(results))
	}
	if !results[0].result.IsError {
		t.Errorf("panicked tool should yield IsError result, got %+v", results[0].result)
	}
	if !strings.Contains(results[0].result.Content, "panic") && !strings.Contains(results[0].result.Content, "boom") {
		t.Errorf("error result should mention panic/boom: %q", results[0].result.Content)
	}
	// Event should also be IsError
	var gotErr bool
	for ev := range ch {
		if r, ok := ev.(ToolResultEvent); ok && r.Result.IsError {
			gotErr = true
		}
	}
	if !gotErr {
		t.Error("no IsError event emitted for panicked tool")
	}
}

// blockTool ignores context cancellation and returns after a short delay (InterruptBlock behavior).
type blockTool struct{ delay time.Duration }

func (blockTool) Info() ToolInfo {
	return ToolInfo{
		Name:              "block",
		Description:       "ignores cancellation",
		InputSchema:       json.RawMessage(`{"type":"object"}`),
		InterruptBehavior: InterruptBlock,
	}
}
func (blockTool) Exec(context.Context, json.RawMessage) (ToolResult, error) {
	return ToolResult{Content: "blocked-done"}, nil
}

// TestExecuteToolsInterruptBlockDoesNotDeadlock verifies that an InterruptBlock tool whose
// consumer has stopped reading still lets the agent loop complete (send uses original ctx).
func TestExecuteToolsInterruptBlockDoesNotDeadlock(t *testing.T) {
	ag := &agent{cfg: AgentConfig{Tools: []Tool{blockTool{}}, MaxConcurrency: 4}}
	ctx, cancel := context.WithCancel(context.Background())
	uses := []ToolCall{{ID: "b1", Name: "block"}}
	ch := make(chan Event) // unbuffered: consumer has left

	// Cancel before the tool finishes and ensure executeTools returns without blocking forever.
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()

	done := make(chan struct{})
	var results []execResult
	go func() {
		defer close(done)
		results = ag.executeTools(ctx, uses, ch)
	}()

	select {
	case <-done:
		// OK
	case <-time.After(2 * time.Second):
		t.Fatal("executeTools deadlocked with canceled consumer")
	}
	if len(results) != 1 {
		t.Fatalf("results = %d, want 1", len(results))
	}
}

// TestContentForModel covers both branches of execResult.contentForModel:
// system errors become "Error: ..." text fed to the model; normal results pass through unchanged.
func TestContentForModel(t *testing.T) {
	// Error branch: system error (not tool IsError) is formatted as text so the model can see it.
	r := execResult{callID: "c1", name: "bash", err: errors.New("boom")}
	got := r.contentForModel()
	if !strings.Contains(got, "Error:") || !strings.Contains(got, "boom") {
		t.Errorf("err branch should format as 'Error: ...', got %q", got)
	}
	// Normal branch: content returned as-is
	r2 := execResult{callID: "c2", name: "read", result: ToolResult{Content: "file body"}}
	if got := r2.contentForModel(); got != "file body" {
		t.Errorf("normal branch should return content as-is, got %q", got)
	}
}
