package tui

// completions_test.go covers the inline command hints ("命令行提示"): the slash-command suggestion
// menu, its filtering, Tab completion, Enter semantics, Esc dismissal, and its rendering.

import (
	"strings"
	"testing"

	"github.com/skys-mission/creator-agent/cmd/creator-agent/tui/i18n"
)

func TestSlashHintOpensOnLoneSlash(t *testing.T) {
	a, _ := newAppWithSim(t, 80, 24)
	injectRune(a, '/')
	if len(a.completions) == 0 {
		t.Fatal("typing '/' should open the command hint menu")
	}
	if got := a.completions[0]; got != "/model" {
		t.Fatalf("hint menu = %v, want /model-new first", a.completions)
	}
}

func TestSlashHintFiltersByQuery(t *testing.T) {
	a, _ := newAppWithSim(t, 80, 24)
	typeText(a, "/mod")
	if len(a.completions) != 1 || a.completions[0] != "/model" {
		t.Fatalf("hint menu for '/mod' = %v, want [/model-new]", a.completions)
	}
	// Once the query gains whitespace the user is typing arguments: the menu closes.
	typeText(a, " x")
	if a.completions != nil {
		t.Fatalf("hint menu should close after whitespace; got %v", a.completions)
	}
}

func TestTabCompletesSingleCandidate(t *testing.T) {
	a, _ := newAppWithSim(t, 80, 24)
	typeText(a, "/mod")
	injectKey(a, KeyTab)
	if got := a.input.Value(); got != "/model" {
		t.Fatalf("Tab completed input = %q, want /model-new", got)
	}
	if a.completions != nil {
		t.Fatalf("menu should close after accepting; got %v", a.completions)
	}
}

func TestEnterAcceptsThenRuns(t *testing.T) {
	a, _ := newAppWithSim(t, 80, 24)
	typeText(a, "/mod")
	injectKey(a, KeyEnter) // partial query -> accept the highlight
	if got := a.input.Value(); got != "/model" {
		t.Fatalf("Enter should accept the completion; input = %q", got)
	}
	if a.modelMenu.open {
		t.Fatal("Enter on a partial query must not run the command yet")
	}
	injectKey(a, KeyEnter) // now exact -> run
	if !a.modelMenu.open {
		t.Fatal("second Enter should run /model")
	}
}

func TestEnterRunsExactCommandImmediately(t *testing.T) {
	a, _ := newAppWithSim(t, 80, 24)
	typeText(a, "/model")
	if a.completions == nil {
		t.Fatal("hint menu should be open while typing a command")
	}
	injectKey(a, KeyEnter) // fully typed exact command -> run now, no second Enter
	if !a.modelMenu.open {
		t.Fatal("Enter on an exact command should run it immediately")
	}
}

func TestEscClosesHintMenuKeepsDraft(t *testing.T) {
	a, _ := newAppWithSim(t, 80, 24)
	typeText(a, "/mod")
	injectKey(a, KeyEsc)
	if a.completions != nil {
		t.Fatal("Esc should close the hint menu")
	}
	if got := a.input.Value(); got != "/mod" {
		t.Fatalf("Esc should keep the typed query; input = %q", got)
	}
}

func TestHintMenuRendersAboveInputBox(t *testing.T) {
	a, sim := newAppWithSim(t, 80, 24)
	typeText(a, "/")
	render(a)
	dump := screenDump(sim)
	if !strings.Contains(dump, "> /model") {
		t.Fatalf("hint menu row not rendered:\n%s", dump)
	}
	if !strings.Contains(dump, i18n.T("cmd.desc./model")) {
		t.Fatalf("hint menu description not rendered:\n%s", dump)
	}
}

func TestQuitKeysWorkWhileHintMenuOpen(t *testing.T) {
	a, _ := newAppWithSim(t, 80, 24)
	typeText(a, "/")
	injectKey(a, KeyCtrlD) // the menu must never trap the exit
	if !a.quitting {
		t.Fatal("Ctrl+D should quit even with the hint menu open")
	}
}
