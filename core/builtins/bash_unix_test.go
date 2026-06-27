//go:build unix

package builtins

import (
	"context"
	"strings"
	"syscall"
	"testing"
	"time"
)

// --- Bash timeout should kill the entire process group (including background children), leaving no orphans. ---

// TestBashTimeoutKillsProcessGroup starts a command that spawns a background child, triggers timeout,
// and verifies the child (not bash itself) is also killed.
func TestBashTimeoutKillsProcessGroup(t *testing.T) {
	// Command: background sleep 2000 + foreground sleep 5 (will timeout). Background sleep PID is printed to stderr for liveness checks.
	// Bash timeout (1s) -> should kill the process group -> background sleep 2000 also dies.
	b := NewBashTool()
	b.Timeout = 1 * time.Second

	cmd := "sleep 2000 & BG_PID=$!; echo \"BG=$BG_PID\" >&2; sleep 5"
	out, err := b.Exec(context.Background(), rawJSON(t, map[string]string{"command": cmd}))
	_ = err // timeout produces IsError result, which is expected
	if !out.IsError {
		t.Logf("note: expected IsError on timeout, got content %q", out.Content)
	}

	// Parse BG=<pid> from stderr (merged into stdout/stderr Content on IsError).
	bgPID := extractBGPID(out.Content)
	if bgPID <= 0 {
		t.Skipf("could not extract background PID from output %q", out.Content)
	}
	// Give kill a moment to take effect
	time.Sleep(300 * time.Millisecond)
	// Liveness check: signal 0 does not actually send a signal, only tests process existence.
	if err := syscall.Kill(bgPID, 0); err == nil {
		// Process still alive = orphan leak = failure
		// Cleanup: best-effort kill
		_ = syscall.Kill(bgPID, syscall.SIGKILL)
		t.Errorf("background child (pid=%d) survived bash timeout; process group not killed", bgPID)
	}
}

// extractBGPID extracts a pid from "BG=123" style output.
func extractBGPID(s string) int {
	idx := strings.Index(s, "BG=")
	if idx < 0 {
		return 0
	}
	rest := s[idx+3:]
	var pid int
	for _, c := range rest {
		if c < '0' || c > '9' {
			break
		}
		pid = pid*10 + int(c-'0')
	}
	return pid
}

// TestBashTimeoutMessageInformative: timed-out commands should return a clear "timed out and killed" message
// instead of bare "signal: killed", so the model knows not to retry the same long command.
func TestBashTimeoutMessageInformative(t *testing.T) {
	b := NewBashTool()
	b.Timeout = 1 * time.Second
	// sleep 5 will definitely exceed the 1s timeout
	out, _ := b.Exec(context.Background(), rawJSON(t, map[string]string{"command": "sleep 5"}))
	if !out.IsError {
		t.Fatal("timeout should produce IsError")
	}
	if !strings.Contains(out.Content, "timed out") {
		t.Errorf("timeout error should mention 'timed out' (so model knows not to retry as-is), got: %q", out.Content)
	}
}

// TestBashNonZeroExitNotReportedAsTimeout: ordinary non-zero exit (not a timeout) should not be reported as timeout.
func TestBashNonZeroExitNotReportedAsTimeout(t *testing.T) {
	b := NewBashTool()
	b.Timeout = 30 * time.Second // much larger than command duration
	out, _ := b.Exec(context.Background(), rawJSON(t, map[string]string{"command": "exit 1"}))
	if !out.IsError {
		t.Fatal("non-zero exit should be IsError")
	}
	if strings.Contains(out.Content, "timed out") {
		t.Errorf("plain non-zero exit must not be reported as timeout, got: %q", out.Content)
	}
}

// TestBashParentDeadlineNotReportedAsTimeout: a parent context that is already DeadlineExceeded
// should not be mislabeled as a command timeout.
func TestBashParentDeadlineNotReportedAsTimeout(t *testing.T) {
	b := NewBashTool()
	b.Timeout = 30 * time.Second
	parentCtx, parentCancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer parentCancel()
	out, _ := b.Exec(parentCtx, rawJSON(t, map[string]string{"command": "sleep 0.1"}))
	if !out.IsError {
		t.Fatal("expired parent ctx should be IsError")
	}
	if strings.Contains(out.Content, "timed out") {
		t.Errorf("pre-expired parent ctx must not be reported as command timeout, got: %q", out.Content)
	}
}

// TestBashCancelNotReportedAsTimeout: user-initiated cancellation (Ctrl+C) should not be reported as timeout.
func TestBashCancelNotReportedAsTimeout(t *testing.T) {
	b := NewBashTool()
	b.Timeout = 30 * time.Second // much larger than command duration
	ctx, cancel := context.WithCancel(context.Background())
	// Cancel immediately after launch to simulate Ctrl+C.
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()
	out, _ := b.Exec(ctx, rawJSON(t, map[string]string{"command": "sleep 10"}))
	if !out.IsError {
		t.Fatal("canceled command should be IsError")
	}
	if strings.Contains(out.Content, "timed out") {
		t.Errorf("user cancel must not be reported as timeout, got: %q", out.Content)
	}
}
