package tui

// layout_test.go holds the shared screen-dump helpers (rowText, dumpScreen) used across the TUI
// rendering tests.

import (
	"strings"
)

// rowText returns the decoded text of one screen row (trimmed of trailing spaces).
func rowText(sim *Screen, y int) string {
	cells, w, _ := sim.GetContents()
	var out []rune
	for x := 0; x < w; x++ {
		c := cells[y*w+x]
		if c.Main != 0 {
			out = append(out, c.Main)
		} else {
			out = append(out, ' ')
		}
	}
	s := string(out)
	for len(s) > 0 && s[len(s)-1] == ' ' {
		s = s[:len(s)-1]
	}
	return s
}

// dumpScreen returns the whole screen joined by newlines (for diagnosis on failure).
func dumpScreen(sim *Screen) string {
	_, _, h := sim.GetContents()
	var rows []string
	for y := 0; y < h; y++ {
		rows = append(rows, rowText(sim, y))
	}
	return strings.Join(rows, "\n")
}
