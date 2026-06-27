package tui

// layout_switch_test.go verifies the Claude Code layout's source-confirmed visuals: dot-led
// assistant turns, bare user text, and a top-line input box wrapped by horizontal rules.

import (
	"strings"
	"testing"
)

// TestClaudeLayoutAssistantHasDot verifies an assistant turn renders with a leading dot
// (⏺ on darwin, ● elsewhere).
func TestClaudeLayoutAssistantHasDot(t *testing.T) {
	a, sim := newAppWithSim(t, 80, 24)
	mb := msgBlock{kind: kindAssistant}
	mb.content.WriteString("Hello world")
	mb.finalize(a.rt.md)
	a.messages = append(a.messages, mb)
	render(a)
	screen := dumpScreen(sim)

	if !strings.Contains(screen, "Hello world") {
		t.Errorf("assistant content not rendered")
	}
	if !strings.Contains(screen, "⏺") && !strings.Contains(screen, "●") {
		t.Errorf("claude layout should render a leading dot (⏺ or ●); screen:\n%s", screen)
	}
}

// TestClaudeLayoutUserMessageIsBareText verifies user messages render as bare wrapped text.
func TestClaudeLayoutUserMessageIsBareText(t *testing.T) {
	a, sim := newAppWithSim(t, 80, 24)
	a.messages = append(a.messages, msgBlock{kind: kindUser})
	a.messages[0].content.WriteString("Fix the bug")
	render(a)
	screen := dumpScreen(sim)

	if !strings.Contains(screen, "Fix the bug") {
		t.Errorf("user content not rendered")
	}
}

// TestClaudeLayoutInputBoxTopLine verifies the input box paints a top horizontal rule (─) and a
// ❯ prompt, wrapped by a bottom rule.
func TestClaudeLayoutInputBoxTopLine(t *testing.T) {
	a, sim := newAppWithSim(t, 80, 24)
	render(a)
	screen := dumpScreen(sim)

	if strings.Contains(screen, "╭") || strings.Contains(screen, "╮") {
		t.Errorf("claude input box must not paint rounded corners; screen:\n%s", screen)
	}
	if !strings.Contains(screen, "❯") {
		t.Errorf("claude ❯ prompt missing")
	}
	// Verify two full-width horizontal rules wrap the textarea.
	cells, w, h := sim.GetContents()
	ruleRows := 0
	for y := 0; y < h; y++ {
		dashes := 0
		for x := 1; x < w-1; x++ {
			if cells[y*w+x].Main == '─' {
				dashes++
			}
		}
		if dashes >= w-4 { // a near-full-width run of dashes == a horizontal rule
			ruleRows++
		}
	}
	if ruleRows < 2 {
		t.Errorf("claude input box should paint two horizontal rules (top + bottom wrapping textarea); found %d; screen:\n%s", ruleRows, screen)
	}
}
