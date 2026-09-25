package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"
	"time"

	"github.com/skys-mission/creator-agent/cmd/creator-agent/diag"
	"github.com/skys-mission/creator-agent/contract"
)

// inputEvent wraps a decoded terminal Event (key).
type inputEvent struct{ ev Event }

// errMsg surfaces an infrastructure failure (a pump/reader panic) in the notice line.
type errMsg struct{ err error }

// runLoop starts the input pump and runs the event loop until the App quits.
func runLoop(a *App) {
	w, h := a.screen.Size()
	a.width, a.height = w, h
	a.input.SetWidth(maxi(10, w-4))

	// Pump raw input from stdin on a goroutine, decoding escape sequences into Events. The reader
	// goroutine blocks on stdin; it exits on EOF/error and on quit when a byte is already pending,
	// but a quiescent stdin leaves it parked in the read syscall. This is a benign leaked goroutine:
	// it holds no resource the process needs at exit, and the process ends shortly after quit anyway.
	go func() {
		defer recoverFromGoroutine(a, "input pump")
		ch := make(chan Event, 32)
		go func() {
			defer recoverFromGoroutine(a, "input reader")
			PumpInput(stdinReader(), ch, a.quitCh)
		}()
		for ev := range ch {
			// Drop mouse events entirely: the bare shell has nothing to point at or scroll.
			if _, ok := ev.(*EventMouse); ok {
				continue
			}
			if ek, ok := ev.(*EventKey); ok && ek == nil {
				continue
			}
			select {
			case a.events <- inputEvent{ev: ev}:
			case <-a.quitCh:
				return
			}
		}
	}()

	// Render the initial frame (full Sync).
	a.forceRender = true
	render(a)

	// Event loop: single mutator. Sources: quit, internal events, terminal resize (SIGWINCH).
	var resizeCh <-chan struct{}
	if a.term != nil {
		resizeCh = a.term.ResizeCh()
	}
	for {
		select {
		case <-a.quitCh:
			return
		case <-a.ctx.Done():
			// Signal received (SIGINT/SIGTERM) or parent context cancelled: shut down cleanly.
			if !a.quitting {
				a.quitting = true
				close(a.quitCh)
			}
			return
		case <-resizeCh:
			if a.term != nil {
				w, h := a.term.Size()
				a.screen.SetSize(w, h)
				a.width, a.height = w, h
				a.input.SetWidth(maxi(10, w-4))
			}
			a.forceRender = true
			render(a)
		case ev := <-a.events:
			handleEventSafe(a, ev)
			if a.quitting {
				close(a.quitCh)
				return
			}
			render(a)
		}
	}
}

// handleEventSafe dispatches one event with a panic recover so a handler/render bug does not crash
// the process. The panic is captured to the crash log and surfaced as a notice.
func handleEventSafe(a *App, ev any) {
	defer func() {
		if r := recover(); r != nil {
			diag.Trace("PANIC event-loop: %v", r)
			logCrash(r)
			a.notice = fmt.Sprintf("internal error (see ~/.creator/tui-crash.log): %v", r)
		}
	}()
	handleEvent(a, ev)
}

// recoverFromGoroutine is the shared recover for background goroutines (input pump, input reader).
// It captures the panic to the crash log and surfaces a non-fatal error to the event loop, so a
// panic in a pump/reader never crashes the process. No-op when no panic occurred.
func recoverFromGoroutine(a *App, where string) {
	if r := recover(); r != nil {
		diag.Trace("PANIC %s: %v", where, r)
		logCrash(r)
		send(a, errMsg{err: fmt.Errorf("%s panicked (see ~/.creator/tui-crash.log): %v", where, r)})
	}
}

func handleEvent(a *App, msg any) {
	switch m := msg.(type) {
	case inputEvent:
		handleTcellEvent(a, m.ev)
	case errMsg:
		a.notice = m.err.Error()
	}
}

// send pushes a message to the App event queue (never blocks past quit).
func send(a *App, msg any) {
	select {
	case a.events <- msg:
	case <-a.quitCh:
	}
}

// logCrash writes the panic cause + stack to ~/.creator/tui-crash.log for diagnosis.
func logCrash(r interface{}) {
	logCrashToFile(r, debug.Stack())
}

func logCrashToFile(r interface{}, stack []byte) {
	dir, err := contract.LogDir()
	if err != nil {
		// No home dir to write to: still surface the crash on stderr so it is not lost entirely.
		fmt.Fprintf(os.Stderr, "creator-agent crash (no home dir, could not write log): %v\n%s\n", r, stack)
		return
	}
	_ = os.MkdirAll(dir, 0o755)
	msg := fmt.Sprintf("time: %s\npanic: %v\n\n%s\n", time.Now().Format(time.RFC3339), r, stack)
	// Overwrite mode: keep only the most recent crash for quick diagnosis.
	// The user is pointed here on crash, so a write failure must not be silent: mirror to stderr so
	// the diagnostic survives even when the file cannot be written (read-only home, full disk, etc.).
	if werr := os.WriteFile(filepath.Join(dir, "tui-crash.log"), []byte(msg), 0o600); werr != nil {
		fmt.Fprintf(os.Stderr, "creator-agent crash (could not write tui-crash.log under ~/.creator: %v):\n%s\n", werr, msg)
	}
}

// LogCrashToDisk writes a panic value and its stack to the crash log. Exported so
// the top-level main goroutine recover shares the same diagnostic path as the
// TUI's per-goroutine recovers, regardless of where the panic propagated from.
func LogCrashToDisk(r interface{}, stack []byte) {
	logCrashToFile(r, stack)
}
