package tui

import (
	"context"
	"time"
)

// App is the whole UI state of the minimal rebuild shell: the terminal, the input box, a one-line
// notice, and the /model-new dialog. Everything else (message viewport, pickers, approvals,
// sessions, markdown) was cut in the 2026-09 rebuild — see docs/tui-cut.md for what was removed
// and how to bring it back.
//
// State discipline (docs/tui.md §6): the event-loop goroutine is the only mutator of App.
// Background goroutines communicate exclusively through the events channel.
type App struct {
	screen *Screen
	term   *Terminal

	events chan any
	quitCh chan struct{}
	ctx    context.Context // cancelled on SIGINT/SIGTERM so the loop exits cleanly

	width, height int

	input inputBuffer

	notice string // one line shown above the input box; empty = nothing

	// Inline command hints ("命令行提示"): the slash-command suggestion menu shown while the input
	// is a "/" query. completions is the (fuzzy-filtered) candidate list; compIdx/compScroll are the
	// selection and its scroll offset (page size compPageSize).
	completions []string
	compIdx     int
	compScroll  int

	modelForm modelFormState
	modelMenu modelMenuState
	models    modelStore

	quitting    bool
	forceRender bool // next render does a full Sync (overlay open/close, resize, first frame)
	lastCtrlC   time.Time

	curBg Color // region background for withRegionBg compositing
}
