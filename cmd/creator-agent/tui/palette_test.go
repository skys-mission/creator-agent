package tui

// palette_test.go covers the Ctrl+P command palette: open/close, fuzzy filtering, execution, and
// the arg-fillback behavior for arg-taking commands. State is asserted on the App directly (the
// test harness drives handleTcellEvent without invoking render).

import (
	"strings"
	"testing"
)

// injectCtrlP sends a Ctrl+P key event through the normal dispatch path.
func injectCtrlP(a *App) {
	handleTcellEvent(a, NewEventKey(KeyCtrlP, 0, ModNone))
}

// findPaletteEntry returns the paletteItem with the given name, or false.
func findPaletteEntry(entries []paletteItem, name string) (paletteItem, bool) {
	for _, e := range entries {
		if e.name == name {
			return e, true
		}
	}
	return paletteItem{}, false
}

// TestPaletteOpenClose verifies Ctrl+P opens the palette and Esc closes it.
func TestPaletteOpenClose(t *testing.T) {
	a, _ := newAppWithSim(t, 80, 24)

	if a.palette.open {
		t.Fatalf("palette should be closed initially")
	}

	injectCtrlP(a)
	if !a.palette.open {
		t.Fatalf("Ctrl+P should open the palette")
	}

	// Empty-query open: all registry commands + direct-action items (themes) are listed.
	want := len(commandRegistry) + len(SupportedThemes())
	if got := len(a.palette.entries); got != want {
		t.Fatalf("empty-query entry count = %d, want %d (commands + theme actions)", got, want)
	}

	// Esc closes.
	injectKey(a, KeyEsc)
	if a.palette.open {
		t.Fatalf("Esc should close the palette")
	}
}

// TestPaletteCtrlPToggle verifies that pressing Ctrl+P while the palette is open closes it.
func TestPaletteCtrlPToggle(t *testing.T) {
	a, _ := newAppWithSim(t, 80, 24)

	injectCtrlP(a)
	if !a.palette.open {
		t.Fatalf("Ctrl+P should open the palette")
	}

	injectCtrlP(a)
	if a.palette.open {
		t.Fatalf("second Ctrl+P should close the palette (toggle)")
	}
}

// TestPaletteFilter verifies the fuzzy filter narrows the list and ranks name matches highly.
func TestPaletteFilter(t *testing.T) {
	a, _ := newAppWithSim(t, 80, 24)
	injectCtrlP(a)

	// Type "he" -> should match /help (name prefix) and possibly others by description, but /help
	// must be present and rank at or near the top.
	injectRune(a, 'h')
	injectRune(a, 'e')

	if _, ok := findPaletteEntry(a.palette.entries, "/help"); !ok {
		t.Fatalf("/help should be in filtered entries for query 'he'; got %v", entryNames(a.palette.entries))
	}
	// /help must rank near the top (name match); exact #1 can vary when other entries'
	// descriptions also contain the query substring after localization.
	foundAt := -1
	for i, e := range a.palette.entries {
		if e.name == "/help" {
			foundAt = i
			break
		}
	}
	if foundAt < 0 || foundAt > 2 {
		t.Fatalf("/help should be in top 3 for 'he'; got position %d in %v", foundAt, entryNames(a.palette.entries))
	}
	// Selection must be reset to 0 after a re-filter.
	if a.palette.selIdx != 0 {
		t.Fatalf("selIdx after filter = %d, want 0", a.palette.selIdx)
	}

	// Backspace clears one char; "h" still matches /help.
	injectKey(a, KeyBackspace)
	if _, ok := findPaletteEntry(a.palette.entries, "/help"); !ok {
		t.Fatalf("/help should still match for query 'h'; got %v", entryNames(a.palette.entries))
	}
}

// TestPaletteExecute verifies Enter runs the selected no-arg command.
func TestPaletteExecute(t *testing.T) {
	a, _ := newAppWithSim(t, 80, 24)
	// Pre-populate toolInfos so /tools has deterministic output; empty list -> "No available tools".
	a.rt.toolInfos = nil

	injectCtrlP(a)

	// Move the selection to /tools. The registry order is the default empty-query order, so walk
	// down until the selected entry is /tools.
	target := "/tools"
	for a.palette.selIdx < len(a.palette.entries)-1 {
		if a.palette.entries[a.palette.selIdx].name == target {
			break
		}
		injectKey(a, KeyDown)
	}
	if a.palette.entries[a.palette.selIdx].name != target {
		t.Fatalf("could not select %s; landed on %q", target, a.palette.entries[a.palette.selIdx].name)
	}

	injectKey(a, KeyEnter)

	// /tools with empty toolInfos appends a system message "No available tools".
	last := a.messages[len(a.messages)-1]
	if !strings.Contains(last.content.String(), "No available tools") {
		t.Fatalf("Enter on /tools should run it; last message = %q", last.content.String())
	}
	// The palette must be closed after committing.
	if a.palette.open {
		t.Fatalf("palette should close after commit")
	}
}

// TestPaletteArgFillback verifies that selecting an arg-taking command (/diff) fills "/diff " into
// the main input instead of running it, so the user can finish typing the argument.
func TestPaletteArgFillback(t *testing.T) {
	a, _ := newAppWithSim(t, 80, 24)

	injectCtrlP(a)
	// Filter down to /diff.
	injectRune(a, 'd')
	injectRune(a, 'i')
	injectRune(a, 'f')
	injectRune(a, 'f')
	if _, ok := findPaletteEntry(a.palette.entries, "/diff"); !ok {
		t.Fatalf("/diff should be in filtered entries for 'diff'; got %v", entryNames(a.palette.entries))
	}
	if a.palette.entries[0].name != "/diff" {
		t.Fatalf("top entry for 'diff' = %q, want /diff", a.palette.entries[0].name)
	}

	injectKey(a, KeyEnter)

	if a.palette.open {
		t.Fatalf("palette should close after selecting an arg-taking command")
	}
	if got, want := a.input.Value(), "/diff "; got != want {
		t.Fatalf("main input after /diff selection = %q, want %q", got, want)
	}
	// No message should have been added (the command was not executed).
	if len(a.messages) != 0 {
		t.Fatalf("arg-taking command should not execute; got messages: %v", a.messages)
	}
}

// TestCommandRegistryEquivalence guards the registry refactor: the derived slashCommands list must
// still cover the full pre-refactor command set with no duplicates, and findCommand must resolve
// each. This catches accidental drift between the registry and its consumers.
func TestCommandRegistryEquivalence(t *testing.T) {
	want := map[string]bool{
		"/help": true, "/clear": true, "/copy": true, "/compact": true, "/cost": true,
		"/model": true, "/models": true, "/mcps": true, "/agents": true, "/variants": true,
		"/mode": true, "/modes": true,
		"/changes": true, "/diff": true,
		"/resume": true, "/tools": true, "/themes": true,
		"/rename": true, "/pin": true, "/new": true, "/sessions": true,
		"/exit": true, "/quit": true,
	}

	seen := make(map[string]int, len(getSlashCommands()))
	for _, name := range getSlashCommands() {
		seen[name]++
		if !want[name] {
			t.Errorf("unexpected command in registry: %q", name)
		}
		if findCommand(name) == nil {
			t.Errorf("findCommand(%q) returned nil", name)
		}
	}
	for name := range want {
		if seen[name] == 0 {
			t.Errorf("missing command from registry: %q", name)
		}
		if seen[name] > 1 {
			t.Errorf("duplicate command in registry: %q appears %d times", name, seen[name])
		}
	}
	// Unknown command resolves to nil and an empty description.
	if findCommand("/nope") != nil {
		t.Errorf("findCommand(/nope) should be nil")
	}
	if commandDesc("/nope") != "" {
		t.Errorf("commandDesc(/nope) should be empty")
	}
	// A known command has a non-empty description.
	if commandDesc("/help") == "" {
		t.Errorf("commandDesc(/help) should be non-empty")
	}
}

// entryNames returns the names of a slice of paletteItem, for assertion error messages.
func entryNames(items []paletteItem) []string {
	out := make([]string, 0, len(items))
	for _, it := range items {
		out = append(out, it.name)
	}
	return out
}
