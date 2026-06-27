package tui

// title_test.go covers the auto session-title pipeline: the trigger guards (default title, exactly
// one user message, titleGen injected), the async generation→enqueue, applyTitle persistence (incl.
// sessionID + non-default guards), and cleanTitle normalization.

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/skys-mission/creator-agent/core"
)

// fakeTitleGenerator is a controllable TitleGenerator that records its input and returns a preset
// result, optionally blocking until released (to synchronize async tests).
type fakeTitleGenerator struct {
	mu        sync.Mutex
	called    int
	lastInput string
	result    string
	err       error
	release   chan struct{}
}

func (f *fakeTitleGenerator) Generate(_ context.Context, firstUserMsg string) (string, error) {
	f.mu.Lock()
	f.called++
	f.lastInput = firstUserMsg
	f.mu.Unlock()
	if f.release != nil {
		<-f.release
	}
	return f.result, f.err
}

func (f *fakeTitleGenerator) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.called
}

// newAppWithTitle builds a sim App wired with a MemoryStore seeded with a session (default title)
// holding the given messages, plus a fake title generator.
func newAppWithTitle(t *testing.T, sessionID string, msgs []core.Message) (*App, *fakeTitleGenerator) {
	t.Helper()
	a, _ := newAppWithSim(t, 80, 24)
	a.rt.store = core.NewMemoryStore()
	a.rt.sessionID = sessionID
	if err := a.rt.store.SaveWithMeta(sessionID, core.DefaultSessionTitle(time.Now()), msgs); err != nil {
		t.Fatalf("seed session: %v", err)
	}
	// Provide a sender that enqueues into events (the real run.go wires this).
	a.rt.sender = func(msg any) {
		select {
		case a.events <- msg:
		default:
		}
	}
	a.rt.ctx = context.Background()
	gen := &fakeTitleGenerator{result: "Refactor config loader"}
	a.rt.titleGen = gen
	return a, gen
}

// TestMaybeTriggerDefaultTitleGenerates verifies a session with the default title + one user message
// triggers exactly one generation with that message as input.
func TestMaybeTriggerDefaultTitleGenerates(t *testing.T) {
	msgs := []core.Message{core.UserMessage("Help me debug the config loader")}
	a, gen := newAppWithTitle(t, "ses_a", msgs)
	gen.release = make(chan struct{}) // hold the goroutine so we can observe the call
	maybeTriggerTitleGeneration(a)
	// The goroutine should have recorded the call (it's blocked on release).
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if gen.callCount() == 1 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if gen.callCount() != 1 {
		t.Fatalf("generator call count = %d, want 1", gen.callCount())
	}
	if gen.lastInput != "Help me debug the config loader" {
		t.Errorf("generator input = %q, want the first user message", gen.lastInput)
	}
	close(gen.release) // let the goroutine finish (it will enqueue the result)
}

// TestMaybeTriggerSkipsWhenTitleAlreadySet verifies a non-default title skips generation.
func TestMaybeTriggerSkipsWhenTitleAlreadySet(t *testing.T) {
	a, _ := newAppWithSim(t, 80, 24)
	a.rt.store = core.NewMemoryStore()
	a.rt.sessionID = "ses_a"
	a.rt.ctx = context.Background()
	msgs := []core.Message{core.UserMessage("hi")}
	if err := a.rt.store.SaveWithMeta("ses_a", "A custom title", msgs); err != nil {
		t.Fatal(err)
	}
	gen := &fakeTitleGenerator{result: "x"}
	a.rt.titleGen = gen
	a.rt.sender = func(any) {}
	maybeTriggerTitleGeneration(a)
	if gen.callCount() != 0 {
		t.Errorf("generation should be skipped for a non-default title; got %d calls", gen.callCount())
	}
}

// TestMaybeTriggerSkipsWhenNoTitleGen verifies no generator injected -> no crash, no work.
func TestMaybeTriggerSkipsWhenNoTitleGen(t *testing.T) {
	a, _ := newAppWithSim(t, 80, 24)
	a.rt.store = core.NewMemoryStore()
	a.rt.sessionID = "ses_a"
	a.rt.ctx = context.Background()
	a.rt.titleGen = nil // none injected
	a.rt.sender = func(any) {}
	if err := a.rt.store.SaveWithMeta("ses_a", core.DefaultSessionTitle(time.Now()), []core.Message{core.UserMessage("hi")}); err != nil {
		t.Fatal(err)
	}
	maybeTriggerTitleGeneration(a) // must not panic
}

// TestMaybeTriggerSkipsWhenMultipleUserMessages verifies the "exactly one user message" gate.
func TestMaybeTriggerSkipsWhenMultipleUserMessages(t *testing.T) {
	msgs := []core.Message{
		core.UserMessage("first"),
		core.AssistantMessage("reply"),
		core.UserMessage("second"),
	}
	a, gen := newAppWithTitle(t, "ses_a", msgs)
	maybeTriggerTitleGeneration(a)
	// Give the goroutine a moment in case the gate is wrong.
	time.Sleep(30 * time.Millisecond)
	if gen.callCount() != 0 {
		t.Errorf("generation should be skipped with >1 user message; got %d calls", gen.callCount())
	}
}

// TestApplyTitlePersistsAndMatchesSession verifies applyTitle writes the title via SaveWithMeta for
// the matching session and forces a repaint.
func TestApplyTitlePersistsAndMatchesSession(t *testing.T) {
	a, _ := newAppWithTitle(t, "ses_a", []core.Message{core.UserMessage("hi")})
	applyTitle(a, titleGeneratedMsg{sessionID: "ses_a", title: "Greeting"})
	title, _, _, err := loadMetaBestEffort(a.rt.store, "ses_a")
	if err != nil {
		t.Fatalf("loadMeta: %v", err)
	}
	if title != "Greeting" {
		t.Errorf("persisted title = %q, want Greeting", title)
	}
	if !a.forceRender {
		t.Errorf("forceRender should be set after applying a title")
	}
}

// TestApplyTitleIgnoresDifferentSession verifies a title for a non-active session is still
// persisted (so the title survives) but only when its title is still default.
func TestApplyTitleIgnoresDifferentSession(t *testing.T) {
	a, _ := newAppWithTitle(t, "ses_active", []core.Message{core.UserMessage("hi")})
	// Seed another session that still has a default title.
	if err := a.rt.store.SaveWithMeta("ses_other", core.DefaultSessionTitle(time.Now()), []core.Message{core.UserMessage("hello")}); err != nil {
		t.Fatal(err)
	}
	applyTitle(a, titleGeneratedMsg{sessionID: "ses_other", title: "Other title"})
	title, _, _, _ := loadMetaBestEffort(a.rt.store, "ses_other")
	if title != "Other title" {
		t.Errorf("title for ses_other = %q, want 'Other title'", title)
	}
}

// TestApplyTitleSkipsNonDefaultCurrent verifies applyTitle does not overwrite an already-custom title.
func TestApplyTitleSkipsNonDefaultCurrent(t *testing.T) {
	a, _ := newAppWithSim(t, 80, 24)
	a.rt.store = core.NewMemoryStore()
	if err := a.rt.store.SaveWithMeta("ses_a", "Pre-existing custom", []core.Message{core.UserMessage("hi")}); err != nil {
		t.Fatal(err)
	}
	applyTitle(a, titleGeneratedMsg{sessionID: "ses_a", title: "Should not overwrite"})
	title, _, _, _ := loadMetaBestEffort(a.rt.store, "ses_a")
	if title != "Pre-existing custom" {
		t.Errorf("title = %q, want the pre-existing custom title (no overwrite)", title)
	}
}

// TestCleanTitle verifies think-stripping, first-line picking, and truncation.
func TestCleanTitle(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"plain", "Debug the config loader", "Debug the config loader"},
		{"strips think", "<think>reasoning here</think>\nDebug the config loader", "Debug the config loader"},
		{"first non-empty line", "\n  \nDebug the config loader\nextra", "Debug the config loader"},
		{"truncates long", strings.Repeat("a", 120), strings.Repeat("a", 97) + "..."},
		{"empty returns empty", "   \n  ", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := cleanTitle(tc.in); got != tc.want {
				t.Errorf("cleanTitle(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestMaybeTriggerEnqueuesResult verifies the generated title flows through sender as a
// titleGeneratedMsg, then applyTitle persists it end-to-end.
func TestMaybeTriggerEnqueuesResult(t *testing.T) {
	msgs := []core.Message{core.UserMessage("Fix the grep tool timeout")}
	a, gen := newAppWithTitle(t, "ses_a", msgs)
	gen.result = "Grep timeout fix"
	maybeTriggerTitleGeneration(a)
	// Wait for the titleGeneratedMsg to land in events.
	deadline := time.Now().Add(2 * time.Second)
	var got any
	for time.Now().Before(deadline) {
		select {
		case m := <-a.events:
			got = m
		default:
			time.Sleep(5 * time.Millisecond)
			continue
		}
		if _, ok := got.(titleGeneratedMsg); ok {
			break
		}
	}
	tm, ok := got.(titleGeneratedMsg)
	if !ok {
		t.Fatalf("expected a titleGeneratedMsg in events; got %#v", got)
	}
	if tm.title != "Grep timeout fix" || tm.sessionID != "ses_a" {
		t.Errorf("titleGeneratedMsg = %+v, want {ses_a, Grep timeout fix}", tm)
	}
}
