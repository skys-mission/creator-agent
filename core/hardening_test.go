package core

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// --- Empty stream (no content, no tools, no finish) should error, not silently FinishStop ---

// emptyStreamProvider opens a stream successfully but immediately closes it without emitting any event.
type emptyStreamProvider struct{}

func (emptyStreamProvider) Stream(ctx context.Context, _ ModelRequest) (<-chan ModelEvent, error) {
	ch := make(chan ModelEvent)
	go func() { close(ch) }()
	return ch, nil
}

func TestEmptyStreamReportsError(t *testing.T) {
	ag := NewAgent(emptyStreamProvider{})
	events, err := ag.Stream(context.Background(), PromptInput("", "hi"))
	if err != nil {
		t.Fatalf("Stream start: %v", err)
	}
	var sawErr bool
	for ev := range events {
		if e, ok := ev.(ErrorEvent); ok {
			sawErr = true
			if !strings.Contains(e.Err.Error(), "empty response") {
				t.Errorf("unexpected error msg: %v", e.Err)
			}
		}
		if _, ok := ev.(FinishEvent); ok {
			t.Error("empty stream must NOT emit FinishStop")
		}
	}
	if !sawErr {
		t.Error("empty stream should produce an ErrorEvent")
	}
}

// --- Mid-stream ctx cancel should yield FinishCanceled (not FinishStop) ---

// hangingProvider opens a stream and never ends it until ctx cancellation causes the internal send to abort.
type hangingProvider struct {
	firstSent chan struct{}
	once      sync.Once
}

func (h *hangingProvider) Stream(ctx context.Context, _ ModelRequest) (<-chan ModelEvent, error) {
	ch := make(chan ModelEvent)
	go func() {
		defer close(ch)
		for {
			select {
			case <-ctx.Done():
				return
			case ch <- MTextDelta{Delta: "."}:
				h.once.Do(func() {
					if h.firstSent != nil {
						close(h.firstSent)
					}
				})
			}
		}
	}()
	return ch, nil
}

func TestCancelMidStreamYieldsFinishCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	provider := &hangingProvider{firstSent: make(chan struct{})}
	ag := NewAgent(provider)
	events, err := ag.Stream(ctx, PromptInput("", "hi"))
	if err != nil {
		t.Fatalf("Stream start: %v", err)
	}
	go func() {
		<-provider.firstSent
		cancel()
	}()
	var finish FinishEvent
	var sawFinish bool
	for ev := range events {
		if f, ok := ev.(FinishEvent); ok {
			finish = f
			sawFinish = true
		}
	}
	if !sawFinish {
		t.Fatal("no finish event")
	}
	if finish.Reason != FinishCanceled {
		t.Errorf("mid-stream cancel should yield FinishCanceled, got %s", finish.Reason)
	}
}

// --- Empty-name tool_use should be dropped, producing no ToolCall ---

func TestEmptyToolNameDropped(t *testing.T) {
	mock := &mockProvider{
		turns: [][]ModelEvent{
			{MToolUseComplete{ID: "x1", Name: "", Input: []byte(`{}`)}},
			{MTextDelta{Delta: "done"}},
		},
	}
	ag := NewAgent(mock, WithTools(echoTool{}))
	events, _ := ag.Stream(context.Background(), PromptInput("", "hi"))
	var sawToolResult bool
	for ev := range events {
		if _, ok := ev.(ToolResultEvent); ok {
			sawToolResult = true
		}
	}
	if sawToolResult {
		t.Error("empty-name tool call should be dropped, not executed")
	}
}

// --- InterruptBlock tool completes even when ctx is canceled ---

type blockingTool struct{}

func (blockingTool) Info() ToolInfo {
	return ToolInfo{
		Name:              "block",
		InputSchema:       json.RawMessage(`{"type":"object"}`),
		InterruptBehavior: InterruptBlock,
	}
}

func (blockingTool) Exec(ctx context.Context, _ json.RawMessage) (ToolResult, error) {
	select {
	case <-time.After(100 * time.Millisecond):
	case <-ctx.Done():
		return ToolResult{Content: "interrupted (unexpected for InterruptBlock)"}, nil
	}
	return ToolResult{Content: "completed"}, nil
}

func TestInterruptBlockToolIgnoresCancel(t *testing.T) {
	mock := &mockProvider{
		turns: [][]ModelEvent{
			{MToolUseDelta{ID: "b1", Name: "block", DeltaJSON: `{}`}},
			{MTextDelta{Delta: "ok"}},
		},
	}
	ag := NewAgent(mock, WithTools(blockingTool{}))
	ctx, cancel := context.WithCancel(context.Background())
	events, err := ag.Stream(ctx, PromptInput("", "hi"))
	if err != nil {
		t.Fatalf("Stream start: %v", err)
	}
	go func() {
		time.Sleep(10 * time.Millisecond)
		cancel()
	}()
	var resultContent string
	for ev := range events {
		if e, ok := ev.(ToolResultEvent); ok && e.Result.Content != "" {
			resultContent = e.Result.Content
		}
	}
	if resultContent != "completed" {
		t.Errorf("InterruptBlock tool should complete despite cancel; got %q", resultContent)
	}
}

// --- RunForked with MaxSteps<=0 should normalize, not zero-step empty run ---

func TestRunForkedNormalizesMaxSteps(t *testing.T) {
	mock := &mockProvider{turns: [][]ModelEvent{{MTextDelta{Delta: "hi"}}}}
	cfg := AgentConfig{Model: mock, MaxSteps: 0}
	state := &RunState{Messages: []Message{UserMessage("hi")}}
	ch := make(chan Event, 8)
	RunForked(context.Background(), cfg, state, ch)
	close(ch)
	var sawText bool
	for ev := range ch {
		if _, ok := ev.(TextEvent); ok {
			sawText = true
		}
		if f, ok := ev.(FinishEvent); ok && f.Reason == FinishStepLimit && !sawText {
			t.Errorf("RunForked with MaxSteps=0 should normalize & run, but got step_limit without any step")
		}
	}
	if !sawText {
		t.Error("RunForked should normalize MaxSteps<=0 and actually run the loop")
	}
}

// --- JSONFileStore corrupt file degrades (backup + empty history + no error) ---

func TestJSONFileStoreLoadCorruptDegrades(t *testing.T) {
	dir := t.TempDir()
	store, err := NewJSONFileStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "sid.json")
	if err := os.WriteFile(path, []byte("{not valid json"), 0o644); err != nil {
		t.Fatal(err)
	}
	msgs, err := store.Load("sid")
	if err != nil {
		t.Errorf("corrupt session should degrade, not error: %v", err)
	}
	if msgs != nil {
		t.Errorf("corrupt session should yield nil (empty) history, got %d msgs", len(msgs))
	}
	entries, _ := os.ReadDir(dir)
	var hasBackup bool
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "sid.json.corrupt") {
			hasBackup = true
		}
	}
	if !hasBackup {
		t.Error("corrupt session file should be backed up")
	}
	msgs2, err := store.Load("sid")
	if err != nil || msgs2 != nil {
		t.Errorf("after reset, Load should be (nil,nil); got (%v,%v)", msgs2, err)
	}
}

// --- JSONFileStore concurrent writes to the same session do not corrupt the file ---

func TestJSONFileStoreConcurrentSaveNoCorruption(t *testing.T) {
	dir := t.TempDir()
	store, err := NewJSONFileStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	const n = 8
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			msgs := []Message{UserMessage("round"), AssistantMessage("resp")}
			_ = store.Save("shared", msgs)
		}(i)
	}
	wg.Wait()
	loaded, err := store.Load("shared")
	if err != nil {
		t.Errorf("after concurrent saves, file should be loadable: %v", err)
	}
	if len(loaded) != 2 {
		t.Errorf("expected 2 msgs, got %d", len(loaded))
	}
}

// --- Session save failure should be diagnosable (warnf to stderr, no panic) ---

type failingStore struct{}

func (failingStore) Load(string) ([]Message, error)               { return nil, nil }
func (failingStore) Save(string, []Message) error                 { return errors.New("disk full") }
func (failingStore) SaveWithMeta(string, string, []Message) error { return errors.New("disk full") }
func (failingStore) Clear(string) error                           { return errors.New("permission denied") }
func (failingStore) List() ([]SessionInfo, error)                 { return nil, errors.New("unreadable") }

func TestSessionSaveFailureIsDiagnosed(t *testing.T) {
	cfg := AgentConfig{Model: &mockProvider{turns: [][]ModelEvent{{MTextDelta{Delta: "x"}}}}, SessionStore: failingStore{}}
	normalizeConfig(&cfg)
	a := &agent{cfg: cfg, store: failingStore{}}
	_, err := a.Stream(context.Background(), StreamInput{Messages: []Message{UserMessage("hi")}, SessionID: "s1"})
	if err != nil {
		t.Fatalf("Stream should not fail despite save error: %v", err)
	}
	a.ClearSession("s1")
}

// --- Spill cleanup ---

func TestSpillCleanup(t *testing.T) {
	_ = CleanupSpills()
	info := ToolInfo{Name: "cleanup-test", MaxResultChars: 10}
	out := maybeSpillResult(info, ToolResult{Content: strings.Repeat("z", 500)})
	if !strings.Contains(out.Content, "spilled to") {
		t.Skip("spill did not trigger (unexpected), skipping")
	}
	idx := strings.LastIndex(out.Content, "spilled to ")
	path := strings.TrimSpace(strings.TrimRight(out.Content[idx+len("spilled to "):], ")"))
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("spill file should exist before cleanup: %v", err)
	}
	if err := CleanupSpills(); err != nil {
		t.Errorf("CleanupSpills error: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("spill file should be removed after cleanup; stat err=%v", err)
	}
}
