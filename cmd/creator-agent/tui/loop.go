package tui

import (
	"context"
	"fmt"
	"runtime/debug"
	"time"

	"github.com/skys-mission/creator-agent/cmd/creator-agent/diag"
	"github.com/skys-mission/creator-agent/core"
)

type (
	eventMsg struct {
		ev  core.Event
		gen uint64
	}
	streamEndMsg struct{ gen uint64 }
	errMsg       struct {
		err error
		gen uint64 // 0 = infrastructure (input pump, spinner); non-zero must match streamGen
	}
	spinnerTickMsg struct{}
)

func (e errMsg) Error() string { return e.err.Error() }

// runLoop starts the consumer goroutines and runs the event loop until the App quits.
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
			// Filter mouse events: only wheel up/down are useful for the TUI; drop button
			// press/release/drag to avoid event storms when the user moves or clicks the mouse.
			if em, ok := ev.(*EventMouse); ok {
				if em == nil {
					continue
				}
				if em.Button() != MouseWheelUp && em.Button() != MouseWheelDown {
					continue
				}
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
	go pumpSpinner(a)

	// Load the current session's persisted history into the message area before the first frame, so
	// a resumed session (-c/--continue) shows its prior conversation instead of the home screen.
	// No-op for a fresh session with no stored history.
	loadCurrentSessionHistory(a)

	// TUI -r/--resume: open the session picker before the first frame so the user can pick which
	// session to resume on startup. No-ops when the store has no sessions (falls back to the fresh
	// session). Width is already set above, so initPicker can size its query field correctly.
	if a.resumeOnStart {
		openSessionPicker(a)
		a.resumeOnStart = false // one-shot: only on startup, not on resize
	}

	// Render the initial frame (full Sync).
	a.forceRender = true
	render(a)

	// Event loop: single mutator.
	// Three sources: quit, internal events (input/agent/spinner), and terminal resize (SIGWINCH).
	var resizeCh <-chan struct{}
	if a.term != nil {
		resizeCh = a.term.ResizeCh()
	}
	for {
		select {
		case <-a.quitCh:
			return
		case <-a.rt.ctx.Done():
			// Signal received (SIGINT/SIGTERM) or parent context cancelled: shut down cleanly.
			// term.Close() is deferred in the caller, so we only need to stop the loop and close
			// the quit channel so background goroutines exit.
			if !a.quitting {
				a.quitting = true
				close(a.quitCh)
			}
			return
		case <-resizeCh:
			// Terminal resized: update Screen geometry + input wrap width, then full repaint.
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
			// (full-screen, Sync-based) redraw on idle spinner ticks to avoid needless repaints.
			if _, isTick := ev.(spinnerTickMsg); isTick && !spinnerActive(a) {
				continue
			}
			// Throttle mouse wheel repaints: a single scroll gesture can produce dozens of events,
			// and re-rendering a large message viewport on every event makes the TUI lag. Coalesce
			// wheel events within 50ms into a single redraw.
			if ie, ok := ev.(inputEvent); ok {
				if _, isMouse := ie.ev.(*EventMouse); isMouse {
					if time.Since(a.lastMouseRender) < 50*time.Millisecond {
						continue
					}
					a.lastMouseRender = time.Now()
				}
			}
			render(a)
		}
	}
}

// spinnerActive reports whether the spinner is currently shown (non-idle status, or an MCP server
// toggle is in flight with the picker open showing the animated "connecting…" row), so that idle
// spinner ticks can skip a redraw.
func spinnerActive(a *App) bool {
	if a.status == statusThinking || a.status == statusRunningTool || a.status == statusCompacting {
		return true
	}
	return a.mcpPicker.open && a.mcpPicker.toggling != ""
}

func pumpSpinner(a *App) {
	defer recoverFromGoroutine(a, "spinner")
	t := time.NewTicker(100 * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-t.C:
			select {
			case a.events <- spinnerTickMsg{}:
			case <-a.quitCh:
				return
			}
		case <-a.quitCh:
			return
		}
	}
}

// inputEvent wraps an input Event (key/resize/mouse).
type inputEvent struct{ ev Event }

// handleEventSafe dispatches one event with a panic recover so a handler/render bug does not crash
// the process. The panic is captured to the crash log and surfaced as an error in the UI.
func handleEventSafe(a *App, ev any) {
	defer func() {
		if r := recover(); r != nil {
			diag.Trace("PANIC event-loop: %v", r)
			logCrash(r)
			a.err = fmt.Errorf("internal error (see ~/.creator/tui-crash.log): %v", r)
			a.status = statusError
		}
	}()
	handleEvent(a, ev)
}

// recoverFromGoroutine is the shared recover for background goroutines that have no per-call
// handler (input pump, input reader, spinner). It captures the panic to the crash log and surfaces
// a non-fatal error to the event loop, so a panic in a pump/reader never crashes the process.
// The where label identifies the goroutine in the surfaced error. It is a no-op when no panic
// occurred (deferred call always runs).
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
	case eventMsg:
		// Drop events from a cancelled or superseded stream so a stale goroutine cannot mutate the
		// current turn (e.g. after an Esc/Ctrl+C interrupt followed by a new submit).
		if m.gen != a.streamGen {
			return
		}
		handleCoreEvent(a, m.ev)
	case streamEndMsg:
		if m.gen != a.streamGen {
			return
		}
		handleStreamEnd(a)
	case errMsg:
		if m.gen != 0 && m.gen != a.streamGen {
			return
		}
		a.err = m.err
		a.status = statusError
	case spinnerTickMsg:
		a.spinnerFrame++
	case switchAgentMsg:
		a.rt.ag = m.ag
		a.status = statusIdle
	case titleGeneratedMsg:
		applyTitle(a, m)
	case mcpToggleDoneMsg:
		applyMCPToggleDone(a, m)
	case compactDoneMsg:
		applyCompactDone(a, m)
	case askMsg:
		// Defense: if a previous ask is somehow still pending, deny it first to leak-proof its replyCh.
		if a.asking != nil {
			sendReply(a.asking.replyCh, false)
		}
		a.asking = &m
		a.approveIdx = 0
		diag.Trace("approval.ask tool=%s", m.toolName)
	}
}

// startStream launches a goroutine that drains ag.Stream's event channel and forwards events.
//
// Each turn runs under its own cancelable context (a child of a.rt.ctx) so a single turn can be
// interrupted (Esc/Ctrl+C) without tearing down the app. The cancel is published to a.streamCancel
// and the turn is tagged with a fresh streamGen; events carry that gen so a cancelled/superseded
// stream's late events are dropped by the event loop. Runs on the event-loop goroutine.
func startStream(a *App, in core.StreamInput) {
	diag.Trace("stream.start")
	a.streamGen++
	gen := a.streamGen
	ctx, cancel := context.WithCancel(a.rt.ctx)
	a.streamCancel = cancel
	go func() {
		defer cancel() // release the per-turn context when the goroutine exits
		defer func() {
			if r := recover(); r != nil {
				diag.Trace("PANIC stream: %v", r)
				logCrash(r)
				send(a, errMsg{err: fmt.Errorf("stream panicked: %v\n%s", r, debug.Stack()), gen: gen})
			}
		}()
		ch, err := a.rt.ag.Stream(ctx, in)
		if err != nil {
			send(a, errMsg{err: err, gen: gen})
			send(a, streamEndMsg{gen: gen})
			return
		}
		for {
			select {
			case <-ctx.Done():
				send(a, streamEndMsg{gen: gen})
				return
			case ev, ok := <-ch:
				if !ok {
					send(a, streamEndMsg{gen: gen})
					return
				}
				send(a, eventMsg{ev: ev, gen: gen})
			}
		}
	}()
}

// send pushes a message to the App event queue (non-blocking, never panics on a closed quitCh).
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
