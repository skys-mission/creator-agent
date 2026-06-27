package tui

// input_render_test.go verifies the input→insert→render pipeline using the self-built Screen
// (no real terminal). Keys are injected directly into handleTcellEvent; the back buffer is then
// inspected via GetContents.

import (
	"testing"

	"github.com/skys-mission/creator-agent/core"
)

// newAppWithSim builds an App backed by a self-built Screen writing to a throwaway buffer (no real
// terminal). It returns the app and the screen for content inspection / key injection.
func newAppWithSim(t *testing.T, w, h int) (*App, *Screen) {
	t.Helper()
	sim := NewScreen(&bufWriter{}, w, h)
	a := &App{
		screen:   sim,
		width:    w,
		height:   h,
		atBottom: true,
		quitCh:   make(chan struct{}),
		events:   make(chan any, 64),
		rt: runtimeState{
			md: newMarkdownCache(),
		},
	}
	a.input.SetWidth(w - 4)
	return a, sim
}

// injectRune sends a KeyRune event through the normal dispatch path.
func injectRune(a *App, r rune) {
	handleTcellEvent(a, NewEventKey(KeyRune, r, ModNone))
}

// injectKey sends a non-rune key event (e.g. KeyEnter, KeyBackspace2).
func injectKey(a *App, k Key) {
	handleTcellEvent(a, NewEventKey(k, 0, ModNone))
}

// cellText reads a horizontal run of non-empty cells at row y (trimmed), for assertion. Wide (CJK)
// runes occupy their primary cell; the follow cell is a space, so we just collect each cell's main
// rune and trim trailing spaces.
func cellText(sim *Screen, y int) string {
	cells, w, _ := sim.GetContents()
	var out []rune
	for x := 0; x < w; x++ {
		c := cells[y*w+x]
		if c.Main == ' ' || c.Main == 0 {
			if len(out) > 0 {
				out = append(out, ' ')
			}
			continue
		}
		out = append(out, c.Main)
	}
	// trim trailing spaces
	s := string(out)
	for len(s) > 0 && s[len(s)-1] == ' ' {
		s = s[:len(s)-1]
	}
	return s
}

// screenDump returns the full decoded screen text (all rows joined by '\n'), for debugging.
func screenDump(sim *Screen) string {
	cells, w, h := sim.GetContents()
	var rows []string
	for y := 0; y < h; y++ {
		var out []rune
		for x := 0; x < w; x++ {
			c := cells[y*w+x]
			if c.Main == 0 {
				out = append(out, ' ')
			} else {
				out = append(out, c.Main)
			}
		}
		rows = append(rows, string(out))
	}
	return joinRows(rows)
}

func joinRows(rows []string) string {
	out := ""
	for i, r := range rows {
		if i > 0 {
			out += "\n"
		}
		out += r
	}
	return out
}

func TestInputRendersTypedText(t *testing.T) {
	a, sim := newAppWithSim(t, 80, 24)
	injectRune(a, 'h')
	injectRune(a, 'i')
	render(a)
	// The input box sits near the bottom; scan all rows for "hi".
	found := false
	_, h := sim.Size()
	for y := 0; y < h; y++ {
		if containsSubstring(cellText(sim, y), "hi") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("typed 'hi' not rendered in input box")
	}
}

func TestInputMultibyteRender(t *testing.T) {
	a, sim := newAppWithSim(t, 80, 24)
	for _, r := range "你好" {
		injectRune(a, r)
	}
	render(a)
	// Buffer should hold exactly 你好 (2 runes), no mojibake.
	if got := a.input.Value(); got != "你好" {
		t.Errorf("input buffer = %q, want 你好", got)
	}
	// And both CJK runes should appear on screen somewhere (each occupies 2 cells; the follow cell
	// is empty, so check each rune independently across the whole screen rather than the substring).
	foundNi, foundHao := false, false
	_, h := sim.Size()
	cells, w, _ := sim.GetContents()
	for i := 0; i < w*h; i++ {
		if cells[i].Main != ' ' && cells[i].Main != 0 {
			switch cells[i].Main {
			case '你':
				foundNi = true
			case '好':
				foundHao = true
			}
		}
	}
	if !foundNi || !foundHao {
		t.Errorf("CJK not rendered: 你=%v 好=%v\n--- screen dump ---\n%s", foundNi, foundHao, screenDump(sim))
	}
}

func TestInputBackspaceRemovesLastRune(t *testing.T) {
	a, _ := newAppWithSim(t, 80, 24)
	for _, r := range "abc" {
		injectRune(a, r)
	}
	injectKey(a, KeyBackspace2)
	if got := a.input.Value(); got != "ab" {
		t.Errorf("after backspace = %q, want ab", got)
	}
}

func TestInputCtrlWDeletesWord(t *testing.T) {
	a, _ := newAppWithSim(t, 80, 24)
	for _, r := range "hello world" {
		injectRune(a, r)
	}
	handleTcellEvent(a, NewEventKey(KeyCtrlW, 0, ModNone))
	if got := a.input.Value(); got != "hello " {
		t.Errorf("after Ctrl+W = %q, want 'hello '", got)
	}
}

func TestInputDoesNotSubmitOnEmptyEnter(t *testing.T) {
	a, _ := newAppWithSim(t, 80, 24)
	// No agent; Enter on empty input should be a no-op (no panic, no stream).
	injectKey(a, KeyEnter)
	if a.status != statusIdle {
		t.Errorf("empty Enter changed status to %v", a.status)
	}
}

// TestInputCursorRenderPosition verifies the rendered cursor lands immediately after the last
// typed rune, accounting for wide-char (CJK) display width. This is the regression guard for the
// "cursor points wrong" bug where the cursor column was computed in runes, not display columns.
func TestInputCursorRenderPosition(t *testing.T) {
	a, sim := newAppWithSim(t, 80, 24)
	for _, r := range "ab你" { // display widths: a=1, b=1, 你=2 -> cursor at col 4
		injectRune(a, r)
	}
	render(a)
	cx, cy, vis := sim.GetCursor()
	if !vis {
		t.Fatal("cursor not visible after typing")
	}
	// cursor x = promptX + promptW ("❯ " = 2) + displayWidth. promptX is the '❯' glyph column,
	// derived from the screen so the test tracks the layout, not a magic number.
	promptX := findInputPromptX(t, sim)
	promptW := strW("❯ ")
	const displayWidth = 4
	expectedX := promptX + promptW + displayWidth
	if cx != expectedX {
		t.Errorf("cursor x = %d, want %d (prompt x=%d + promptW %d + display-width %d)", cx, expectedX, promptX, promptW, displayWidth)
	}
	if cy < 0 || cy >= 24 {
		t.Errorf("cursor y = %d out of range", cy)
	}
}

// TestInputCursorRenderPositionAfterBackspace ensures the cursor moves back after deleting.
func TestInputCursorRenderPositionAfterBackspace(t *testing.T) {
	a, sim := newAppWithSim(t, 80, 24)
	for _, r := range "abc" {
		injectRune(a, r)
	}
	injectKey(a, KeyBackspace2) // delete 'c' -> cursor at col 2
	render(a)
	cx, _, vis := sim.GetCursor()
	if !vis {
		t.Fatal("cursor not visible")
	}
	promptX := findInputPromptX(t, sim)
	promptW := strW("❯ ")
	const displayWidth = 2 // "ab" after deleting 'c'
	expectedX := promptX + promptW + displayWidth
	if cx != expectedX {
		t.Errorf("cursor x after backspace = %d, want %d (prompt x=%d + promptW %d + display-width %d)", cx, expectedX, promptX, promptW, displayWidth)
	}
}

// findInputPromptX locates the input box's leading prompt glyph '❯' on the screen and returns its
// column. This is the precise anchor for the cursor: typed text starts at promptX + promptWidth
// ("❯ " = 2 cols), so cursor = promptX + promptWidth + typedDisplayWidth. Scans bottom-up so the
// (bottom-anchored) input box is found first. '❯' (U+276F) is unique to the Claude input box, so
// unlike a generic '-' it can't be confused with home-screen text.
func findInputPromptX(t *testing.T, sim *Screen) int {
	t.Helper()
	const promptGlyph = '❯'
	_, h := sim.Size()
	cells, w, _ := sim.GetContents()
	for y := h - 1; y >= 0; y-- {
		for x := 0; x < w; x++ {
			if cells[y*w+x].Main == promptGlyph {
				return x
			}
		}
	}
	t.Fatal("could not locate input prompt '❯' to anchor cursor assertion")
	return -1
}

func containsSubstring(haystack, needle string) bool {
	if len(needle) == 0 {
		return true
	}
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}

// Compile-time guard: core import is used for the stream type only in helpers above (kept for
// future extension); ensure the test compiles cleanly.
var _ = core.UserMessage
