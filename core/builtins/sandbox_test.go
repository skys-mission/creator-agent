package builtins

import (
	"context"
	"os/exec"
	"runtime"
	"strings"
	"testing"
)

// ===== NoopSandbox =====

func TestNoopSandboxWrap(t *testing.T) {
	cmd := NoopSandbox{}.Wrap(context.Background(), "ls -la", "/tmp")
	if cmd.Path == "" {
		t.Fatal("noop should return a valid cmd")
	}
	// Should be plain bash -c, no wrapping
	if len(cmd.Args) < 3 || cmd.Args[1] != "-c" {
		t.Errorf("noop args = %v, want [bash -c <cmd>]", cmd.Args)
	}
}

// ===== darwinProfile pure function =====

func TestDarwinProfileContainsCwd(t *testing.T) {
	p := darwinProfile("/Users/x/project", nil, "/private/var/folders/xx/T", true)
	if !strings.Contains(p, `(version 1)`) {
		t.Error("missing version")
	}
	if !strings.Contains(p, `/Users/x/project`) {
		t.Error("cwd not in profile")
	}
	if !strings.Contains(p, `file-write*`) {
		t.Error("missing file-write rule")
	}
}

func TestDarwinProfileDeniesNetwork(t *testing.T) {
	p := darwinProfile("/cwd", nil, "/tmp", true)
	if !strings.Contains(p, `(deny network*)`) {
		t.Error("network not denied")
	}
	p2 := darwinProfile("/cwd", nil, "/tmp", false)
	if !strings.Contains(p2, `(allow network*)`) {
		t.Error("network should be allowed when NoNetwork=false")
	}
}

func TestDarwinProfileAllowDirs(t *testing.T) {
	p := darwinProfile("/cwd", []string{"/tmp/extra", "/var/log"}, "/tmp", true)
	if !strings.Contains(p, "/tmp/extra") || !strings.Contains(p, "/var/log") {
		t.Errorf("allowDirs not in profile: %s", p)
	}
}

func TestDarwinProfileTmpDir(t *testing.T) {
	tmp := "/private/var/folders/xx/yy/T"
	p := darwinProfile("/cwd", nil, tmp, true)
	if !strings.Contains(p, tmp) {
		t.Errorf("macOS TMPDIR allow rule missing: %s", p)
	}
	// Should not over-broadly allow the entire /private/var/folders tree
	if strings.Contains(p, `(subpath "/private/var/folders")`) {
		t.Errorf("profile over-broadly allows entire /private/var/folders: %s", p)
	}
}

// cwd/allowDirs containing " must be escaped so they cannot inject extra profile rules.
func TestDarwinProfileEscapesInjection(t *testing.T) {
	evil := `/tmp/x") (allow file-write* (subpath "/etc`
	p := darwinProfile(evil, nil, "/tmp", true)
	// The injected /etc allow should not appear (evil " is escaped to \", so the whole thing stays a single subpath string)
	if strings.Contains(p, `(subpath "/etc")`) {
		t.Errorf("profile injection NOT neutralized — /etc allow leaked:\n%s", p)
	}
	if !strings.Contains(p, `\"`) {
		t.Errorf("expected escaped quotes in profile:\n%s", p)
	}
}

// ===== bwrapArgs pure function =====

func TestBwrapArgsBasic(t *testing.T) {
	args := bwrapArgs("/home/x/proj", nil, true)
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "--die-with-parent") {
		t.Error("missing --die-with-parent")
	}
	if !strings.Contains(joined, "--unshare-all") {
		t.Error("missing --unshare-all")
	}
	if !strings.Contains(joined, "--bind /home/x/proj /home/x/proj") {
		t.Errorf("cwd not bound: %s", joined)
	}
}

func TestBwrapArgsSystemDirs(t *testing.T) {
	args := bwrapArgs("/proj", nil, true)
	joined := strings.Join(args, " ")
	for _, sys := range []string{"/usr", "/bin", "/etc", "/proc"} {
		if !strings.Contains(joined, sys) {
			t.Errorf("system dir %s missing", sys)
		}
	}
}

func TestBwrapArgsAllowDirs(t *testing.T) {
	args := bwrapArgs("/proj", []string{"/extra"}, true)
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "--bind /extra /extra") {
		t.Errorf("allowDir not bound: %s", joined)
	}
}

// ===== NewSandbox factory =====

func TestNewSandboxDisabled(t *testing.T) {
	s, err := NewSandbox(false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := s.(NoopSandbox); !ok {
		t.Errorf("disabled should return NoopSandbox, got %T", s)
	}
}

func TestNewSandboxEnabledBinaryCheck(t *testing.T) {
	// When sandbox is enabled: if the binary exists on the current platform, return the corresponding implementation;
	// otherwise return an error.
	s, err := NewSandbox(true, nil)
	switch runtime.GOOS {
	case "darwin":
		if err != nil {
			// sandbox-exec may exist; if it errors, that means it really is not available
			if _, lookErr := exec.LookPath("sandbox-exec"); lookErr != nil {
				return // expected: no binary, error
			}
			t.Errorf("unexpected error: %v", err)
		}
		if _, ok := s.(DarwinSandbox); !ok && s != nil {
			t.Errorf("darwin should return DarwinSandbox, got %T", s)
		}
	case "linux":
		if err != nil {
			if _, lookErr := exec.LookPath("bwrap"); lookErr != nil {
				return // expected: no bwrap, error
			}
			t.Errorf("unexpected error: %v", err)
		}
		if _, ok := s.(LinuxSandbox); !ok && s != nil {
			t.Errorf("linux should return LinuxSandbox, got %T", s)
		}
	default:
		// Windows etc.: enabling must error
		if err == nil {
			t.Error("unsupported OS with sandbox enabled should error")
		}
	}
}

// ===== Darwin/Linux Wrap produce correct commands =====

func TestDarwinSandboxWrapCommand(t *testing.T) {
	cmd := DarwinSandbox{NoNetwork: true}.Wrap(context.Background(), "ls", "/cwd")
	if cmd.Path == "" {
		t.Fatal("no cmd")
	}
	// Should be sandbox-exec -p <profile> bash -c ls
	if len(cmd.Args) < 5 || cmd.Args[0] == "" {
		t.Errorf("darwin wrap args wrong: %v", cmd.Args)
	}
	// Verify it contains sandbox-exec and profile
	joined := strings.Join(cmd.Args, " ")
	if !strings.Contains(joined, "sandbox-exec") {
		t.Error("missing sandbox-exec")
	}
}

func TestLinuxSandboxWrapCommand(t *testing.T) {
	cmd := LinuxSandbox{}.Wrap(context.Background(), "ls", "/cwd")
	if cmd.Path == "" {
		t.Fatal("no cmd")
	}
	joined := strings.Join(cmd.Args, " ")
	if !strings.Contains(joined, "bwrap") {
		t.Error("missing bwrap")
	}
	if !strings.Contains(joined, "bash -c") {
		t.Error("missing bash -c")
	}
}

// TestEscapeSchemeStringBoundaries covers escapeSchemeString edge cases (empty string + mixed).
func TestEscapeSchemeStringBoundaries(t *testing.T) {
	// empty string -> empty string
	if got := escapeSchemeString(""); got != "" {
		t.Errorf("empty string should stay empty, got %q", got)
	}
	// mixed " and \ -> both escaped
	got := escapeSchemeString(`a"b\c`)
	if !strings.Contains(got, `\"`) || !strings.Contains(got, `\\`) {
		t.Errorf("quotes and backslashes should be escaped, got %q", got)
	}
	// no special characters -> pass through unchanged
	if got := escapeSchemeString("/normal/path"); got != "/normal/path" {
		t.Errorf("normal path should pass through, got %q", got)
	}
}

// TestBwrapArgsNoNetwork: the noNetwork parameter is currently ignored (unshare-all already includes network isolation).
// Verify that passing true/false both produce valid args without panicking.
func TestBwrapArgsNoNetwork(t *testing.T) {
	for _, nn := range []bool{true, false} {
		args := bwrapArgs("/cwd", []string{"/extra"}, nn)
		if len(args) == 0 {
			t.Errorf("bwrapArgs(noNetwork=%v) should return non-empty args", nn)
		}
		// Should contain die-with-parent + unshare-all
		joined := strings.Join(args, " ")
		if !strings.Contains(joined, "--die-with-parent") || !strings.Contains(joined, "--unshare-all") {
			t.Errorf("bwrapArgs missing core flags (noNetwork=%v): %s", nn, joined)
		}
	}
}

// TestNewSandboxEnabledUnsupportedOS: on supported OSes (darwin/linux) this test verifies the normal path;
// if Windows support is added in the future, this test will already be green. Currently covers
// NewSandbox's enabled=true branch (complements TestNewSandboxEnabledBinaryCheck).
func TestNewSandboxEnabledReturnsErrorOrImpl(t *testing.T) {
	s, err := NewSandbox(true, []string{"/tmp"})
	// Either returns an implementation (binary exists) or an error (binary missing / OS unsupported)
	if err != nil {
		// error path: s must be nil
		if s != nil {
			t.Errorf("on error, sandbox should be nil, got %T", s)
		}
		return
	}
	if s == nil {
		t.Error("no error but sandbox is nil")
	}
}
