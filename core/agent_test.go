package core

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// mockProvider returns preset ModelEvents per turn (tests the loop without real API).
type mockProvider struct {
	turns [][]ModelEvent
	call  int
}

func (m *mockProvider) Stream(ctx context.Context, req ModelRequest) (<-chan ModelEvent, error) {
	// Repeat the last turn when preset turns are exhausted (simulates persistent tool use for step_limit tests).
	var events []ModelEvent
	switch {
	case m.call < len(m.turns):
		events = m.turns[m.call]
	case len(m.turns) > 0:
		events = m.turns[len(m.turns)-1]
	}
	m.call++
	ch := make(chan ModelEvent, len(events))
	go func() {
		defer close(ch)
		for _, e := range events {
			ch <- e
		}
	}()
	return ch, nil
}

// echoTool is a test tool.
type echoTool struct{}

func (echoTool) Info() ToolInfo {
	return ToolInfo{
		Name:        "echo",
		Description: "echo back the input",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"msg":{"type":"string"}},"required":["msg"]}`),
	}
}

func (echoTool) Exec(ctx context.Context, input json.RawMessage) (ToolResult, error) {
	return ToolResult{Content: "echoed: " + string(input)}, nil
}

// TestAgentLoopToolUseAndFinish verifies: model calls one tool -> gets result -> outputs text -> FinishStop.
func TestAgentLoopToolUseAndFinish(t *testing.T) {
	mock := &mockProvider{
		turns: [][]ModelEvent{
			// Turn 1: model decides to call echo
			{MToolUseDelta{ID: "c1", Name: "echo", DeltaJSON: `{"msg":"hi"}`}},
			// Turn 2: model gives final text (no tool_use -> exit)
			{MTextDelta{Delta: "all "}, MTextDelta{Delta: "done"}},
		},
	}
	ag := NewAgent(mock, WithTools(echoTool{}), WithMaxSteps(5))

	events, err := ag.Stream(context.Background(), PromptInput("", "test"))
	if err != nil {
		t.Fatalf("Stream error: %v", err)
	}

	var text string
	var toolResults int
	var finish string
	for ev := range events {
		switch e := ev.(type) {
		case TextEvent:
			text += e.Delta
		case ToolResultEvent:
			toolResults++
		case FinishEvent:
			finish = string(e.Reason)
		case ErrorEvent:
			t.Fatalf("unexpected error event: %v", e.Err)
		}
	}

	if text != "all done" {
		t.Errorf("text = %q, want %q", text, "all done")
	}
	if toolResults != 1 {
		t.Errorf("tool results = %d, want 1", toolResults)
	}
	if finish != "stop" {
		t.Errorf("finish = %q, want stop", finish)
	}
}

// TestAgentStepLimit verifies: model calls tools every turn -> reaches MaxSteps -> FinishStepLimit.
func TestAgentStepLimit(t *testing.T) {
	mock := &mockProvider{
		turns: [][]ModelEvent{
			{MToolUseDelta{ID: "c1", Name: "echo", DeltaJSON: `{}`}},
		},
	}
	ag := NewAgent(mock, WithTools(echoTool{}), WithMaxSteps(2))

	events, _ := ag.Stream(context.Background(), PromptInput("", "loop"))
	var finish string
	for ev := range events {
		if f, ok := ev.(FinishEvent); ok {
			finish = string(f.Reason)
		}
	}
	if finish != "step_limit" {
		t.Errorf("finish = %q, want step_limit", finish)
	}
}

// TestAgentSingleToolUseStartEvent verifies providers that emit both delta and complete
// produce exactly one ToolUseStartEvent per tool call.
func TestAgentSingleToolUseStartEvent(t *testing.T) {
	mock := &mockProvider{
		turns: [][]ModelEvent{
			{
				MToolUseDelta{ID: "c1", Name: "echo", DeltaJSON: `{"msg":"hi"}`},
				MToolUseComplete{ID: "c1", Name: "echo", Input: json.RawMessage(`{"msg":"hi"}`)},
			},
			{MTextDelta{Delta: "done"}},
		},
	}
	ag := NewAgent(mock, WithTools(echoTool{}), WithMaxSteps(3))

	events, _ := ag.Stream(context.Background(), PromptInput("", "x"))
	var starts int
	for ev := range events {
		if _, ok := ev.(ToolUseStartEvent); ok {
			starts++
		}
	}
	if starts != 1 {
		t.Errorf("ToolUseStartEvent count = %d, want 1", starts)
	}
}

// TestAgentUnknownToolFailClosed verifies: unknown tool returns an IsError result to the model without crashing.
func TestAgentUnknownToolFailClosed(t *testing.T) {
	mock := &mockProvider{
		turns: [][]ModelEvent{
			{MToolUseDelta{ID: "c1", Name: "nonexistent", DeltaJSON: `{}`}},
			{MTextDelta{Delta: "ok"}},
		},
	}
	ag := NewAgent(mock, WithMaxSteps(3)) // no tools

	events, _ := ag.Stream(context.Background(), PromptInput("", "x"))
	var hadErrorResult bool
	for ev := range events {
		if r, ok := ev.(ToolResultEvent); ok && r.Result.IsError {
			hadErrorResult = true
		}
	}
	if !hadErrorResult {
		t.Error("expected IsError result for unknown tool (fail-closed)")
	}
}

// panicMW panics in BeforeModel to verify agent.Stream top-level recover: does not crash and emits ErrorEvent.
type panicMW struct{ BaseMiddleware }

func (panicMW) BeforeModel(context.Context, *RunState) error { panic("boom") }

func TestStreamRecoversPanic(t *testing.T) {
	mock := &mockProvider{turns: [][]ModelEvent{{MTextDelta{Delta: "x"}}}}
	ag := NewAgent(mock, WithMiddlewares(panicMW{})).(*agent)

	events, err := ag.Stream(context.Background(), PromptInput("", "x"))
	if err != nil {
		t.Fatal(err)
	}
	var gotErr bool
	for ev := range events {
		if e, ok := ev.(ErrorEvent); ok && strings.Contains(e.Err.Error(), "panic recovered") {
			gotErr = true
		}
	}
	if !gotErr {
		t.Error("agent.Stream should recover middleware panic and emit ErrorEvent (not crash)")
	}
}

// namedTool is a minimal tool used to test name-collision dedup. id distinguishes instances so the
// test can confirm dedupeTools keeps the first registration.
type namedTool struct{ name, id string }

func (n namedTool) Info() ToolInfo { return ToolInfo{Name: n.name} }
func (namedTool) Exec(context.Context, json.RawMessage) (ToolResult, error) {
	return ToolResult{}, nil
}

// TestDedupeTools verifies collision dedup keeps the first registration and preserves distinct names.
func TestDedupeTools(t *testing.T) {
	got := dedupeTools([]Tool{
		namedTool{"echo", "first"},
		namedTool{"echo", "second"},
		namedTool{"grep", "g"},
	})
	if len(got) != 2 {
		t.Fatalf("dedupeTools kept %d tools, want 2", len(got))
	}
	if first, ok := got[0].(namedTool); !ok || first.id != "first" {
		t.Errorf("dedupeTools should keep the first 'echo' registration, got %+v", got[0])
	}
	if got[1].Info().Name != "grep" {
		t.Errorf("distinct tool name should be preserved, got %q", got[1].Info().Name)
	}
}

// panicBeforeAgentMW panics in BeforeAgent, which runs synchronously inside Stream (before the
// worker goroutine launches) while the per-session lock is held.
type panicBeforeAgentMW struct{ BaseMiddleware }

func (panicBeforeAgentMW) BeforeAgent(context.Context, *RunState) error { panic("before-agent boom") }

// TestStreamReleasesSessionLockOnSyncPanic verifies that a panic in the synchronous setup phase
// does not leak the per-session lock (which would deadlock that session forever).
func TestStreamReleasesSessionLockOnSyncPanic(t *testing.T) {
	mock := &mockProvider{turns: [][]ModelEvent{{MTextDelta{Delta: "ok"}}}}
	ag := NewAgent(mock, WithMiddlewares(panicBeforeAgentMW{}))

	// First call: BeforeAgent panics synchronously while holding the session lock. Recover it.
	func() {
		defer func() { _ = recover() }()
		_, _ = ag.Stream(context.Background(), PromptInput("sess1", "x"))
		t.Fatal("expected BeforeAgent panic to propagate from Stream")
	}()

	// Second call on the same session: it must be able to acquire the session lock. If the lock
	// leaked, sessionLock.Lock() blocks forever and BeforeAgent is never reached.
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer func() { _ = recover() }()
		_, _ = ag.Stream(context.Background(), PromptInput("sess1", "y"))
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("second Stream on same session deadlocked: session lock leaked on synchronous panic")
	}
}
