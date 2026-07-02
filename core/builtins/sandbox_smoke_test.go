//go:build sandboxsmoke

package builtins

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestSandboxSmokeWriteIsolation actually invokes the OS sandbox binary (bwrap on Linux,
// sandbox-exec on macOS) to verify real write isolation, rather than only checking argument
// construction like the unit tests. It is gated behind the `sandboxsmoke` build tag so the default
// `go test ./...` never depends on the sandbox binary being installed; CI runs it explicitly on a
// host where the binary is present (see the sandbox-smoke job).
//
// Contract verified:
//   - A write inside the working directory succeeds.
//   - A write to a sibling directory outside the sandbox's allowed paths fails.
func TestSandboxSmokeWriteIsolation(t *testing.T) {
	sb, available := NewOSSandbox(nil, true)
	if !available {
		t.Skip("OS sandbox binary not available on this host")
	}

	cwd := t.TempDir()

	run := func(command string) error {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		cmd := sb.Wrap(ctx, command, cwd)
		cmd.Dir = cwd
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Logf("command %q output: %s", command, out)
		}
		return err
	}

	// Write inside cwd must succeed.
	if err := run("echo hello > inside.txt"); err != nil {
		t.Fatalf("write inside cwd should succeed, got: %v", err)
	}
	if _, err := os.Stat(filepath.Join(cwd, "inside.txt")); err != nil {
		t.Fatalf("inside.txt should exist: %v", err)
	}

	// The escape target must live outside every allow-listed area (cwd, TMPDIR on macOS,
	// allowDirs). $HOME qualifies: bwrap does not bind it (unshare-all), and the macOS profile
	// grants it read but not write. A working sandbox must block this write.
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		t.Skip("no home directory to use as an out-of-sandbox escape target")
	}
	target := filepath.Join(home, ".creator_agent_sandbox_smoke_escape.txt")
	_ = os.Remove(target)
	defer os.Remove(target) // defensive: clean up if a broken sandbox lets the write through

	if err := run("echo pwned > " + target); err == nil {
		if _, statErr := os.Stat(target); statErr == nil {
			t.Fatalf("write to %s should be blocked by the sandbox, but it succeeded", target)
		}
	}
}
