package main

import (
	"fmt"
	"os"
	"sync"
)

// exitCleanups is the registry of cleanup functions that must run before exit (executed in reverse registration order).
//
// Background: main has multiple os.Exit paths (die / tui error) that skip defer,
// causing leaks of MCP stdio child processes, spill temp files, and AutoMemory extraction goroutines.
// Register respective cleanups after these resources are successfully created.
//
// Registration timing: immediately **after** the resource is successfully created (e.g. after mcp.LoadAll succeeds, register mcpClose).
// Cleanup functions should be idempotent and non-blocking (best-effort; failure does not block exit).
var (
	exitCleanups   []func()
	exitCleanupsMu sync.Mutex
)

// registerCleanup registers an exit cleanup function (LIFO: last registered runs first).
func registerCleanup(fn func()) {
	exitCleanupsMu.Lock()
	defer exitCleanupsMu.Unlock()
	exitCleanups = append(exitCleanups, fn)
}

// runCleanups executes all registered cleanup functions in reverse order (each panic is recovered, not blocking others).
// Last line of defense before process exit.
func runCleanups() {
	exitCleanupsMu.Lock()
	fns := exitCleanups
	exitCleanups = nil // prevent re-execution
	exitCleanupsMu.Unlock()
	for i := len(fns) - 1; i >= 0; i-- {
		func() {
			defer func() { _ = recover() }()
			fns[i]()
		}()
	}
}

// exitWith runs all cleanups then exits with the given code (replaces bare os.Exit, ensuring cleanup runs).
func exitWith(code int) {
	runCleanups()
	os.Exit(code)
}

// exitErr runs cleanups, prints the error, and exits with the given code.
func exitErr(err error, code int) {
	fmt.Fprintln(os.Stderr, "error:", err)
	exitWith(code)
}
