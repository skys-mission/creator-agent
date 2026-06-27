package main

// compact_test.go covers the manual /compact logic: compactMessages (the pure summarize+partition
// step) and the newCompactFactory wiring (load → compact → persist). Uses an in-process mock
// ModelProvider so no network is needed.

import (
	"context"
	"strings"
	"testing"

	"github.com/skys-mission/creator-agent/core"
)

// mockCompactProvider returns a fixed summary for any CollectText call.
type mockCompactProvider struct{ summary string }

func (m *mockCompactProvider) Stream(ctx context.Context, req core.ModelRequest) (<-chan core.ModelEvent, error) {
	ch := make(chan core.ModelEvent, 1)
	ch <- core.MTextDelta{Delta: m.summary}
	close(ch)
	return ch, nil
}

// buildHistory builds a system + N user/assistant turn history.
func buildHistory(nTurns int) []core.Message {
	msgs := []core.Message{core.SystemMessage("sys")}
	for i := 0; i < nTurns; i++ {
		msgs = append(msgs, core.UserMessage("user turn"))
		msgs = append(msgs, core.AssistantMessage("assistant turn"))
	}
	return msgs
}

// TestCompactMessagesSummarizes verifies compactMessages produces a shorter history with a summary
// and preserves the system message + recent tail.
func TestCompactMessagesSummarizes(t *testing.T) {
	provider := &mockCompactProvider{summary: "this is the summary"}
	msgs := buildHistory(20) // system + 40 msgs = 41, well above the threshold
	out, ok, err := compactMessages(context.Background(), provider, msgs)
	if err != nil {
		t.Fatalf("compactMessages: %v", err)
	}
	if !ok {
		t.Fatalf("ok should be true for a long history")
	}
	// Output must be shorter than input.
	if len(out) >= len(msgs) {
		t.Errorf("compacted length = %d, want < %d", len(out), len(msgs))
	}
	// System message preserved at the front.
	if len(out) == 0 || out[0].Role != core.RoleSystem {
		t.Errorf("first message should be the system message; got role=%v", firstRole(out))
	}
	// The summary appears somewhere (as a user message).
	foundSummary := false
	for _, m := range out {
		if m.Role == core.RoleUser && strings.Contains(m.Content, "this is the summary") {
			foundSummary = true
		}
	}
	if !foundSummary {
		t.Errorf("compacted history should contain the summary text")
	}
}

// TestCompactMessagesTooShort verifies a short history is returned unchanged (ok=false).
func TestCompactMessagesTooShort(t *testing.T) {
	provider := &mockCompactProvider{summary: "summary"}
	msgs := buildHistory(2) // system + 4 = 5, below minMessagesToCompact (8)
	out, ok, err := compactMessages(context.Background(), provider, msgs)
	if err != nil {
		t.Fatalf("compactMessages: %v", err)
	}
	if ok {
		t.Errorf("ok should be false for a short history")
	}
	if len(out) != len(msgs) {
		t.Errorf("short history should be returned unchanged; got %d, want %d", len(out), len(msgs))
	}
}

// TestCompactFactoryPersists verifies the factory compacts a session's stored history and persists
// the compacted messages (the store ends up shorter than the seed).
func TestCompactFactoryPersists(t *testing.T) {
	store := core.NewMemoryStore()
	sessionID := "ses_compact"
	seed := buildHistory(20)
	if err := store.SaveWithMeta(sessionID, "title", seed); err != nil {
		t.Fatal(err)
	}
	provider := &mockCompactProvider{summary: "summary text"}
	factory := newCompactFactory(func() core.ModelProvider { return provider }, store)
	if factory == nil {
		t.Fatal("factory should be non-nil")
	}
	out, err := factory(context.Background(), sessionID)
	if err != nil {
		t.Fatalf("factory: %v", err)
	}
	if len(out) >= len(seed) {
		t.Errorf("compacted length = %d, want < %d", len(out), len(seed))
	}
	// The store should now hold the compacted messages.
	stored, _ := store.Load(sessionID)
	if len(stored) != len(out) {
		t.Errorf("stored length = %d, want %d (compacted)", len(stored), len(out))
	}
}

// TestCompactFactoryNilProviderReturnsNil verifies a nil provider CLOSURE disables compaction (the
// factory itself is nil). A non-nil closure returning nil is handled at call time.
func TestCompactFactoryNilProviderReturnsNil(t *testing.T) {
	factory := newCompactFactory(nil, core.NewMemoryStore())
	if factory != nil {
		t.Errorf("newCompactFactory with nil provider closure should return nil")
	}
}

func firstRole(msgs []core.Message) core.Role {
	if len(msgs) == 0 {
		return ""
	}
	return msgs[0].Role
}
