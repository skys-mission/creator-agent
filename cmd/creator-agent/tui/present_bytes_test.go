package tui

// present_bytes_test.go verifies the ACTUAL bytes the Screen's Present()/Sync() emits — not the
// back buffer (which SimulationScreen-style tests check), but the real on-wire escape sequence.
// This is the layer the terminal sees, and the one that was never directly asserted before.

import (
	"bytes"
	"strings"
	"testing"
)

// TestPresentRunBasedCUP: in incremental mode (Show), two changed cells with an unchanged gap
// between them form two separate runs, each getting its own CUP. First establish a front frame
// (Sync), then change two non-adjacent cells and Show().
func TestPresentRunBasedCUP(t *testing.T) {
	bw := &bufWriter{}
	s := NewScreen(bw, 8, 1)
	// Establish front frame: all spaces.
	_ = s.Sync()
	bw.buf.Reset()
	// Now change cells 0 and 3; cells 1,2 are unchanged (match front) so they break the run.
	s.SetContent(0, 0, 'A', nil, StyleDefault)
	s.SetContent(3, 0, 'B', nil, StyleDefault)
	_ = s.Show()
	out := bw.buf.String()
	// Two runs separated by the unchanged gap => a CUP at (0,0)="1;1H" and at (3,0)="1;4H".
	if !strings.Contains(out, "\x1b[1;1H") {
		t.Errorf("missing run CUP(0,0):\n%s", escRepr(out))
	}
	if !strings.Contains(out, "\x1b[1;4H") {
		t.Errorf("missing run CUP(3,0):\n%s", escRepr(out))
	}
	// The gap cells (1,2) must NOT be emitted as spaces.
	if strings.Contains(out, "A  B") || strings.Contains(out, "A B") {
		t.Errorf("unchanged gap cells emitted (should be skipped):\n%s", escRepr(out))
	}
}

// TestPresentAdjacentCellsShareCUP: two adjacent changed cells form one run and get a single CUP
// at the run start, with bytes written sequentially (cursor advance within the run).
func TestPresentAdjacentCellsShareCUP(t *testing.T) {
	bw := &bufWriter{}
	s := NewScreen(bw, 8, 1)
	s.SetContent(0, 0, 'A', nil, StyleDefault)
	s.SetContent(1, 0, 'B', nil, StyleDefault)
	_ = s.Sync()
	out := bw.buf.String()
	// Only one CUP at (0,0); 'B' is written after 'A' without a second CUP.
	if c := strings.Count(out, "\x1b[1;"); c != 1 {
		t.Errorf("adjacent run should have 1 CUP, got %d:\n%s", c, escRepr(out))
	}
	if !strings.Contains(out, "AB") {
		t.Errorf("run bytes not written sequentially (AB):\n%s", escRepr(out))
	}
}

// TestPresentWideRuneFollowedByCUP: a width-2 rune at (0,0) followed by content at (2,0). After
// the wide rune the run is broken and the next primary cell is repositioned with an explicit CUP
// so the terminal cannot drift if its cursor advance disagrees with our width model. The follow
// cell must NOT be emitted as a separate space.
func TestPresentWideRuneFollowedByCUP(t *testing.T) {
	bw := &bufWriter{}
	s := NewScreen(bw, 6, 1)
	s.SetContent(0, 0, '你', nil, StyleDefault)
	s.SetContent(2, 0, 'x', nil, StyleDefault)
	_ = s.Sync()
	out := bw.buf.String()
	// First CUP at (0,0) for the wide rune.
	if !strings.Contains(out, "\x1b[1;1H") {
		t.Errorf("missing run-start CUP(0,0):\n%s", escRepr(out))
	}
	// Second CUP at (2,0) for the following cell — this is the anti-drift reposition.
	if !strings.Contains(out, "\x1b[1;3H") {
		t.Errorf("missing follow-up CUP(2,0) after wide rune:\n%s", escRepr(out))
	}
	// The wide rune bytes must appear.
	if !strings.Contains(out, "你") {
		t.Errorf("wide rune not emitted:\n%s", escRepr(out))
	}
	// The follow cell (1,0) must not be written as a separate space glyph.
	if strings.Contains(out, "\x1b[1;2H ") {
		t.Errorf("follow cell (1,0) emitted as a space, corrupting the wide glyph:\n%s", escRepr(out))
	}
}

// TestPresentNoFullClearPerFrame: Sync must NOT emit \x1b[2J (full screen clear), which causes
// flicker. Each cell is overwritten in place via CUP.
func TestPresentNoFullClearPerFrame(t *testing.T) {
	bw := &bufWriter{}
	s := NewScreen(bw, 4, 1)
	s.SetContent(0, 0, 'A', nil, StyleDefault)
	_ = s.Sync()
	out := bw.buf.String()
	if strings.Contains(out, "\x1b[2J") {
		t.Errorf("Sync emitted full-screen clear (causes flicker):\n%s", escRepr(out))
	}
}

// TestPresentCursorShownAtRequestedPosition: after rendering, the cursor is positioned at the
// requested ShowCursor location and shown.
func TestPresentCursorShownAtRequestedPosition(t *testing.T) {
	bw := &bufWriter{}
	s := NewScreen(bw, 4, 2)
	s.SetContent(0, 0, 'A', nil, StyleDefault)
	s.ShowCursor(1, 1)
	_ = s.Sync()
	out := bw.buf.String()
	if !strings.Contains(out, "\x1b[2;2H") {
		t.Errorf("cursor not placed at (1,1):\n%s", escRepr(out))
	}
	if !strings.Contains(out, "\x1b[?25h") {
		t.Errorf("cursor show sequence missing:\n%s", escRepr(out))
	}
}

// escRepr renders the byte string with escapes visible, for readable failure output.
func escRepr(s string) string {
	var b bytes.Buffer
	for _, r := range s {
		switch r {
		case 0x1b:
			b.WriteString("\\x1b")
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}
