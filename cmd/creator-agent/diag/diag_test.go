package diag

import (
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"testing"
)

// reset clears package state so a test can run Setup() against a fresh temp HOME. Also restores the
// runtime crash output to stderr so a deleted temp fatal.log is never left as the crash destination.
func reset(t *testing.T) {
	t.Helper()
	mu.Lock()
	if traceFile != nil {
		_ = traceFile.Close()
		traceFile = nil
	}
	if fatalFile != nil {
		_ = fatalFile.Close()
		fatalFile = nil
	}
	mu.Unlock()
	t.Cleanup(func() {
		mu.Lock()
		if traceFile != nil {
			_ = traceFile.Close()
			traceFile = nil
		}
		if fatalFile != nil {
			_ = fatalFile.Close()
			fatalFile = nil
		}
		mu.Unlock()
		_ = debug.SetCrashOutput(os.Stderr, debug.CrashOptions{})
	})
}

func TestSetupWritesHeaderAndTrace(t *testing.T) {
	reset(t)
	home := t.TempDir()
	t.Setenv("HOME", home)

	Setup()
	Trace("tool.start name=%s id=%s", "edit", "call_1")
	Trace("stream.end")

	data, err := os.ReadFile(filepath.Join(home, ".creator", traceFileName))
	if err != nil {
		t.Fatalf("read trace.log: %v", err)
	}
	out := string(data)
	for _, want := range []string{"session start", "tool.start name=edit id=call_1", "stream.end"} {
		if !strings.Contains(out, want) {
			t.Errorf("trace.log missing %q\n--- got ---\n%s", want, out)
		}
	}
	// fatal.log must exist (crash destination opened) even though no crash occurred.
	if _, err := os.Stat(filepath.Join(home, ".creator", fatalFileName)); err != nil {
		t.Errorf("fatal.log not created: %v", err)
	}
}

func TestTraceNoopWhenDisabled(t *testing.T) {
	reset(t)
	// traceFile is nil after reset and Setup not called: Trace must be a safe no-op.
	Trace("should not panic %d", 1)
}

func TestRotateIfLarge(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "trace.log")
	big := make([]byte, maxLogBytes+1)
	if err := os.WriteFile(path, big, 0o600); err != nil {
		t.Fatal(err)
	}
	rotateIfLarge(path)
	if _, err := os.Stat(path + ".1"); err != nil {
		t.Errorf("expected rotated file %q.1: %v", path, err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("original should have been renamed away, stat err=%v", err)
	}

	// A small file must NOT rotate.
	small := filepath.Join(dir, "small.log")
	if err := os.WriteFile(small, []byte("hi"), 0o600); err != nil {
		t.Fatal(err)
	}
	rotateIfLarge(small)
	if _, err := os.Stat(small + ".1"); !os.IsNotExist(err) {
		t.Errorf("small file should not rotate")
	}
}

func TestBuildRevisionNonEmpty(t *testing.T) {
	if got := buildRevision(); got == "" {
		t.Error("buildRevision returned empty string")
	}
}
