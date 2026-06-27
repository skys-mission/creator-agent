//go:build !unix

package builtins

import "os/exec"

// setSysProcAttr is a no-op on non-Unix (e.g. Windows): Windows has no process group semantics.
// Keeps the original behavior (ctx cancellation SIGKILLs the direct child only).
func setSysProcAttr(_ *exec.Cmd) {}

// killProcessGroup is a no-op on non-Unix: handled by exec.CommandContext's default kill.
func killProcessGroup(_ *exec.Cmd) error { return nil }
