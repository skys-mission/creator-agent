//go:build darwin || linux

package main

import (
	"errors"
	"flag"
	"os"
	"os/exec"
	"os/signal"
	"syscall"

	"github.com/skys-mission/creator-agent/cmd/creator-agent/tui"
)

// ttyGuardEnv marks the supervised child process. Set on the child so it skips supervision and runs
// the TUI directly, which also prevents an infinite re-exec loop.
const ttyGuardEnv = "CREATOR_AGENT_TTY_GUARD"

// noSupervisorEnv is an opt-out escape hatch: set to "1" to disable the crash-safety supervisor and
// run the TUI in-process (single process), at the cost of losing terminal restoration on Go fatal
// runtime errors.
const noSupervisorEnv = "CREATOR_AGENT_NO_SUPERVISOR"

// hardenEnv, when "1", makes the supervisor enable extra runtime memory checks (GODEBUG=clobberfree)
// in the child to pinpoint use-after-free corruption.
const hardenEnv = "CREATOR_AGENT_HARDEN"

// superviseTTY re-execs this binary as a child that runs the interactive TUI, then guarantees the
// terminal is restored when the child exits for ANY reason — including Go fatal runtime errors
// (memory corruption, unexpected signal) and SIGKILL, which bypass every deferred cleanup and would
// otherwise leave the tty in raw + mouse-reporting mode (clicking then echoes raw SGR mouse reports
// like "0;53;25M" into the shell).
//
// The supervisor parent never enters raw mode itself, so it stays a healthy process able to emit the
// restore sequence no matter how violently the child dies. It returns true only after fully handling
// the child's lifecycle (the caller must stop); it returns false when this process is the child, when
// supervision is opted out, or when the mode is not the interactive TUI (headless / non-TTY paths
// never enter raw mode and need no supervisor), in which case the caller continues normally.
func superviseTTY() bool {
	// We are the supervised child, or the user opted out: run the TUI directly in-process.
	if os.Getenv(ttyGuardEnv) == "1" || os.Getenv(noSupervisorEnv) == "1" {
		return false
	}
	// Only the interactive TUI enters raw mode. Headless (positional prompt) and the dumb non-TTY
	// REPL never do, so they carry no tty-corruption risk and must not pay for an extra process.
	if len(flag.Args()) != 0 || !isTTY(os.Stdin) || !isTTY(os.Stdout) {
		return false
	}
	exe, err := os.Executable()
	if err != nil {
		// Cannot locate our own binary to re-exec: fall back to in-process execution. The main
		// goroutine recover() still covers ordinary panics; only fatal-error tty repair is lost.
		return false
	}

	cmd := exec.Command(exe)
	cmd.Args = os.Args // preserve argv[0] and all flags verbatim
	cmd.Env = append(os.Environ(), ttyGuardEnv+"=1")
	// Opt-in hardening: CREATOR_AGENT_HARDEN=1 turns on the runtime's clobberfree debug flag in the
	// child. It overwrites freed heap memory with poison so a use-after-free (the suspected cause of
	// the "found pointer to free object" / "found bad pointer in Go heap" crashes) faults at the
	// access site with a useful stack, instead of much later inside the GC. Modest overhead, so it is
	// off by default and best enabled while chasing a specific crash. GODEBUG must be set before the
	// runtime starts, which only the parent (pre-exec) can do for the child.
	if os.Getenv(hardenEnv) == "1" {
		cmd.Env = append(cmd.Env, mergeGodebug(os.Getenv("GODEBUG"), "clobberfree=1"))
	}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Start(); err != nil {
		// Fork failed (e.g. resource limits): degrade gracefully to in-process execution rather
		// than aborting the user's session.
		return false
	}

	// Forward termination signals to the child and never let them kill the supervisor before it has
	// restored the tty. While the child holds raw mode the terminal has ISIG disabled, so Ctrl+C is
	// delivered as a byte (not a signal) and handled by the child; forwarding matters mainly for the
	// brief startup/teardown windows and for an external `kill`.
	sigCh := make(chan os.Signal, 8)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP, syscall.SIGQUIT)
	done := make(chan struct{})
	go func() {
		for {
			select {
			case s := <-sigCh:
				if cmd.Process != nil {
					_ = cmd.Process.Signal(s)
				}
			case <-done:
				return
			}
		}
	}()

	waitErr := cmd.Wait()
	close(done)
	signal.Stop(sigCh)

	// Unconditionally restore the terminal. The sequence is idempotent: on a clean child exit (which
	// already restored the tty) re-emitting it is a harmless no-op; on a fatal-error exit (every
	// defer skipped) this is the only thing that repairs the terminal.
	_ = tui.RestoreTerminal(os.Stdout)

	os.Exit(childExitCode(waitErr))
	return true // unreachable: os.Exit does not return
}

// mergeGodebug returns a "GODEBUG=..." env entry that preserves any existing settings and appends
// add (comma-separated, as the runtime expects). add wins if the same key is already present, since
// it is listed last.
func mergeGodebug(existing, add string) string {
	if existing == "" {
		return "GODEBUG=" + add
	}
	return "GODEBUG=" + existing + "," + add
}

// childExitCode maps the child's wait result to an exit code, mirroring shell conventions: the
// child's own code on normal exit, 128+signal when it was killed by a signal, and 1 otherwise.
func childExitCode(waitErr error) int {
	if waitErr == nil {
		return 0
	}
	var ee *exec.ExitError
	if errors.As(waitErr, &ee) {
		if ws, ok := ee.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
			return 128 + int(ws.Signal())
		}
		if code := ee.ExitCode(); code >= 0 {
			return code
		}
	}
	return 1
}
