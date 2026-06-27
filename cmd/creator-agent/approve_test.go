package main

import (
	"context"
	"strings"
	"testing"
)

// Verify: input "y" -> allow once, but does not enter session allow-set (asked again next time).
func TestApproverYesOnce(t *testing.T) {
	useColor = false
	ap := newReplApprover(strings.NewReader("y\n"), &strings.Builder{})
	if !ap.approve(context.Background(), "write", `{"path":"x"}`) {
		t.Error("y should allow")
	}
	// y is not remembered: allow-set should not contain write
	ap.mu.Lock()
	has := ap.allowSet["write"]
	ap.mu.Unlock()
	if has {
		t.Error("y should NOT add to session allow-set (only 'a' does)")
	}
}

// Verify: input "a" -> allow + session-wide write/edit allow-set (matches TUI rememberAllEdits).
func TestApproverAlwaysRemember(t *testing.T) {
	useColor = false
	out := &strings.Builder{}
	ap := newReplApprover(strings.NewReader("a\n"), out)
	if !ap.approve(context.Background(), "write", `{"path":"x"}`) {
		t.Error("a should allow")
	}
	ap.mu.Lock()
	hasWrite := ap.allowSet["write"]
	hasEdit := ap.allowSet["edit"]
	ap.mu.Unlock()
	if !hasWrite || !hasEdit {
		t.Errorf("a on write should set session-wide write/edit allow-set, got write=%v edit=%v", hasWrite, hasEdit)
	}
}

// Verify: session-wide write/edit allow skips prompts for other paths.
func TestApproverAlwaysRememberAllEditsSkipsOtherPaths(t *testing.T) {
	useColor = false
	out := &strings.Builder{}
	ap := newReplApprover(strings.NewReader("a\n"), out)
	if !ap.approve(context.Background(), "write", `{"path":"/a.txt"}`) {
		t.Fatal("a should allow first write")
	}
	outAfterFirst := out.String()
	if !ap.approve(context.Background(), "write", `{"path":"/b.txt"}`) {
		t.Fatal("session-wide write allow should skip prompt for other paths")
	}
	if out.String() != outAfterFirst {
		t.Fatal("second write should not prompt after session allow")
	}
}

// Verify: allow-set hit directly allows (no prompt).
func TestApproverSessionSetSkipsPrompt(t *testing.T) {
	useColor = false
	out := &strings.Builder{}
	ap := newReplApprover(strings.NewReader("n\n"), out) // reader gives "n", but should be short-circuited by allow-set
	ap.mu.Lock()
	ap.allowSet["write"] = true
	ap.mu.Unlock()

	// Should not read from reader (direct allow-set hit)
	if !ap.approve(context.Background(), "write", `{}`) {
		t.Error("allow-set hit should allow without asking")
	}
	// out should not contain approval prompt (no question asked)
	if strings.Contains(out.String(), "Approval") {
		t.Error("should not prompt when allow-set hits")
	}
}

// Verify: input "n" / enter / other -> deny.
func TestApproverDeny(t *testing.T) {
	useColor = false
	cases := []string{"n\n", "\n", "xyz\n", "no\n"}
	for _, in := range cases {
		ap := newReplApprover(strings.NewReader(in), &strings.Builder{})
		if ap.approve(context.Background(), "write", `{}`) {
			t.Errorf("input %q should deny", strings.TrimSpace(in))
		}
	}
}

// Verify: EOF (no input) -> deny (safe side).
func TestApproverEOF(t *testing.T) {
	useColor = false
	ap := newReplApprover(strings.NewReader(""), &strings.Builder{})
	if ap.approve(context.Background(), "write", `{}`) {
		t.Error("EOF should deny")
	}
}

// Verify: headlessApprover fixed deny.
func TestHeadlessApproverDenyAll(t *testing.T) {
	h := headlessApprover{}
	if h.approve(context.Background(), "write", `{}`) {
		t.Error("headless should deny all writes")
	}
	if h.approve(context.Background(), "bash", `{}`) {
		t.Error("headless should deny bash")
	}
}

// Verify: truncateForPrompt summary.
func TestTruncateForPrompt(t *testing.T) {
	if got := truncateForPrompt("short"); got != "short" {
		t.Errorf("short = %q", got)
	}
	long := strings.Repeat("x", 200)
	got := truncateForPrompt(long)
	if !strings.HasSuffix(got, "…") {
		t.Errorf("long not truncated: %q", got)
	}
	if got := truncateForPrompt(""); got != "(no args)" {
		t.Errorf("empty = %q", got)
	}
	// multi-line collapsed to single line
	if got := truncateForPrompt("line1\nline2"); !strings.Contains(got, "⏎") {
		t.Errorf("newline not collapsed: %q", got)
	}
}
