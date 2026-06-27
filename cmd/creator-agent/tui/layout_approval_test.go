package tui

// layout_approval_test.go guards the Claude-layout rendering bug where switching from the prompt
// (whose footer reads "Tab agents ^P commands") to the permission approval block left the footer
// bleeding through the approval rows. drawApprovalBlockClaude previously drew only its rule +
// lines without clearing the body rect; since the screen is front/back-diffed and the back buffer
// is not reset per frame, any cell not rewritten keeps its previous glyph, so the shorter approval
// lines exposed stale prompt-footer text mixed with the file path and buttons.

import (
	"strconv"
	"strings"
	"testing"
)

func TestApprovalBlockClearsStaleFooter(t *testing.T) {
	a, sim := newAppWithSim(t, 80, 24)

	// Frame 1: idle prompt — its footer ("Tab agents ^P commands") sits on the bottom row.
	render(a)
	if !strings.Contains(rowText(sim, 23), "Tab agents") {
		t.Fatalf("frame 1: expected prompt footer on bottom row; got %q", rowText(sim, 23))
	}

	// Frame 2: permission approval replaces the prompt at the bottom.
	rc := make(chan bool, 1)
	a.asking = &askMsg{toolName: "bash", input: `{"command":"ls -la"}`, replyCh: rc}
	render(a)

	// The stale footer must not bleed through the approval block.
	if strings.Contains(dumpScreen(sim), "Tab agents") {
		t.Errorf("stale prompt footer bled through the approval block:\n%s", dumpScreen(sim))
	}
}

// TestApprovalBlockKeepsButtonsVisibleOnLargeDiff guards the bug where a write/edit of a large file
// pushed the action buttons off the bottom of the approval block. fullFileDiff emits one diff row
// per changed line, so a 100-line write produces ~100 rows; the approval block is capped at h-4
// (~20 rows), and the block was painted top-down, truncating the trailing prompt + buttons. The
// user then sees a wall of diff with no Allow/Deny to act on, which reads as "stuck at approval".
func TestApprovalBlockKeepsButtonsVisibleOnLargeDiff(t *testing.T) {
	a, sim := newAppWithSim(t, 80, 24)

	content := strings.Repeat("line content here\n", 100)
	input := `{"path":"big.txt","content":` + strconv.Quote(content) + `}`
	a.asking = &askMsg{toolName: "write", input: input, replyCh: make(chan bool, 1)}
	render(a)

	screen := dumpScreen(sim)
	if !strings.Contains(screen, "1. Yes") || !strings.Contains(screen, "3. No") {
		t.Errorf("approval options truncated by large preview; expected 1. Yes / 3. No visible.\n%s", screen)
	}
	if !strings.Contains(screen, "big.txt") {
		t.Errorf("approval file path truncated; expected big.txt visible.\n%s", screen)
	}
}
