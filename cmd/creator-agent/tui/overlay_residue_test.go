package tui

// overlay_residue_test.go guards the bug where closing a full-width overlay (diff/help) left stale
// glyphs in the screen's left columns. The screen is front/back-diffed and never auto-cleared; the
// claude layout's main view paints only the inset main column, so without a per-frame clear the
// overlay's col-0 glyphs ('@@' / '+' diff prefixes) bled through after the overlay closed.

import (
	"testing"
)

func TestOverlayCloseLeavesNoLeftEdgeResidue(t *testing.T) {
	a, sim := newAppWithSim(t, 60, 20)

	a.diffOverlay.open = true
	a.diffOverlay.title = "diff"
	lines := []string{"@@ -1,3 +1,3 @@"}
	for i := 0; i < 30; i++ {
		lines = append(lines, "+added content line")
	}
	a.diffOverlay.lines = lines
	render(a)

	// Precondition: the overlay paints a diff prefix into col 0 (row 1 is the first body line).
	cells, w, _ := sim.GetContents()
	if m := cells[1*w+0].Main; m != '@' {
		t.Fatalf("precondition: expected '@' at (0,1), got %q\n%s", m, dumpScreen(sim))
	}

	closeDiffOverlay(a)
	render(a)

	assertNoLeftEdgeDiffResidue := func(stage string) {
		cells, w, h := sim.GetContents()
		for y := 0; y < h; y++ {
			for x := 0; x < 2; x++ {
				if m := cells[y*w+x].Main; m == '@' || m == '+' {
					t.Errorf("%s: residue %q at (%d,%d)\n%s", stage, m, x, y, dumpScreen(sim))
				}
			}
		}
	}
	assertNoLeftEdgeDiffResidue("after overlay close")

	// The overlay-exit clear should have fired; the optimization then marks subsequent frames
	// as steady-state (no full clear), and they must still be residue-free.
	if a.lastFrameOverlay {
		t.Errorf("lastFrameOverlay should be false after a main-view frame")
	}
	render(a)
	assertNoLeftEdgeDiffResidue("steady-state frame after close")
}
