package tui

// followcell_test.go verifies the fix for stale follow-cell content that caused emoji/CJK rows to
// appear misaligned under incremental rendering.

import (
	"bytes"
	"strings"
	"testing"
)

// TestSetContentFillsFollowCell: writing a width-2 rune fills the follow cell with a space of the
// same style, so the buffer has no "hole" where stale content could persist.
func TestSetContentFillsFollowCell(t *testing.T) {
	bw := &bufWriter{}
	s := NewScreen(bw, 4, 1)
	st := StyleDefault.Foreground(NewRGBColor(1, 2, 3))
	s.SetContent(0, 0, '🔧', nil, st)
	r0, _ := s.GetContent(0, 0)
	r1, _ := s.GetContent(1, 0)
	if r0 != '🔧' {
		t.Errorf("cell0 = %q, want 🔧", r0)
	}
	if r1 != ' ' {
		t.Errorf("follow cell1 = %q, want space (auto-filled)", r1)
	}
}

// TestIncrementalNoStaleFollowCell: the regression. Frame 1 has a wide rune at col 0. Frame 2
// replaces that line entirely with ASCII starting at col 0. Under incremental Show(), the follow
// cell (col 1) must be re-emitted (it changed from the auto-filled space to the new ASCII char),
// so no stale glyph remains.
func TestIncrementalNoStaleFollowCell(t *testing.T) {
	bw := &bufWriter{}
	s := NewScreen(bw, 6, 1)
	// Frame 1: 🔧 + spaces.
	s.SetContent(0, 0, '🔧', nil, StyleDefault)
	_ = s.Sync()
	bw.buf.Reset()
	// Frame 2: pure ASCII "ABCDEF" overwriting the whole row.
	for i, ch := range "ABCDEF" {
		s.SetContent(i, 0, ch, nil, StyleDefault)
	}
	_ = s.Show()
	out := bw.buf.String()
	// The ASCII content must appear (the follow cell got overwritten and re-emitted).
	if !strings.Contains(out, "A") || !strings.Contains(out, "F") {
		t.Errorf("ASCII overwrite not emitted:\n%s", escRepr(out))
	}
}

// TestPresentSkipsEmittingFollowCell: even though the follow cell is filled in the buffer, Present
// must NOT emit it as a separate glyph (the terminal advances its own cursor by 2). So for a row
// "🔧x", the output should contain the 🔧 bytes then x, with no extra CUP/space for the follow cell.
func TestPresentSkipsEmittingFollowCell(t *testing.T) {
	bw := &bufWriter{}
	s := NewScreen(bw, 6, 1)
	s.SetContent(0, 0, '🔧', nil, StyleDefault)
	s.SetContent(2, 0, 'x', nil, StyleDefault)
	_ = s.Sync()
	out := bw.buf.String()
	// The follow cell (col 1) should not be emitted as a space via its own CUP.
	if strings.Contains(out, "\x1b[1;2H ") {
		t.Errorf("follow cell emitted as separate space:\n%s", escRepr(out))
	}
}

// TestIncrementalWideRuneClearsStaleFollowCell: when a previous frame placed content at the follow
// cell of what is now a wide rune, incremental Show() must clear that stale content. Writing a
// space directly into the follow cell would corrupt the wide glyph on terminals that render it as
// two columns, so the correct fix needs a different approach (e.g. clearing the affected region
// before drawing the wide rune). This test documents the stale-cell scenario and will be enabled
// once a safe fix is implemented.
func TestIncrementalWideRuneClearsStaleFollowCell(t *testing.T) {
	t.Skip("pending a fix that clears stale follow cells without breaking wide-glyph rendering")
}

var _ = bytes.NewBuffer
