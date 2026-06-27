//go:build !darwin && !linux

package main

// superviseTTY is a no-op on platforms without the interactive TUI (the terminal layer is
// unsupported there), so the caller always runs in-process. Mirrors superviseTTY on unix.
func superviseTTY() bool { return false }
