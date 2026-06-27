package tui

import "testing"

// TestPreviewCompletionSkipsFileMention verifies that navigating the file (@mention) completion
// menu with arrow keys does not clobber the input: the candidate is a path spliced into the
// surrounding text, so only slash-command candidates (whose candidate IS the entire input) are
// mirrored into the input box. Regression guard for the @mention token being overwritten.
func TestPreviewCompletionSkipsFileMention(t *testing.T) {
	a, _ := newAppWithSim(t, 80, 24)

	// File completion: preview must NOT touch the input.
	a.input.SetValue("fix @main")
	a.completionsKind = compFile
	a.completions = []string{"main.go", "main_test.go"}
	a.compIdx = 1
	previewCompletion(a)
	if got := a.input.Value(); got != "fix @main" {
		t.Fatalf("compFile preview clobbered input: got %q, want %q", got, "fix @main")
	}

	// Slash completion: preview mirrors the candidate into the input.
	a.completionsKind = compSlash
	a.completions = []string{"/help", "/clear"}
	a.compIdx = 1
	previewCompletion(a)
	if got := a.input.Value(); got != "/clear" {
		t.Fatalf("compSlash preview should mirror candidate: got %q, want %q", got, "/clear")
	}
}
