package tui

// thinking_test.go covers the three-state reasoning display (compact -> expanded -> hidden).

import "testing"

// TestThinkingModeCycle verifies cycleThinkingMode rotates compact -> expanded -> hidden -> compact.
func TestThinkingModeCycle(t *testing.T) {
	a, _ := newAppWithSim(t, 80, 24)
	a.current = &msgBlock{kind: kindAssistant}
	a.current.thinking.WriteString("some reasoning")

	if a.current.thinkingMode != thinkingCompact {
		t.Fatalf("default mode = %d, want thinkingCompact(%d)", a.current.thinkingMode, thinkingCompact)
	}
	cycleThinkingMode(a)
	if a.current.thinkingMode != thinkingExpanded {
		t.Fatalf("after 1 cycle = %d, want thinkingExpanded(%d)", a.current.thinkingMode, thinkingExpanded)
	}
	cycleThinkingMode(a)
	if a.current.thinkingMode != thinkingHidden {
		t.Fatalf("after 2 cycles = %d, want thinkingHidden(%d)", a.current.thinkingMode, thinkingHidden)
	}
	cycleThinkingMode(a)
	if a.current.thinkingMode != thinkingCompact {
		t.Fatalf("after 3 cycles = %d, want thinkingCompact(%d)", a.current.thinkingMode, thinkingCompact)
	}
}

// TestCtrlTCycle verifies Ctrl+T drives the same cycle.
func TestCtrlTCycle(t *testing.T) {
	a, _ := newAppWithSim(t, 80, 24)
	a.current = &msgBlock{kind: kindAssistant}
	a.current.thinking.WriteString("reasoning")
	handleTcellEvent(a, NewEventKey(KeyCtrlT, 0, ModNone))
	if a.current.thinkingMode != thinkingExpanded {
		t.Fatalf("Ctrl+T should move compact->expanded; got %d", a.current.thinkingMode)
	}
}
