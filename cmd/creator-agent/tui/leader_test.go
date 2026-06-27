package tui

// leader_test.go covers the opencode-style <ctrl+x> leader prefix: activation, cancel, unknown-key
// swallowing, and binding dispatch.

import "testing"

// TestLeaderSequence covers the core leader state machine: Ctrl+X activates, Esc cancels, an
// unknown key is swallowed (never leaks into the input), and a bound key dispatches its action.
func TestLeaderSequence(t *testing.T) {
	a, _ := newAppWithSim(t, 80, 24)

	// <leader>Esc cancels leader mode.
	handleTcellEvent(a, NewEventKey(KeyCtrlX, 0, ModNone))
	if !a.leaderActive {
		t.Fatalf("leader not active after Ctrl+X")
	}
	handleTcellEvent(a, NewEventKey(KeyEsc, 0, ModNone))
	if a.leaderActive {
		t.Fatalf("Esc should cancel leader mode")
	}

	// <leader> with an unknown key: swallowed, input untouched.
	handleTcellEvent(a, NewEventKey(KeyCtrlX, 0, ModNone))
	handleTcellEvent(a, NewEventKey(KeyRune, 'Z', ModNone))
	if a.leaderActive {
		t.Fatalf("leader should clear after an unknown key")
	}
	if a.input.Value() != "" {
		t.Fatalf("unknown leader key leaked into input: %q", a.input.Value())
	}

	// <leader>t opens the theme picker (themes are built-in, so this works without injected deps).
	handleTcellEvent(a, NewEventKey(KeyCtrlX, 0, ModNone))
	handleTcellEvent(a, NewEventKey(KeyRune, 't', ModNone))
	if a.leaderActive {
		t.Fatalf("leader should clear after a binding key")
	}
	if !a.themePicker.open {
		t.Fatalf("theme picker should open after <leader>t")
	}
}

// TestLeaderQuit verifies <leader>q quits.
func TestLeaderQuit(t *testing.T) {
	a, _ := newAppWithSim(t, 80, 24)
	handleTcellEvent(a, NewEventKey(KeyCtrlX, 0, ModNone))
	handleTcellEvent(a, NewEventKey(KeyRune, 'q', ModNone))
	if !a.quitting {
		t.Fatalf("<leader>q should quit")
	}
}

// TestLeaderAcceptsUppercase verifies the leader key is case-insensitive (<leader>T == <leader>t).
func TestLeaderAcceptsUppercase(t *testing.T) {
	a, _ := newAppWithSim(t, 80, 24)
	handleTcellEvent(a, NewEventKey(KeyCtrlX, 0, ModNone))
	handleTcellEvent(a, NewEventKey(KeyRune, 'T', ModNone))
	if !a.themePicker.open {
		t.Fatalf("<leader>T should open the theme picker (case-insensitive)")
	}
}
