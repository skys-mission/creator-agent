package tui

import (
	"strings"
	"time"

	"github.com/skys-mission/creator-agent/cmd/creator-agent/tui/i18n"
)

// handleTcellEvent dispatches one decoded terminal event. Mouse events are already dropped by the
// input pump; keys and resizes are all the bare shell reacts to (resize via the loop's ResizeCh).
func handleTcellEvent(a *App, ev Event) {
	if ek, ok := ev.(*EventKey); ok {
		if ek == nil {
			return
		}
		handleKey(a, ek)
	}
}

// handleKey routes one key press. While the /model-new dialog is open it consumes every key.
// Otherwise: quit on Ctrl+D or double Ctrl+C (always available), the command-hint menu consumes
// keys while open, Esc clears the draft (Esc never quits — a stray press must not cost the draft
// or the process), Tab completes, Alt+Enter inserts a newline, Enter submits.
func handleKey(a *App, e *EventKey) {
	if a.modelForm.open {
		handleModelFormKey(a, e)
		return
	}
	if a.modelMenu.open {
		handleModelMenuKey(a, e)
		return
	}
	k := e.Key()

	// Quit keys are routed before everything else so the hint menu can never trap the exit.
	switch k {
	case KeyCtrlD:
		a.quitting = true
		return
	case KeyCtrlC:
		if time.Since(a.lastCtrlC) < 2*time.Second {
			a.quitting = true
			return
		}
		a.lastCtrlC = time.Now()
		a.notice = i18n.T("msg.press_ctrlc_again")
		return
	}

	if len(a.completions) > 0 {
		handleCompletionKey(a, k, e.Rune())
		return
	}

	switch k {
	case KeyEsc:
		if a.input.Value() != "" {
			a.input.SetValue("")
		}
		return
	case KeyEnter:
		if e.Modifiers()&ModAlt != 0 {
			a.input.InsertString("\n")
			return
		}
		submitInput(a)
		return
	case KeyTab:
		// Tab-completion on a "/" query: exactly one candidate fills the input, several open the
		// hint menu. Non-command input is left alone.
		v := a.input.Value()
		if strings.HasPrefix(v, "/") {
			cands := matchCommands(v)
			if len(cands) == 1 {
				a.input.SetValue(cands[0])
			} else if len(cands) > 1 {
				a.completions = cands
				a.compIdx = 0
				a.compScroll = 0
			}
		}
		return
	}
	if k == KeyRune {
		if r := e.Rune(); r != 0 {
			a.input.InsertRune(r)
		}
	} else {
		editTextBuffer(k, e.Rune(), &a.input)
	}
	refineCompletions(a)
}
