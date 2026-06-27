//go:build darwin || linux

package main

import (
	"os"
	"os/exec"
	"syscall"
	"testing"
)

// TestSuperviseTTYNoForkAsChild verifies the guard env short-circuits supervision so the child runs
// the TUI in-process (and, critically, that no re-exec loop can form).
func TestSuperviseTTYNoForkAsChild(t *testing.T) {
	t.Setenv(ttyGuardEnv, "1")
	if superviseTTY() {
		t.Fatal("superviseTTY must return false (run in-process) when the guard env is set")
	}
}

// TestSuperviseTTYOptOut verifies the escape hatch disables supervision.
func TestSuperviseTTYOptOut(t *testing.T) {
	t.Setenv(ttyGuardEnv, "")
	t.Setenv(noSupervisorEnv, "1")
	if superviseTTY() {
		t.Fatal("superviseTTY must return false when CREATOR_AGENT_NO_SUPERVISOR=1")
	}
}

// TestSuperviseTTYNonTTY verifies that without a real TTY (the case under `go test`) supervision is
// skipped, so the test binary never accidentally re-execs itself.
func TestSuperviseTTYNonTTY(t *testing.T) {
	t.Setenv(ttyGuardEnv, "")
	t.Setenv(noSupervisorEnv, "")
	if superviseTTY() {
		t.Fatal("superviseTTY must return false when stdin/stdout are not TTYs")
	}
}

func TestMergeGodebug(t *testing.T) {
	if got := mergeGodebug("", "clobberfree=1"); got != "GODEBUG=clobberfree=1" {
		t.Errorf("empty base: got %q", got)
	}
	if got := mergeGodebug("efence=1", "clobberfree=1"); got != "GODEBUG=efence=1,clobberfree=1" {
		t.Errorf("existing base: got %q", got)
	}
}

func TestChildExitCode(t *testing.T) {
	if got := childExitCode(nil); got != 0 {
		t.Errorf("nil err: got %d, want 0", got)
	}

	// Normal non-zero exit code is propagated verbatim.
	err := exec.Command("sh", "-c", "exit 7").Run()
	if got := childExitCode(err); got != 7 {
		t.Errorf("exit 7: got %d, want 7", got)
	}

	// Signal death maps to 128+signal (here SIGKILL = 9 -> 137).
	err = exec.Command("sh", "-c", "kill -KILL $$").Run()
	if got := childExitCode(err); got != 128+int(syscall.SIGKILL) {
		t.Errorf("SIGKILL: got %d, want %d", got, 128+int(syscall.SIGKILL))
	}

	// Non-ExitError (e.g. binary not found) falls back to 1.
	err = exec.Command("this-binary-does-not-exist-xyz").Run()
	if got := childExitCode(err); got != 1 {
		t.Errorf("missing binary: got %d, want 1", got)
	}

	_ = os.Stdout
}
