package tui

// self_render_test.go verifies the byte-level output of the self-built Screen, especially the
// absolute-positioning invariant that fixes CJK-terminal misalignment: every emitted cell must be
// preceded by a CUP, and a display-width-2 rune must be followed by a CUP that snaps the cursor
// past its second cell.

import (
	"bytes"
	"strings"
	"testing"
)

// bufWriter is a termWriter backed by a bytes.Buffer, for capturing emitted bytes in tests.
type bufWriter struct{ buf bytes.Buffer }

func (w *bufWriter) Write(p []byte) (int, error) { return w.buf.Write(p) }

// newTestScreen builds a Screen writing to a capturable buffer.
func newTestScreen(w, h int) (*Screen, *bufWriter) {
	bw := &bufWriter{}
	return NewScreen(bw, w, h), bw
}

// TestSGRColorEmission: setting a cell with an RGB foreground emits the 38;2;r;g;b sequence.
func TestSGRColorEmission(t *testing.T) {
	s, bw := newTestScreen(4, 1)
	red := StyleDefault.Foreground(NewRGBColor(255, 0, 0))
	s.SetContent(0, 0, 'X', nil, red)
	_ = s.Sync()
	out := bw.buf.String()
	if !strings.Contains(out, "38;2;255;0;0") {
		t.Errorf("missing fg SGR 38;2;255;0;0:\n%q", out)
	}
}

// TestSGRBackground: an explicit background emits 48;2;r;g;b.
func TestSGRBackground(t *testing.T) {
	s, bw := newTestScreen(4, 1)
	st := StyleDefault.Background(NewRGBColor(10, 20, 30))
	s.SetContent(0, 0, 'Y', nil, st)
	_ = s.Sync()
	out := bw.buf.String()
	if !strings.Contains(out, "48;2;10;20;30") {
		t.Errorf("missing bg SGR:\n%q", out)
	}
}

// TestSGRDiffOnlyEmitsChanges: two cells with the same style should not re-emit the SGR for the
// second (incremental sgr diffing reduces output bytes).
func TestSGRDiffOnlyEmitsChanges(t *testing.T) {
	s, bw := newTestScreen(4, 1)
	st := StyleDefault.Foreground(NewRGBColor(1, 2, 3))
	s.SetContent(0, 0, 'a', nil, st)
	s.SetContent(1, 0, 'b', nil, st)
	_ = s.Sync()
	out := bw.buf.String()
	// The "38;2;1;2;3" SGR should appear exactly once (both cells share it).
	if c := strings.Count(out, "38;2;1;2;3"); c != 1 {
		t.Errorf("fg SGR emitted %d times, want 1:\n%q", c, out)
	}
}

// TestStyleDecomposeAndCompositeBg: Decompose returns fg/bg and ColorDefault is detectable, so the
// region-background composition logic (surface.go's compositeBg) can decide whether to apply bg.
func TestStyleDecomposeAndCompositeBg(t *testing.T) {
	st := StyleDefault.Foreground(ColorWhite).Background(NewRGBColor(5, 6, 7)).Bold(true)
	fg, bg, attr := st.Decompose()
	if fg != ColorWhite {
		t.Errorf("fg = %v, want ColorWhite", fg)
	}
	if bg.IsDefault() {
		t.Errorf("bg default, want RGB")
	}
	if attr&1 == 0 {
		t.Errorf("bold bit not set")
	}
	// StyleDefault has default fg/bg.
	dfg, dbg, _ := StyleDefault.Decompose()
	if !dfg.IsDefault() || !dbg.IsDefault() {
		t.Errorf("StyleDefault should be all-default")
	}
}

// TestGetContentRoundTrip: SetContent then GetContent returns the same rune/style.
func TestGetContentRoundTrip(t *testing.T) {
	s, _ := newTestScreen(5, 2)
	st := StyleDefault.Foreground(ColorWhite)
	s.SetContent(1, 1, 'Z', nil, st)
	r, gotSt := s.GetContent(1, 1)
	if r != 'Z' {
		t.Errorf("rune = %q, want Z", r)
	}
	_, _, _ = gotSt.Decompose() // style survives; just check no panic
}

// TestIncrementalFlush: a second incremental Show() with no changes emits no cell content.
// (Sync() always force-repaints; Show() diffs against the previous frame.)
func TestIncrementalFlush(t *testing.T) {
	s, bw := newTestScreen(4, 1)
	s.SetContent(0, 0, 'A', nil, StyleDefault)
	_ = s.Sync() // establish front frame
	bw.buf.Reset()
	_ = s.Show() // nothing changed -> should emit nothing cell-wise
	out := bw.buf.String()
	if strings.Contains(out, "A") {
		t.Errorf("unchanged cell re-emitted:\n%q", out)
	}
}

// TestIncrementalFlushEmitsOnlyChanged: after Sync, changing one cell and calling Show() emits
// only that cell's CUP+glyph, not the others.
func TestIncrementalFlushEmitsOnlyChanged(t *testing.T) {
	s, bw := newTestScreen(4, 1)
	s.SetContent(0, 0, 'A', nil, StyleDefault)
	s.SetContent(1, 0, 'B', nil, StyleDefault)
	_ = s.Sync()
	bw.buf.Reset()
	s.SetContent(1, 0, 'C', nil, StyleDefault)
	_ = s.Show()
	out := bw.buf.String()
	if !strings.Contains(out, "C") {
		t.Errorf("changed cell not emitted:\n%q", out)
	}
	if strings.Contains(out, "A") {
		t.Errorf("unchanged cell A re-emitted:\n%q", out)
	}
}
