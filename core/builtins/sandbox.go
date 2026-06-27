package builtins

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// Sandbox wraps a bash command into a restricted execution environment.
//
// Defense in depth: permission middleware (denylist, best-effort) + sandbox (OS-level hard isolation).
// The permission layer is application-level interception (can be bypassed by prompt jailbreaks / shell
// splitting heuristics); the sandbox is OS kernel-level hard isolation (bottom line).
//
// Implementations: NoopSandbox (plain execution, default) / DarwinSandbox (sandbox-exec) / LinuxSandbox (bwrap).
type Sandbox interface {
	// Wrap turns command into an *exec.Cmd (with sandbox invocation + original bash -c).
	// cwd is the writable working directory inside the sandbox (sandbox allows writes to cwd, blocks others).
	Wrap(ctx context.Context, command, cwd string) *exec.Cmd
}

// NoopSandbox performs no isolation; runs bash -c directly (preserves original behavior, backward compatible).
type NoopSandbox struct{}

// Wrap returns a plain bash -c command.
func (NoopSandbox) Wrap(ctx context.Context, command, _ string) *exec.Cmd {
	return exec.CommandContext(ctx, "bash", "-c", command)
}

// DarwinSandbox uses macOS sandbox-exec (Seatbelt) for isolation.
//
// Policy: deny default + allow essentials (process/read/cwd/tmp) + optional deny network.
// Dynamically generates a profile (interpolates cwd/tmp per call) and invokes
// `sandbox-exec -p '<profile>' bash -c '<cmd>'`.
// Note: sandbox-exec has been deprecated since macOS Sierra (2016) but remains available
// (industry-standard CLI tools still rely on it).
//
// Security: cwd/allowDirs/tmpDir are escaped with escapeSchemeString before insertion into the profile,
// preventing injection of arbitrary profile rules via " / \ in paths.
type DarwinSandbox struct {
	AllowDirs []string // additional directories allowed for writing outside cwd
	NoNetwork bool     // disable network (default true)
}

// Wrap returns a sandbox-exec wrapped command.
func (d DarwinSandbox) Wrap(ctx context.Context, command, cwd string) *exec.Cmd {
	profile := darwinProfile(cwd, d.AllowDirs, darwinTmpDir(), d.NoNetwork)
	return exec.CommandContext(ctx, "sandbox-exec", "-p", profile, "bash", "-c", command)
}

// darwinProfile generates a sandbox-exec profile (pure function, easily testable).
//
// Policy (allowlist-style reverse: deny default, then allow essentials).
// tmpDir is passed by the caller (resolved real path of the user's TMPDIR), keeping this function pure.
func darwinProfile(cwd string, allowDirs []string, tmpDir string, noNetwork bool) string {
	var sb strings.Builder
	sb.WriteString("(version 1)\n(deny default)\n")
	// Base allowances (bash startup + required system reads)
	sb.WriteString(`(allow process-exec (literal "/bin/bash") (literal "/bin/sh"))` + "\n")
	sb.WriteString(`(allow process-fork) (allow signal (target self))` + "\n")
	sb.WriteString(`(allow file-read*)` + "\n") // reads are generally harmless
	// Allow writes to cwd + allowDirs (escaped before insertion to prevent injection)
	sb.WriteString(fmt.Sprintf(`(allow file-write* (subpath "%s"))`+"\n", escapeSchemeString(cwd)))
	for _, d := range allowDirs {
		if d = filepath.Clean(d); d != "" {
			sb.WriteString(fmt.Sprintf(`(allow file-write* (subpath "%s"))`+"\n", escapeSchemeString(d)))
		}
	}
	// Temp directory: only allow the user's resolved TMPDIR real path (tightened to avoid exposing all of /private/var/folders)
	if tmpDir = filepath.Clean(tmpDir); tmpDir != "" && tmpDir != "." {
		sb.WriteString(fmt.Sprintf(`(allow file-write* (subpath "%s"))`+"\n", escapeSchemeString(tmpDir)))
	}
	// Network
	if noNetwork {
		sb.WriteString(`(deny network*)` + "\n")
	} else {
		sb.WriteString(`(allow network*)` + "\n")
	}
	return sb.String()
}

// LinuxSandbox uses bubblewrap (bwrap) for isolation.
//
// Policy: unshare-all + ro-bind system dirs + bind cwd (writable) + tmpfs /tmp + disable network (unshare net).
// Invokes `bwrap --die-with-parent --unshare-all --ro-bind /usr --bind <cwd> ... -- bash -c '<cmd>'`.
type LinuxSandbox struct {
	AllowDirs []string
	NoNetwork bool
}

// Wrap returns a bwrap wrapped command.
func (l LinuxSandbox) Wrap(ctx context.Context, command, cwd string) *exec.Cmd {
	args := bwrapArgs(cwd, l.AllowDirs, l.NoNetwork)
	args = append(args, "bash", "-c", command)
	return exec.CommandContext(ctx, "bwrap", args...)
}

// bwrapArgs builds bwrap arguments (pure function, easily testable).
func bwrapArgs(cwd string, allowDirs []string, noNetwork bool) []string {
	args := []string{"--die-with-parent", "--unshare-all"}
	// --unshare-all already includes network isolation; noNetwork is reserved for future refinement
	_ = noNetwork
	// System directories read-only bind (required by bash/core libraries)
	for _, sysdir := range []string{"/usr", "/bin", "/lib", "/lib64"} {
		args = append(args, "--ro-bind", sysdir, sysdir)
	}
	args = append(args, "--ro-bind", "/etc", "/etc")
	// proc/dev (required by bash startup and common tools)
	args = append(args, "--proc", "/proc", "--dev", "/dev")
	// cwd writable bind
	if cwd != "" {
		args = append(args, "--bind", cwd, cwd)
	}
	// Additional writable directories
	for _, d := range allowDirs {
		if d = filepath.Clean(d); d != "" {
			args = append(args, "--bind", d, d)
		}
	}
	// tmp (use an isolated tmpfs instead of exposing the host's global /tmp)
	args = append(args, "--tmpfs", "/tmp")
	return args
}

// NewSandbox selects a sandbox implementation based on GOOS and config.
// enabled=false -> NoopSandbox (default, zero behavior change).
// enabled=true -> selects Darwin/Linux; if the binary is missing (exec.LookPath) returns an error (fail-closed).
func NewSandbox(enabled bool, allowDirs []string) (Sandbox, error) {
	if !enabled {
		return NoopSandbox{}, nil
	}
	switch runtime.GOOS {
	case "darwin":
		if _, err := exec.LookPath("sandbox-exec"); err != nil {
			return nil, fmt.Errorf("sandbox enabled but sandbox-exec not found in PATH (macOS): %w", err)
		}
		return DarwinSandbox{AllowDirs: allowDirs, NoNetwork: true}, nil
	case "linux":
		if _, err := exec.LookPath("bwrap"); err != nil {
			return nil, fmt.Errorf("sandbox enabled but bwrap not found in PATH (Linux): %w", err)
		}
		return LinuxSandbox{AllowDirs: allowDirs, NoNetwork: true}, nil
	default:
		// Windows etc.: sandbox not supported, fail-closed when enabled
		return nil, fmt.Errorf("sandbox enabled but OS %q not supported", runtime.GOOS)
	}
}

// escapeSchemeString escapes characters for insertion into a sandbox-exec profile (Scheme syntax),
// preventing " or \ in paths from closing the string and injecting arbitrary profile rules.
func escapeSchemeString(s string) string {
	var sb strings.Builder
	sb.Grow(len(s))
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '"':
			sb.WriteString(`\"`)
		case '\\':
			sb.WriteString(`\\`)
		default:
			sb.WriteByte(s[i])
		}
	}
	return sb.String()
}

// darwinTmpDir returns the real resolved path of the user's TMPDIR.
// macOS TMPDIR is like /var/folders/.../T/; after resolving symlinks it becomes /private/var/folders/.../T/.
// Only this specific subdirectory is allowed, not the entire /private/var/folders tree.
func darwinTmpDir() string {
	t := os.TempDir()
	if real, err := filepath.EvalSymlinks(t); err == nil && real != "" {
		return real
	}
	return t
}

// Ensure Sandbox interface implementations.
var _ Sandbox = NoopSandbox{}
var _ Sandbox = DarwinSandbox{}
var _ Sandbox = LinuxSandbox{}
