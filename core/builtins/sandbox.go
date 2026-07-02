package builtins

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
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
//
// Deprecated: kept for backward compatibility. New callers should use NewOSSandbox + PolicySandbox,
// which support runtime mode-driven toggling and a configurable network policy.
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

// NewOSSandbox returns the OS-appropriate sandbox (macOS sandbox-exec / Linux bwrap) and whether it
// is available on this host. When the required binary is missing or the OS is unsupported it returns
// (nil, false) instead of an error, so the caller can defer the failure to actual use (fail-closed at
// Wrap time via PolicySandbox) rather than aborting startup for users who never enable isolation.
func NewOSSandbox(allowDirs []string, noNetwork bool) (sandbox Sandbox, available bool) {
	switch runtime.GOOS {
	case "darwin":
		if _, err := exec.LookPath("sandbox-exec"); err != nil {
			return nil, false
		}
		return DarwinSandbox{AllowDirs: allowDirs, NoNetwork: noNetwork}, true
	case "linux":
		if _, err := exec.LookPath("bwrap"); err != nil {
			return nil, false
		}
		return LinuxSandbox{AllowDirs: allowDirs, NoNetwork: noNetwork}, true
	default:
		return nil, false
	}
}

// SandboxController holds a per-session override for sandbox isolation, toggled at runtime via the
// /sandbox command. nil override = follow the base policy (config tri-state + permission mode);
// a non-nil override forces isolation on or off for the rest of the session. Safe for concurrent use.
type SandboxController struct {
	mu       sync.RWMutex
	override *bool
}

// NewSandboxController returns a controller with no override (follow base policy).
func NewSandboxController() *SandboxController { return &SandboxController{} }

// Override returns the current override (nil = follow base policy).
func (c *SandboxController) Override() *bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.override == nil {
		return nil
	}
	v := *c.override
	return &v
}

// Set updates the override. Pass nil to clear it (return to base policy).
func (c *SandboxController) Set(v *bool) {
	c.mu.Lock()
	if v == nil {
		c.override = nil
	} else {
		b := *v
		c.override = &b
	}
	c.mu.Unlock()
}

// PolicySandbox decides per invocation whether to isolate, consulting an isolate() policy closure.
// This lets a runtime /mode switch to "auto" (or a /sandbox toggle) enable isolation without
// rebuilding the agent, mirroring the ModeController pattern. When isolation is required but the OS
// sandbox is unavailable it fails closed: the command is replaced by one that prints guidance and
// exits non-zero, so the model never runs unsandboxed after isolation was requested.
type PolicySandbox struct {
	isolate func() bool
	real    Sandbox // OS sandbox; nil when unavailable on this host
	hint    string  // guidance surfaced when isolation is required but unavailable
}

// NewPolicySandbox builds a policy-driven sandbox. isolate is consulted on every Wrap; real is the
// OS sandbox (may be nil if unavailable); hint explains how to proceed when isolation is unavailable.
func NewPolicySandbox(isolate func() bool, real Sandbox, hint string) *PolicySandbox {
	if isolate == nil {
		isolate = func() bool { return false }
	}
	return &PolicySandbox{isolate: isolate, real: real, hint: hint}
}

// Wrap implements Sandbox.
func (p *PolicySandbox) Wrap(ctx context.Context, command, cwd string) *exec.Cmd {
	if !p.isolate() {
		return NoopSandbox{}.Wrap(ctx, command, cwd)
	}
	if p.real == nil {
		msg := "sandbox required but unavailable on this host: " + p.hint
		script := "printf '%s\\n' " + shellSingleQuote(msg) + " >&2; exit 1"
		return exec.CommandContext(ctx, "bash", "-c", script)
	}
	return p.real.Wrap(ctx, command, cwd)
}

// shellSingleQuote wraps s in single quotes for safe embedding in a bash -c script, escaping any
// embedded single quotes with the standard '\” idiom.
func shellSingleQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// SandboxUnavailableHint returns OS-specific guidance shown when OS isolation is requested but the
// required sandbox binary is missing on this host.
func SandboxUnavailableHint() string {
	switch runtime.GOOS {
	case "darwin":
		return "sandbox-exec not found in PATH (macOS); set [sandbox] enabled = false to run without isolation"
	case "linux":
		return "bubblewrap (bwrap) not found in PATH; install it, or set [sandbox] enabled = false to run without isolation"
	default:
		return "OS sandbox is not supported on this platform; set [sandbox] enabled = false to run without isolation"
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
