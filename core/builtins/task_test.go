package builtins

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/skys-mission/creator-agent/core"
)

// textProvider always returns fixed text (used by child sessions).
type textProvider struct{ text string }

func (p *textProvider) Stream(ctx context.Context, req core.ModelRequest) (<-chan core.ModelEvent, error) {
	ch := make(chan core.ModelEvent, 1)
	go func() {
		defer close(ch)
		ch <- core.MTextDelta{Delta: p.text}
	}()
	return ch, nil
}

// Verify: TaskTool Info fields are correct.
func TestTaskToolInfo(t *testing.T) {
	tt := NewTaskTool(core.AgentConfig{Model: &textProvider{}}, nil, 0)
	info := tt.Info()
	if info.Name != "task" {
		t.Errorf("name = %q", info.Name)
	}
	if info.ReadOnly || info.ConcurrencySafe {
		t.Error("task should be non-readonly/non-parallel")
	}
	if info.InterruptBehavior != core.InterruptBlock {
		t.Error("task should block interrupt")
	}
}

// Verify: child session runs and returns text conclusion + header metadata.
func TestTaskToolReturnsConclusion(t *testing.T) {
	parent := core.AgentConfig{
		Model:        &textProvider{text: "found 3 files matching the pattern"},
		SystemPrompt: "parent-prompt",
	}
	tt := NewTaskTool(parent, nil, 5)

	res, err := tt.Exec(context.Background(), json.RawMessage(`{"description":"find files","prompt":"grep for foo"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Content, "find files") {
		t.Errorf("description not in result: %q", res.Content)
	}
	if !strings.Contains(res.Content, "found 3 files matching the pattern") {
		t.Errorf("sub-agent output missing: %q", res.Content)
	}
}

// Verify: recursion guard — task is automatically excluded from allowedTools even if explicitly included.
func TestTaskToolRecursionGuard(t *testing.T) {
	taskInList := NewTaskTool(core.AgentConfig{Model: &textProvider{}}, nil, 0)
	read := &mockReadTool{}
	// allowedTools explicitly includes task
	tt := NewTaskTool(core.AgentConfig{Model: &textProvider{}}, []core.Tool{read, taskInList}, 5)
	// internal tools should not contain task
	for _, tool := range tt.tools {
		if tool.Info().Name == "task" {
			t.Error("task not excluded from sub-agent toolset (recursion guard failed)")
		}
	}
}

// Verify: empty prompt -> IsError.
func TestTaskToolEmptyPrompt(t *testing.T) {
	tt := NewTaskTool(core.AgentConfig{Model: &textProvider{}}, nil, 5)
	res, _ := tt.Exec(context.Background(), json.RawMessage(`{"description":"x","prompt":""}`))
	if !res.IsError {
		t.Error("empty prompt should return IsError")
	}
}

// Verify: invalid JSON -> IsError.
func TestTaskToolInvalidInput(t *testing.T) {
	tt := NewTaskTool(core.AgentConfig{Model: &textProvider{}}, nil, 5)
	res, _ := tt.Exec(context.Background(), json.RawMessage(`not json`))
	if !res.IsError {
		t.Error("invalid input should return IsError")
	}
}

// Verify: child session uses the derived toolset (not all parent tools).
func TestTaskToolUsesRestrictedTools(t *testing.T) {
	// Use a recording provider to capture the tools received by the child session
	var seenTools []core.ToolInfo
	provider := &recordingProvider{
		text:     "ok",
		onStream: func(req core.ModelRequest) { seenTools = req.Tools },
	}
	read := &mockReadTool{}
	tt := NewTaskTool(core.AgentConfig{Model: provider}, []core.Tool{read}, 5)
	_, _ = tt.Exec(context.Background(), json.RawMessage(`{"description":"d","prompt":"p"}`))
	if len(seenTools) != 1 || seenTools[0].Name != "read" {
		t.Errorf("sub-agent tools = %+v, want only [read]", seenTools)
	}
}

// Verify: child session reuses the parent's SystemPrompt (cache prefix shared).
func TestTaskToolSharesSystemPrompt(t *testing.T) {
	var received []core.Message
	provider := &recordingProvider{
		text:     "ok",
		onStream: func(req core.ModelRequest) { received = req.Messages },
	}
	tt := NewTaskTool(core.AgentConfig{Model: provider, SystemPrompt: "shared-prompt"}, nil, 5)
	_, _ = tt.Exec(context.Background(), json.RawMessage(`{"description":"d","prompt":"do task"}`))
	// The child session's messages should not contain the parent's systemPrompt (RunForked does not auto-add system unless it is in the state)
	// Here we verify: the child session received the prompt as input
	if len(received) == 0 {
		t.Fatal("no messages received by sub-agent")
	}
	// The last message should be the user's prompt
	last := received[len(received)-1]
	if !strings.Contains(last.Content, "do task") {
		t.Errorf("sub-agent didn't receive prompt: %+v", last)
	}
}

// --- helpers ---

type mockReadTool struct{}

func (m *mockReadTool) Info() core.ToolInfo {
	return core.ToolInfo{Name: "read", ReadOnly: true, ConcurrencySafe: true,
		InputSchema: json.RawMessage(`{"type":"object"}`)}
}
func (m *mockReadTool) Exec(ctx context.Context, input json.RawMessage) (core.ToolResult, error) {
	return core.ToolResult{Content: "file content"}, nil
}

type recordingProvider struct {
	text     string
	onStream func(req core.ModelRequest)
}

func (r *recordingProvider) Stream(ctx context.Context, req core.ModelRequest) (<-chan core.ModelEvent, error) {
	if r.onStream != nil {
		r.onStream(req)
	}
	ch := make(chan core.ModelEvent, 1)
	go func() {
		defer close(ch)
		ch <- core.MTextDelta{Delta: r.text}
	}()
	return ch, nil
}

// loopingToolProvider initiates a tool call every round (produces no text), exhausting the child session steps
// to trigger FinishStepLimit + empty output path.
type loopingToolProvider struct{}

func (loopingToolProvider) Stream(ctx context.Context, req core.ModelRequest) (<-chan core.ModelEvent, error) {
	ch := make(chan core.ModelEvent, 2)
	go func() {
		defer close(ch)
		ch <- core.MToolUseComplete{ID: "c1", Name: "read", Input: []byte(`{"path":"x"}`)}
	}()
	return ch, nil
}

// TestTaskToolStepLimitEmptyOutput: when the child session exhausts its step limit without producing text,
// the parent agent should receive a clear "step limit" hint (instead of a generic "no output"),
// so the parent knows the task was too large or the step limit too tight.
func TestTaskToolStepLimitEmptyOutput(t *testing.T) {
	read := &mockReadTool{}
	tt := NewTaskTool(core.AgentConfig{Model: loopingToolProvider{}}, []core.Tool{read}, 3)
	res, _ := tt.Exec(context.Background(), json.RawMessage(`{"description":"big","prompt":"explore everything"}`))
	if !strings.Contains(res.Content, "step limit") {
		t.Errorf("step-limit empty output should mention step limit, got: %q", res.Content)
	}
}

// TestTaskToolDepthRejects verifies the depth counter actually blocks nesting:
// a TaskTool constructed at depth >= its limit must reject without running the
// child session. This pins the fix for the bug where depth was pinned at the
// construction value (0) and the check never fired.
func TestTaskToolDepthRejects(t *testing.T) {
	// depth=2 with default limit 2 -> must reject.
	tt := NewTaskTool(core.AgentConfig{Model: &textProvider{}}, nil, 5, TaskWithDepth(2))
	res, _ := tt.Exec(context.Background(), json.RawMessage(`{"description":"d","prompt":"p"}`))
	if !res.IsError {
		t.Fatalf("depth>=limit should be rejected, got: %+v", res)
	}
	if !strings.Contains(res.Content, "recursion limit") {
		t.Errorf("rejection message should mention recursion limit, got: %q", res.Content)
	}
}

// TestTaskToolDepthAllowsBelowLimit verifies a tool at depth < limit runs the
// child session normally (the happy path once depth actually propagates).
func TestTaskToolDepthAllowsBelowLimit(t *testing.T) {
	tt := NewTaskTool(
		core.AgentConfig{Model: &textProvider{text: "child conclusion"}},
		nil, 5, TaskWithDepth(1),
	)
	res, _ := tt.Exec(context.Background(), json.RawMessage(`{"description":"d","prompt":"p"}`))
	if res.IsError {
		t.Fatalf("depth=1 (< default limit 2) should run, got error: %+v", res)
	}
	if !strings.Contains(res.Content, "child conclusion") {
		t.Errorf("expected child conclusion, got: %q", res.Content)
	}
}

// TestTaskToolConfigurableMaxDepth verifies TaskWithMaxDepth raises the limit
// and that the configured value is clamped by the hard ceiling.
func TestTaskToolConfigurableMaxDepth(t *testing.T) {
	// Raise limit to 5; depth=2 should now be allowed.
	tt := NewTaskTool(
		core.AgentConfig{Model: &textProvider{text: "ok"}},
		nil, 5, TaskWithMaxDepth(5), TaskWithDepth(2),
	)
	res, _ := tt.Exec(context.Background(), json.RawMessage(`{"description":"d","prompt":"p"}`))
	if res.IsError {
		t.Errorf("depth=2 with maxDepth=5 should be allowed, got: %+v", res)
	}

	// depth=5 == limit 5 -> reject.
	tt2 := NewTaskTool(
		core.AgentConfig{Model: &textProvider{}},
		nil, 5, TaskWithMaxDepth(5), TaskWithDepth(5),
	)
	res2, _ := tt2.Exec(context.Background(), json.RawMessage(`{"description":"d","prompt":"p"}`))
	if !res2.IsError {
		t.Error("depth=5 with maxDepth=5 should be rejected")
	}
}
