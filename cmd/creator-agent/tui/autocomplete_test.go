package tui

// autocomplete_test.go covers the inline "/" live fuzzy autocomplete: auto-open on typing "/",
// live refine as the user types, close on whitespace/Escape, and commit on Enter/Tab (with the
// arg-taking trailing-space behavior).

import (
	"strings"
	"testing"
)

// TestSlashAutocompleteOpensOnSlash verifies typing "/" as the first char opens the menu with all
// commands (empty query).
func TestSlashAutocompleteOpensOnSlash(t *testing.T) {
	a, _ := newAppWithSim(t, 80, 24)
	if len(a.completions) != 0 {
		t.Fatalf("completions should start empty")
	}
	injectRune(a, '/')
	if len(a.completions) == 0 {
		t.Fatalf("typing '/' should open the autocomplete menu; got empty")
	}
	// Empty query returns all slash commands (the popup now pages if there are more than 10).
	if len(a.completions) == 0 {
		t.Fatalf("empty-query menu should contain commands; got empty")
	}
	got := strings.Join(a.completions, ",")
	for _, want := range []string{"/help", "/clear", "/cost"} {
		if !strings.Contains(got, want) {
			t.Errorf("empty-query menu missing %s; got %s", want, got)
		}
	}
}

// TestSlashAutocompleteDoesNotOpenForNonSlash verifies typing a normal char never opens the menu.
func TestSlashAutocompleteDoesNotOpenForNonSlash(t *testing.T) {
	a, _ := newAppWithSim(t, 80, 24)
	injectRune(a, 'h')
	injectRune(a, 'i')
	if len(a.completions) != 0 {
		t.Fatalf("non-slash input should not open the menu; got %v", a.completions)
	}
}

// TestSlashAutocompleteLiveRefine verifies the menu narrows as the user types, with the best name
// match ranked first.
func TestSlashAutocompleteLiveRefine(t *testing.T) {
	a, _ := newAppWithSim(t, 80, 24)
	injectRune(a, '/')
	injectRune(a, 'h')
	injectRune(a, 'e')
	if len(a.completions) == 0 {
		t.Fatalf("menu should have matches for '/he'")
	}
	// /help is a name-prefix match for "he" and should rank first.
	if a.completions[0] != "/help" {
		t.Fatalf("top match for '/he' = %q, want /help; got %v", a.completions[0], a.completions)
	}
	// Every shown entry must plausibly relate to "he" — /help present, /clear absent (clear has no
	// "he" subsequence in name or description).
	for _, c := range a.completions {
		if c == "/clear" {
			t.Errorf("'/he' should not match /clear; menu = %v", a.completions)
		}
	}
	// Selection resets to 0 on every refine.
	if a.compIdx != 0 {
		t.Errorf("compIdx after refine = %d, want 0", a.compIdx)
	}
}

// TestSlashAutocompleteClosesOnWhitespace verifies typing a space closes the menu (the user moved
// on to arguments).
func TestSlashAutocompleteClosesOnWhitespace(t *testing.T) {
	a, _ := newAppWithSim(t, 80, 24)
	injectRune(a, '/')
	injectRune(a, 'h')
	if len(a.completions) == 0 {
		t.Fatalf("menu should be open after '/h'")
	}
	injectRune(a, ' ')
	if len(a.completions) != 0 {
		t.Fatalf("space should close the menu; got %v", a.completions)
	}
	// The slash token plus space remain in the input.
	if got := a.input.Value(); got != "/h " {
		t.Errorf("input after space = %q, want '/h '", got)
	}
}

// TestSlashAutocompleteBackspaceRefines verifies backspacing re-refines (and closes when the slash
// is removed).
func TestSlashAutocompleteBackspaceRefines(t *testing.T) {
	a, _ := newAppWithSim(t, 80, 24)
	injectRune(a, '/')
	injectRune(a, 'h')
	injectRune(a, 'e')
	// Backspace to "/h" — menu still open.
	injectKey(a, KeyBackspace)
	if len(a.completions) == 0 {
		t.Fatalf("menu should remain open after backspace to '/h'")
	}
	// Backspace to "/" — menu still open (empty query, shows all).
	injectKey(a, KeyBackspace)
	if len(a.completions) == 0 {
		t.Fatalf("menu should be open for bare '/'")
	}
	// Backspace past "/" — input empty, menu closes.
	injectKey(a, KeyBackspace)
	if a.input.Value() != "" {
		t.Errorf("input should be empty after backspacing past '/'; got %q", a.input.Value())
	}
	if len(a.completions) != 0 {
		t.Errorf("menu should close when input no longer starts with '/'; got %v", a.completions)
	}
}

// TestSlashAutocompleteCommitNoArg verifies Enter commits a non-arg command (no trailing space)
// and closes the menu.
func TestSlashAutocompleteCommitNoArg(t *testing.T) {
	a, _ := newAppWithSim(t, 80, 24)
	injectRune(a, '/')
	injectRune(a, 'h')
	injectRune(a, 'e')
	// /help is the top match.
	if a.completions[0] != "/help" {
		t.Fatalf("precondition: top match = %q, want /help", a.completions[0])
	}
	injectKey(a, KeyEnter)
	if len(a.completions) != 0 {
		t.Errorf("Enter should close the menu; got %v", a.completions)
	}
	if got := a.input.Value(); got != "/help" {
		t.Errorf("input after commit = %q, want /help (no trailing space)", got)
	}
}

// TestSlashAutocompleteCommitArgCommandTrailingSpace verifies committing an arg-taking command
// (e.g. /diff) appends a trailing space so the user can type the argument.
func TestSlashAutocompleteCommitArgCommandTrailingSpace(t *testing.T) {
	a, _ := newAppWithSim(t, 80, 24)
	injectRune(a, '/')
	injectRune(a, 'd')
	injectRune(a, 'i')
	injectRune(a, 'f')
	injectRune(a, 'f')
	// Find /diff in the matches and select it.
	idx := -1
	for i, c := range a.completions {
		if c == "/diff" {
			idx = i
			break
		}
	}
	if idx < 0 {
		t.Fatalf("'/diff' filter should include /diff; got %v", a.completions)
	}
	for a.compIdx < idx {
		injectKey(a, KeyDown)
	}
	if a.completions[a.compIdx] != "/diff" {
		t.Fatalf("selection = %q, want /diff", a.completions[a.compIdx])
	}
	injectKey(a, KeyTab) // Tab also commits.
	if got := a.input.Value(); got != "/diff " {
		t.Errorf("arg-command commit should append trailing space; got %q, want '/diff '", got)
	}
	if len(a.completions) != 0 {
		t.Errorf("commit should close the menu; got %v", a.completions)
	}
}

// TestSlashAutocompleteEscapeCloses verifies Escape dismisses the menu without altering input.
func TestSlashAutocompleteEscapeCloses(t *testing.T) {
	a, _ := newAppWithSim(t, 80, 24)
	injectRune(a, '/')
	injectRune(a, 'h')
	injectKey(a, KeyEsc)
	// Esc at the completion level closes the menu. (The top-level Esc-quit only fires when the
	// menu is closed; handleCompletionKey intercepts Esc first.)
	if len(a.completions) != 0 {
		t.Errorf("Esc should close the menu; got %v", a.completions)
	}
	if got := a.input.Value(); got != "/h" {
		t.Errorf("Esc should leave input intact; got %q, want '/h'", got)
	}
}

// TestSlashAutocompleteNavigation verifies Up/Down move the selection within the open menu.
func TestSlashAutocompleteNavigation(t *testing.T) {
	a, _ := newAppWithSim(t, 80, 24)
	injectRune(a, '/') // all commands
	n := len(a.completions)
	if n < 2 {
		t.Fatalf("need >=2 completions to test navigation; got %d", n)
	}
	if a.compIdx != 0 {
		t.Fatalf("initial compIdx = %d, want 0", a.compIdx)
	}
	injectKey(a, KeyDown)
	if a.compIdx != 1 {
		t.Errorf("after Down, compIdx = %d, want 1", a.compIdx)
	}
	injectKey(a, KeyUp)
	if a.compIdx != 0 {
		t.Errorf("after Up, compIdx = %d, want 0", a.compIdx)
	}
}

// TestSlashAutocompleteDoesNotOpenMidWord verifies a "/" not at position 0 does not trigger the
// menu (opencode only triggers when "/" is the very first character).
func TestSlashAutocompleteDoesNotOpenMidWord(t *testing.T) {
	a, _ := newAppWithSim(t, 80, 24)
	injectRune(a, 'x')
	injectRune(a, '/') // not at position 0
	if len(a.completions) != 0 {
		t.Errorf("a non-leading '/' should not open the menu; got %v", a.completions)
	}
}

// TestMatchCommandsFuzzyUnit is a direct unit test of the matcher covering empty query, name
// prefix, description match, and no-match cases.
func TestMatchCommandsFuzzyUnit(t *testing.T) {
	// Empty query returns all slash commands.
	all := matchCommandsFuzzy("")
	if len(all) == 0 {
		t.Fatalf("empty query should return commands; got empty")
	}
	// Exact-ish name prefix.
	hp := matchCommandsFuzzy("/help")
	if len(hp) == 0 || hp[0] != "/help" {
		t.Errorf("'/help' should rank /help first; got %v", hp)
	}
	// No match returns empty.
	none := matchCommandsFuzzy("/zzzznotacommand")
	if len(none) != 0 {
		t.Errorf("'/zzzznotacommand' should match nothing; got %v", none)
	}
}

// TestSlashAutocompletePaging verifies PgUp/PgDn/Home/End navigate the paged completion menu
// and that the visible window (compScroll) follows the selection.
func TestSlashAutocompletePaging(t *testing.T) {
	a, _ := newAppWithSim(t, 80, 24)
	injectRune(a, '/') // all commands, no cap
	n := len(a.completions)
	if n <= compPageSize {
		t.Fatalf("need more than %d commands to test paging; got %d", compPageSize, n)
	}

	if a.compIdx != 0 || a.compScroll != 0 {
		t.Fatalf("initial selection/scroll should be 0; got idx=%d scroll=%d", a.compIdx, a.compScroll)
	}

	// Move selection down past the first page; scroll should follow.
	target := mini(n-1, compPageSize+2)
	for a.compIdx < target {
		injectKey(a, KeyDown)
	}
	if a.compScroll <= 0 {
		t.Fatalf("scroll should advance when selection leaves first page; got scroll=%d idx=%d", a.compScroll, a.compIdx)
	}

	// PgUp should move selection up by a page and scroll back.
	prevIdx := a.compIdx
	injectKey(a, KeyPgUp)
	if a.compIdx >= prevIdx {
		t.Fatalf("PgUp should decrease selection; prev=%d now=%d", prevIdx, a.compIdx)
	}

	// Home jumps to top.
	injectKey(a, KeyHome)
	if a.compIdx != 0 || a.compScroll != 0 {
		t.Fatalf("Home should reset to top; got idx=%d scroll=%d", a.compIdx, a.compScroll)
	}

	// End jumps to last item.
	injectKey(a, KeyEnd)
	if a.compIdx != n-1 {
		t.Fatalf("End should select last item; got idx=%d want %d", a.compIdx, n-1)
	}

	// Simulate the user's scenario: page down repeatedly to the bottom, then page/line up
	// repeatedly to the top. The first visible item (scroll) must return to 0 and stay updated.
	injectKey(a, KeyHome)
	for a.compIdx < n-1 {
		injectKey(a, KeyPgDn)
	}
	if a.compIdx != n-1 {
		t.Fatalf("paging down did not reach last item; got idx=%d want %d", a.compIdx, n-1)
	}
	// Now go back up: mix of PgUp and Up until we reach the top.
	for i := 0; i < 100 && a.compIdx > 0; i++ {
		if i%3 == 0 {
			injectKey(a, KeyPgUp)
		} else {
			injectKey(a, KeyUp)
		}
	}
	if a.compIdx != 0 {
		t.Fatalf("navigating up did not return to first item; got idx=%d", a.compIdx)
	}
	if a.compScroll != 0 {
		t.Fatalf("top visible item did not return to 0; got scroll=%d", a.compScroll)
	}
}

// TestShouldShowSlashCompletions covers the open/close predicate.
func TestShouldShowSlashCompletions(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"", false},
		{"hello", false},
		{"x/h", false},  // slash not at start
		{"/", true},     // bare slash opens (empty query)
		{"/he", true},   // typing command name
		{"/h e", false}, // whitespace -> closed (moved to args)
		{"/help ", false},
		{"/help\nx", false},
	}
	for _, tc := range cases {
		if got := shouldShowSlashCompletions(tc.in); got != tc.want {
			t.Errorf("shouldShowSlashCompletions(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}
