package tui

// emoji_claude_bg_test.go verifies the fix for the emoji-driven leftward drift in the Claude layout:
// the message area (and the input/approval blocks) must stay on the terminal's default background
// rather than painting a row-wide colored fill. A painted fill would expose the divergence between
// our runewidth model and the terminal's actual emoji advance as a visible shift on every emoji line.
// Leaving the background transparent makes the drift invisible, matching Claude Code's behavior.

import (
	"strings"
	"testing"
)

// TestClaudeEmojiRowsTransparentBg renders an emoji user message in the claude layout and asserts
// the message-area cells carry ColorDefault (transparent) background. This is the core drift fix:
// with no painted background there is nothing to reveal an emoji width desync.
func TestClaudeEmojiRowsTransparentBg(t *testing.T) {
	a, sim := newAppWithSim(t, 80, 24)
	a.messages = append(a.messages, msgBlock{kind: kindUser})
	a.messages[0].content.WriteString("🎉✨😊🤓🔥💡🎯🍣")
	render(a)
	screen := dumpScreen(sim)
	t.Logf("80x24 CLAUDE EMOJI SESSION:\n%s", screen)

	if !strings.Contains(screen, "🎉") {
		t.Fatalf("emoji not rendered at all:\n%s", screen)
	}

	cells, w, _ := sim.GetContents()
	// The user message text sits in the main area (no painted bg). Find emoji rows and verify every
	// cell on that row has a default (transparent) background.
	for y := 0; y < 24; y++ {
		row := rowText(sim, y)
		if !strings.Contains(row, "🎉") {
			continue
		}
		for x := 0; x < w; x++ {
			c := cells[y*w+x]
			_, bg, _ := c.Style.Decompose()
			if !bg.IsDefault() {
				t.Errorf("row %d col %d: claude message cell has painted bg %v (should be transparent); drift would be visible",
					y, x, bg)
			}
		}
	}
}

// TestClaudeInputBoxTransparentBg asserts the prompt input box rules are drawn but its interior has
// no painted background (only the rule glyphs use foreground color). Matches Claude Code exactly.
func TestClaudeInputBoxTransparentBg(t *testing.T) {
	a, sim := newAppWithSim(t, 80, 24)
	render(a)
	cells, w, h := sim.GetContents()

	// Find the top horizontal rule row of the input box: a near-full-width run of '─'.
	borderY := -1
	for y := h - 7; y < h; y++ {
		if y < 0 {
			continue
		}
		dashes := 0
		for x := 0; x < w; x++ {
			if cells[y*w+x].Main == '─' {
				dashes++
			}
		}
		if dashes >= w-4 {
			borderY = y
			break
		}
	}
	if borderY < 0 {
		t.Fatalf("input box top horizontal rule not found:\n%s", dumpScreen(sim))
	}
	// The interior rows (below the top rule, within the input box) must be transparent.
	for y := borderY + 1; y < h; y++ {
		for x := 0; x < w; x++ {
			c := cells[y*w+x]
			_, bg, _ := c.Style.Decompose()
			if !bg.IsDefault() {
				t.Errorf("input box interior row %d col %d has painted bg %v (should be transparent)",
					y, x, bg)
			}
		}
	}
}

// TestClaudeEmitsNoBackgroundSGR renders an emoji session in the claude layout and checks the raw
// output bytes contain no 48;2;r;g;b background-color SGR. This is the byte-level guarantee that the
// terminal never receives an instruction to paint a background that could expose drift.
func TestClaudeEmitsNoBackgroundSGR(t *testing.T) {
	bw := &bufWriter{}
	a := &App{
		screen: NewScreen(bw, 80, 24),
		width:  80, height: 24, atBottom: true,
		quitCh: make(chan struct{}),
		events: make(chan any, 64),
		rt:     runtimeState{md: newMarkdownCache()},
	}
	a.input.SetWidth(76)
	a.messages = append(a.messages, msgBlock{kind: kindUser})
	a.messages[0].content.WriteString("🎉✨😊🤓🔥💡🎯🍣")
	render(a)
	out := bw.buf.String()
	if strings.Contains(out, "48;2;") {
		t.Errorf("claude output contains a background SGR (48;2;...); expected fully transparent bg.\noutput: %q", out)
	}
}
