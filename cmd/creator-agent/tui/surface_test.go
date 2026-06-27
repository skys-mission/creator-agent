package tui

// surface_test.go verifies the core invariant of Surface: no write escapes its rectangle,
// especially wide (CJK/emoji) runes whose follow cell would otherwise spill into a neighbor.

import (
	"strings"
	"testing"
)

// newSimSurface builds a root Surface of size w×h backed by a self-built Screen writing to a
// throwaway buffer (no real terminal).
func newSimSurface(t *testing.T, w, h int) (*Surface, *Screen) {
	t.Helper()
	sim := NewScreen(&bufWriter{}, w, h)
	return newSurfaceSize(sim, w, h), sim
}

// syncSurface is now a no-op: the self-built Screen's GetContents reads the back buffer directly,
// so no flush is needed before inspecting contents. Kept for call-site compatibility.
func syncSurface(s *Surface) {}

// cellAt reads the primary rune at absolute (x,y) from the screen's back buffer.
func cellAt(sim *Screen, x, y int) rune {
	cells, w, _ := sim.GetContents()
	c := cells[y*w+x]
	if c.Main == 0 {
		return ' '
	}
	return c.Main
}

// TestWideRuneClippedAtRightEdge: a CJK rune (width 2) placed at the last column of a surface
// must NOT be written there, so its follow cell cannot leak into column W (which belongs to a
// neighbor). This is the precise root-cause regression.
func TestWideRuneClippedAtRightEdge(t *testing.T) {
	// Surface width 4. Place ASCII at cols 0,1,2 then a CJK rune at col 3 (last col).
	// The CJK rune needs cols 3,4 — col 4 is out of range, so it must be dropped.
	s, sim := newSimSurface(t, 4, 1)
	consumed := s.DrawPlain(0, 0, "abc你", StyleDefault)
	if consumed != 3 {
		t.Errorf("DrawPlain consumed %d cols, want 3 (CJK dropped at last col)", consumed)
	}
	syncSurface(s)
	// Cols 0-2 = abc, col 3 must be empty/space (the wide rune was NOT written).
	if r := cellAt(sim, 3, 0); r != 0 && r != ' ' {
		t.Errorf("col 3 = %q, want empty/space (wide rune must not spill)", r)
	}
}

// TestWideRuneFitsInMiddle: a CJK rune with two free cells before the edge IS written and occupies
// both cells.
func TestWideRuneFitsInMiddle(t *testing.T) {
	s, sim := newSimSurface(t, 4, 1)
	s.DrawPlain(0, 0, "a你b", StyleDefault)
	syncSurface(s)
	// 'a' at 0, '你' at 1-2 (primary at 1), 'b' at 3.
	if r := cellAt(sim, 0, 0); r != 'a' {
		t.Errorf("col 0 = %q, want 'a'", r)
	}
	if r := cellAt(sim, 1, 0); r != '你' {
		t.Errorf("col 1 = %q, want '你'", r)
	}
	if r := cellAt(sim, 3, 0); r != 'b' {
		t.Errorf("col 3 = %q, want 'b'", r)
	}
}

// TestSubSurfaceClipping: a child surface narrower than the parent must reject writes past its
// own right edge, even though the underlying screen has room.
func TestSubSurfaceClipping(t *testing.T) {
	root, sim := newSimSurface(t, 10, 1)
	// Child covering cols 2..6 (width 5).
	child := root.Sub(Rect{2, 0, 5, 1})
	// Write 10 'x' from local col 0 — only 5 should land (cols 2-6).
	child.RepeatPlain(0, 0, 'x', 10, StyleDefault)
	syncSurface(root)
	for x := 0; x < 10; x++ {
		r := cellAt(sim, x, 0)
		if x >= 2 && x < 7 {
			if r != 'x' {
				t.Errorf("col %d = %q, want 'x'", x, r)
			}
		} else {
			if r != 0 && r != ' ' {
				t.Errorf("col %d = %q, want empty (outside child rect)", x, r)
			}
		}
	}
}

// TestWideRuneClippedInSubSurface: a CJK rune at the child's last local column must not spill into
// the parent's area beyond the child.
func TestWideRuneClippedInSubSurface(t *testing.T) {
	root, sim := newSimSurface(t, 10, 1)
	child := root.Sub(Rect{2, 0, 3, 1}) // absolute cols 2,3,4
	// Fill cols 0,1 with ASCII, then CJK at local col 2 (last col) — must be dropped.
	child.DrawPlain(0, 0, "ab你", StyleDefault)
	syncSurface(root)
	// Absolute col 4 (child last col) and col 5 (parent area beyond child) must be empty.
	if r := cellAt(sim, 4, 0); r != 0 && r != ' ' {
		t.Errorf("child last col (abs 4) = %q, want empty (wide rune clipped)", r)
	}
	if r := cellAt(sim, 5, 0); r != 0 && r != ' ' {
		t.Errorf("parent col beyond child (abs 5) = %q, want empty", r)
	}
}

// TestVerticalClipping: writes outside the surface's row range are dropped.
func TestVerticalClipping(t *testing.T) {
	s, sim := newSimSurface(t, 4, 2)
	// Row 2 is out of range (height 2 -> rows 0,1).
	s.DrawPlain(0, 2, "abcd", StyleDefault)
	syncSurface(s)
	for x := 0; x < 4; x++ {
		if r := cellAt(sim, x, 0); r != 0 && r != ' ' {
			t.Errorf("row 0 col %d = %q, want empty", x, r)
		}
	}
}

// TestRepeatPlainOverflowPadsSpaces: RepeatPlain of a rune past the right edge pads with spaces
// instead of leaving the row half-drawn (keeps borders clean).
func TestRepeatPlainOverflowPadsSpaces(t *testing.T) {
	s, sim := newSimSurface(t, 3, 1)
	// Repeat '─' 5 times into a width-3 surface: cols 0,1,2 should all be '─' (fits), the extra
	// 2 repeats simply have no room.
	s.RepeatPlain(0, 0, '─', 5, StyleDefault)
	syncSurface(s)
	for x := 0; x < 3; x++ {
		if r := cellAt(sim, x, 0); r != '─' {
			t.Errorf("col %d = %q, want '─'", x, r)
		}
	}
}

// TestDrawTextStyledRunsConsumesDisplayWidth: DrawText returns columns consumed in display width,
// and a following run continues correctly after a wide rune.
func TestDrawTextStyledRunsConsumesDisplayWidth(t *testing.T) {
	s, sim := newSimSurface(t, 10, 1)
	var sl styledLine
	sl.appendRun("你", StyleDefault)
	sl.appendRun("好", StyleDefault)
	consumed := s.DrawText(0, 0, sl.runs)
	if consumed != 4 {
		t.Errorf("consumed %d, want 4 (two CJK runes)", consumed)
	}
	syncSurface(s)
	if r := cellAt(sim, 0, 0); r != '你' {
		t.Errorf("col 0 = %q, want '你'", r)
	}
	if r := cellAt(sim, 2, 0); r != '好' {
		t.Errorf("col 2 = %q, want '好'", r)
	}
}

// TestCompositeBgKeepsExplicitBg: styles with an explicit background keep it; styles without get
// the surface bg.
func TestCompositeBgKeepsExplicitBg(t *testing.T) {
	root, sim := newSimSurface(t, 2, 1)
	red := NewRGBColor(255, 0, 0)
	blue := NewRGBColor(0, 0, 255)
	explicit := StyleDefault.Foreground(ColorWhite).Background(red)
	redsurf := root.WithBg(blue)
	redsurf.DrawPlain(0, 0, "ab", explicit)
	cells, _, _ := sim.GetContents()
	_, bg0, _ := cells[0].Style.Decompose()
	if bg0 != red {
		t.Errorf("explicit bg overridden: got %v, want red", bg0)
	}
	// Now a style with no bg should get Blue.
	plain := StyleDefault.Foreground(ColorWhite)
	redsurf.DrawPlain(0, 0, "ab", plain)
	cells, _, _ = sim.GetContents()
	_, bg1, _ := cells[0].Style.Decompose()
	if bg1 != blue {
		t.Errorf("plain bg not composited: got %v, want blue", bg1)
	}
}

// TestZeroWidthRuneSkipped: a combining mark (zero width) is skipped so it does not corrupt the
// cell it would otherwise overwrite as a primary rune. The preceding and following normal runes
// remain at their expected columns.
func TestZeroWidthRuneSkipped(t *testing.T) {
	s, sim := newSimSurface(t, 5, 1)
	// 'e' + combining acute (U+0301, zero width) + 'f'.
	s.DrawPlain(0, 0, "e\u0301f", StyleDefault)
	syncSurface(s)
	// col 0 = 'e', col 1 = 'f' (the combining mark was skipped, so 'f' advances by 1 only).
	if r := cellAt(sim, 0, 0); r != 'e' {
		t.Errorf("col 0 = %q, want 'e'", r)
	}
	if r := cellAt(sim, 1, 0); r != 'f' {
		t.Errorf("col 1 = %q, want 'f' (combining skipped)", r)
	}
}

// TestFillRectLocalClipped: FillRectLocal only fills within the surface.
func TestFillRectLocalClipped(t *testing.T) {
	root, sim := newSimSurface(t, 5, 2)
	child := root.Sub(Rect{1, 0, 3, 2}) // abs cols 1-3, rows 0-1
	child.FillRectLocal(Rect{0, 0, 10, 10}, '#', StyleDefault)
	syncSurface(root)
	for y := 0; y < 2; y++ {
		for x := 0; x < 5; x++ {
			r := cellAt(sim, x, y)
			if x >= 1 && x <= 3 {
				if r != '#' {
					t.Errorf("(%d,%d)=%q want '#'", x, y, r)
				}
			} else {
				if r != 0 && r != ' ' {
					t.Errorf("(%d,%d)=%q want empty (outside child)", x, y, r)
				}
			}
		}
	}
}

// TestLongASCIIFitsAndClips: a long ASCII string is fully written up to the edge then stops.
func TestLongASCIIFitsAndClips(t *testing.T) {
	s, sim := newSimSurface(t, 3, 1)
	s.DrawPlain(0, 0, "abcdef", StyleDefault)
	syncSurface(s)
	if r := cellAt(sim, 0, 0); r != 'a' {
		t.Errorf("col 0 = %q", r)
	}
	if r := cellAt(sim, 2, 0); r != 'c' {
		t.Errorf("col 2 = %q, want 'c'", r)
	}
}

// TestRunewidthParityWithOld: the new runewidth wrapper agrees with the old hand-rolled width for
// the common cases used in the codebase.
func TestRunewidthParityWithOld(t *testing.T) {
	cases := []struct {
		r    rune
		want int
	}{
		{'a', 1}, {'你', 2}, {'好', 2}, {'─', 1}, {' ', 1}, {'・', 2},
	}
	for _, c := range cases {
		if got := runeW(c.r); got != c.want {
			t.Errorf("runeW(%q)=%d want %d", c.r, got, c.want)
		}
	}
	if got := strW("ab你好"); got != 6 {
		t.Errorf("strW(\"ab你好\")=%d want 6", got)
	}
}

// smoke helper used by other test files potentially; keep a no-op import guard.
var _ = strings.TrimSpace
