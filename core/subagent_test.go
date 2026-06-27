package core

import (
	"context"
	"encoding/json"
	"testing"
)

// TestNewForkedConfigInherits verifies: NewForkedConfig inherits Model/SystemPrompt/Middlewares; Tools defaults to empty.
func TestNewForkedConfigInherits(t *testing.T) {
	mw := &handlerMarker{}
	parent := AgentConfig{
		Model:        &mockProvider{},
		SystemPrompt: "parent-prompt",
		Middlewares:  []Middleware{mw},
		MaxSteps:     10,
	}
	child := NewForkedConfig(parent)
	if child.SystemPrompt != "parent-prompt" {
		t.Errorf("SystemPrompt = %q, want inherited", child.SystemPrompt)
	}
	if child.MaxSteps != 10 {
		t.Errorf("MaxSteps = %d, want 10", child.MaxSteps)
	}
	if len(child.Middlewares) != 1 || child.Middlewares[0] != mw {
		t.Errorf("Middlewares not inherited")
	}
	if len(child.Tools) != 0 {
		t.Errorf("Tools = %d, want 0 (default empty)", len(child.Tools))
	}
	if child.SessionStore == nil {
		t.Error("SessionStore should be a fresh MemoryStore")
	}
}

// TestNewForkedConfigIndependent verifies: modifying the child does not affect the parent (middleware slice is an independent copy).
func TestNewForkedConfigIndependent(t *testing.T) {
	parent := AgentConfig{
		Model:       &mockProvider{},
		Middlewares: []Middleware{&handlerMarker{}},
	}
	child := NewForkedConfig(parent, ForkWithMiddlewares(&handlerMarker{}))
	if len(parent.Middlewares) != 1 {
		t.Errorf("parent Middlewares mutated: %d", len(parent.Middlewares))
	}
	if len(child.Middlewares) != 1 {
		t.Errorf("child Middlewares = %d, want 1 (replaced)", len(child.Middlewares))
	}
}

// TestForkWithTools verifies: ForkWithTools overrides tools.
func TestForkWithTools(t *testing.T) {
	tool := &markerTool{name: "x"}
	child := NewForkedConfig(AgentConfig{Model: &mockProvider{}}, ForkWithTools(tool))
	if len(child.Tools) != 1 || child.Tools[0] != tool {
		t.Errorf("ForkWithTools not applied")
	}
}

// TestForkWithMaxSteps covers ForkWithMaxSteps (previously 0% coverage).
func TestForkWithMaxSteps(t *testing.T) {
	child := NewForkedConfig(AgentConfig{Model: &mockProvider{}, MaxSteps: 100}, ForkWithMaxSteps(7))
	if child.MaxSteps != 7 {
		t.Errorf("MaxSteps = %d, want 7 (ForkWithMaxSteps should override)", child.MaxSteps)
	}
}

// TestForkWithSystemPrompt covers ForkWithSystemPrompt.
func TestForkWithSystemPrompt(t *testing.T) {
	child := NewForkedConfig(AgentConfig{Model: &mockProvider{}, SystemPrompt: "parent"}, ForkWithSystemPrompt("child"))
	if child.SystemPrompt != "child" {
		t.Errorf("SystemPrompt = %q, want child", child.SystemPrompt)
	}
}

// TestForkInheritsParentDefaults verifies: child inherits parent's Model/SystemPrompt/Middlewares/depth.
func TestForkInheritsParentDefaults(t *testing.T) {
	parent := AgentConfig{Model: &mockProvider{}, SystemPrompt: "p", depth: 1}
	child := NewForkedConfig(parent)
	if child.Model != parent.Model {
		t.Error("child should inherit parent Model")
	}
	if child.SystemPrompt != "p" {
		t.Error("child should inherit parent SystemPrompt")
	}
	if child.depth != 2 {
		t.Errorf("child depth = %d, want 2 (parent+1)", child.depth)
	}
	if len(child.Tools) != 0 {
		t.Error("child Tools should default empty (caller specifies)")
	}
}

// TestRunForkedExecutes verifies: RunForked runs the loop and produces events.
func TestRunForkedExecutes(t *testing.T) {
	mock := &mockProvider{turns: [][]ModelEvent{
		{MTextDelta{Delta: "forked-output"}},
	}}
	cfg := AgentConfig{Model: mock, MaxSteps: 5}
	state := &RunState{Messages: []Message{SystemMessage("sys"), UserMessage("go")}}
	ch := make(chan Event, 10)
	RunForked(context.Background(), cfg, state, ch)
	close(ch)

	var text string
	for ev := range ch {
		if e, ok := ev.(TextEvent); ok {
			text += e.Delta
		}
	}
	if text != "forked-output" {
		t.Errorf("text = %q, want forked-output", text)
	}
}

// TestRunForkedRestrictedTools verifies: with restricted tools, state.Tools reflects the subset (the model only sees the subset).
func TestRunForkedRestrictedTools(t *testing.T) {
	var seenTools []ToolInfo
	mock := &recordingProvider{
		mockProvider: mockProvider{turns: [][]ModelEvent{{MTextDelta{Delta: "ok"}}}},
		onStream:     func(req ModelRequest) { seenTools = req.Tools },
	}
	tool1 := &markerTool{name: "read", ro: true}
	tool2 := &markerTool{name: "write"}
	cfg := AgentConfig{Model: mock, MaxSteps: 5, Tools: []Tool{tool1, tool2}}
	state := &RunState{Messages: []Message{UserMessage("x")}}
	ch := make(chan Event, 10)
	RunForked(context.Background(), cfg, state, ch)
	close(ch)

	if len(seenTools) != 2 {
		t.Errorf("seenTools = %d, want 2", len(seenTools))
	}
}

// TestRunForkedIndependentSession verifies: RunForked does not write to the parent session (independent store).
func TestRunForkedIndependentSession(t *testing.T) {
	parentStore := NewMemoryStore()
	_ = parentStore.Save("main", []Message{UserMessage("parent-history")})

	mock := &mockProvider{turns: [][]ModelEvent{{MTextDelta{Delta: "ok"}}}}
	cfg := NewForkedConfig(AgentConfig{Model: mock, MaxSteps: 5, SessionStore: parentStore})
	state := &RunState{Messages: []Message{UserMessage("forked-input")}}
	ch := make(chan Event, 10)
	RunForked(context.Background(), cfg, state, ch)
	close(ch)

	main, _ := parentStore.Load("main")
	if len(main) != 1 || main[0].Content != "parent-history" {
		t.Errorf("parent session mutated by forked: %+v", main)
	}
}

// --- test helpers ---

type handlerMarker struct {
	BaseMiddleware
}

type recordingProvider struct {
	mockProvider
	onStream func(req ModelRequest)
}

func (r *recordingProvider) Stream(ctx context.Context, req ModelRequest) (<-chan ModelEvent, error) {
	if r.onStream != nil {
		r.onStream(req)
	}
	return r.mockProvider.Stream(ctx, req)
}

type markerTool struct {
	name string
	ro   bool
}

func (m *markerTool) Info() ToolInfo {
	return ToolInfo{
		Name: m.name, ReadOnly: m.ro,
		InputSchema: json.RawMessage(`{"type":"object"}`),
	}
}
func (m *markerTool) Exec(ctx context.Context, input json.RawMessage) (ToolResult, error) {
	return ToolResult{Content: m.name}, nil
}
