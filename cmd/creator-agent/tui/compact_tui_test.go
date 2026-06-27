package tui

// compact_tui_test.go covers the TUI side of /compact: startCompact refuses without a compactor
// and while busy; applyCompactDone replaces the displayed messages on success and surfaces errors.

import (
	"context"
	"testing"
	"time"

	"github.com/skys-mission/creator-agent/core"
)

// TestStartCompactNoCompactor verifies /compact reports unavailable when no compactor is injected.
func TestStartCompactNoCompactor(t *testing.T) {
	a, _ := newAppWithSim(t, 80, 24)
	a.rt.sessionID = "ses"
	a.rt.compactor = nil
	startCompact(a)
	last := a.messages[len(a.messages)-1].content.String()
	if !contains(last, "unavailable") {
		t.Errorf("expected an unavailable message; got %q", last)
	}
}

// TestStartCompactRefusedWhileBusy verifies /compact is refused while streaming.
func TestStartCompactRefusedWhileBusy(t *testing.T) {
	a, _ := newAppWithSim(t, 80, 24)
	a.rt.sessionID = "ses"
	a.rt.compactor = func(_ context.Context, _ string) ([]core.Message, error) { return nil, nil }
	a.status = statusThinking
	startCompact(a)
	last := a.messages[len(a.messages)-1].content.String()
	if !contains(last, "Execution in progress") {
		t.Errorf("expected a refused message; got %q", last)
	}
}

// TestApplyCompactDoneReplacesMessages verifies applyCompactDone rebuilds the displayed blocks from
// the compacted history.
func TestApplyCompactDoneReplacesMessages(t *testing.T) {
	a, _ := newAppWithSim(t, 80, 24)
	a.rt.sessionID = "ses"
	a.messages = []msgBlock{{kind: kindUser}, {kind: kindAssistant}, {kind: kindUser}}
	compacted := []core.Message{
		core.UserMessage("[conversation summary so far]\nthe summary"),
		core.UserMessage("recent turn"),
	}
	applyCompactDone(a, compactDoneMsg{sessionID: "ses", rawOut: compacted})
	// 2 compacted user blocks + 1 system note ("History compacted…").
	if len(a.messages) != 3 {
		t.Errorf("displayed messages = %d, want 3 (2 compacted + 1 system note)", len(a.messages))
	}
	if a.status != statusIdle {
		t.Errorf("status should be idle after compact; got %v", a.status)
	}
}

// TestApplyCompactDoneError verifies an error is surfaced as a system note.
func TestApplyCompactDoneError(t *testing.T) {
	a, _ := newAppWithSim(t, 80, 24)
	a.rt.sessionID = "ses"
	a.messages = []msgBlock{{kind: kindUser}}
	applyCompactDone(a, compactDoneMsg{sessionID: "ses", err: errCompact("boom")})
	last := a.messages[len(a.messages)-1].content.String()
	if !contains(last, "Compact failed") {
		t.Errorf("expected a failure message; got %q", last)
	}
}

// TestStartCompactAsyncFlow verifies the full async path: startCompact sets statusCompacting, the
// compactor's result arrives via the sender, and applyCompactDone finalizes.
func TestStartCompactAsyncFlow(t *testing.T) {
	a, _ := newAppWithSim(t, 80, 24)
	a.rt.ctx = context.Background()
	a.rt.sessionID = "ses"
	a.messages = []msgBlock{{kind: kindUser}}
	a.rt.sender = func(msg any) {
		select {
		case a.events <- msg:
		default:
		}
	}
	// Use a slow compactor (small delay) so we can observe statusCompacting.
	done := make(chan struct{})
	a.rt.compactor = func(_ context.Context, _ string) ([]core.Message, error) {
		time.Sleep(20 * time.Millisecond)
		close(done)
		return []core.Message{core.UserMessage("compacted")}, nil
	}
	startCompact(a)
	if a.status != statusCompacting {
		t.Errorf("status should be statusCompacting during the async compact; got %v", a.status)
	}
	// Wait for the compactor to finish + drain the result.
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("compactor did not complete")
	}
	// Drain pending compactDoneMsg events.
	drained := false
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		select {
		case msg := <-a.events:
			if m, ok := msg.(compactDoneMsg); ok {
				applyCompactDone(a, m)
				drained = true
			}
		default:
			if drained {
				break
			}
			time.Sleep(2 * time.Millisecond)
		}
	}
	if a.status != statusIdle {
		t.Errorf("status should be idle after completion; got %v", a.status)
	}
	// 1 compacted user block + 1 system note ("History compacted…").
	if len(a.messages) != 2 {
		t.Errorf("displayed messages = %d, want 2 (1 compacted + 1 system note)", len(a.messages))
	}
}

// errCompact is a sentinel error for the error-path test.
type errCompact string

func (e errCompact) Error() string { return string(e) }

// contains is a local substring helper (avoid importing strings for one call).
func contains(s, sub string) bool {
	return len(s) >= len(sub) && indexOf(s, sub) >= 0
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
