package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/mattn/go-runewidth"
	"github.com/skys-mission/creator-agent/cmd/creator-agent/tui/i18n"
)

// ErrTerminalUnavailable indicates the interactive TUI could not initialize the terminal (raw mode /
// alternate screen). The caller degrades to a non-full-screen mode instead of aborting.
var ErrTerminalUnavailable = errors.New("interactive terminal unavailable")

// Run starts the minimal rebuild shell (bare page + /model-new) in the current terminal. lang is
// the display language ("en", "zh"; empty = English). Blocks until the user quits.
func Run(lang string) error {
	i18n.SetLang(i18n.ParseLang(lang))

	// Signals -> cancel (Ctrl+C is handled in raw mode via KeyCtrlC; this also catches SIGINT
	// and SIGTERM from external signals or terminal UI so the cleanup path runs).
	sigCtx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	a := &App{
		ctx:    sigCtx,
		quitCh: make(chan struct{}),
		events: make(chan any, 64),
	}

	// Width model: the renderer sizes every cell with go-runewidth. Its DefaultCondition uses
	// EastAsianWidth=false unless RUNEWIDTH_EASTASIAN is present, but CJK terminals often render
	// ambiguous-width characters (box-drawing ─ │, symbols ● ○ □, etc.) at 2 columns. We align the
	// width model to the terminal: if the locale is CJK or RUNEWIDTH_EASTASIAN=1, treat ambiguous
	// chars as width 2. The condition is assigned to the package-level widthCond and restored on exit.
	prevCond := widthCond
	widthCond = configureRunewidth()
	defer func() { widthCond = prevCond }()

	// An unrecoverable panic raised inside a library goroutine bypasses every deferred recover()
	// and its output gets wiped when the TUI leaves the alternate screen. Pinning fd 2 to a file
	// preserves the full stack for post-mortem analysis.
	_ = RedirectStderrToFile()

	// Absolute per-cell cursor positioning (each emitted cell is preceded by a CUP) eliminates the
	// CJK-terminal misalignment that cursor-advance-based renderers suffer.
	term, err := OpenTerminal()
	if err != nil {
		return fmt.Errorf("%w: %v", ErrTerminalUnavailable, err)
	}
	defer term.Close()
	// Last-resort signal cleanup: if the main goroutine is stuck in a long render or a panic
	// prevents the deferred term.Close() from running, the first SIGINT/SIGTERM waits briefly for
	// the normal exit path, then restores the terminal as a fallback. The delay avoids racing with
	// the normal exit path, which would otherwise write to the main screen after alt screen has
	// been disabled.
	go func() {
		defer func() {
			if r := recover(); r != nil {
				logCrash(r)
			}
		}()
		<-sigCtx.Done()
		select {
		case <-time.After(100 * time.Millisecond):
			term.Close()
		}
	}()
	// Recover from any panic in the main goroutine after terminal cleanup runs, so the terminal
	// is always restored to a usable state even if render/input logic crashes.
	defer func() {
		if r := recover(); r != nil {
			logCrash(r)
		}
	}()
	w, h := term.Size()
	a.screen = NewScreen(term, w, h)
	a.term = term

	runLoop(a)
	return nil
}

// configureRunewidth aligns the cell-width model used by the renderer with what CJK terminals
// actually paint: when the locale is CJK (ja/ko/zh) or the user sets RUNEWIDTH_EASTASIAN=1,
// ambiguous-width characters (box-drawing ─ │, symbols ● ○ □ ■, etc.) count as 2 columns.
func configureRunewidth() *runewidth.Condition {
	cond := runewidth.DefaultCondition
	if v := os.Getenv("RUNEWIDTH_EASTASIAN"); v != "" {
		// Explicit override: honor the user's choice ("1"/"true" -> width 2, else width 1).
		cond.EastAsianWidth = (v == "1" || strings.EqualFold(v, "true"))
		return cond
	}
	// No explicit override: infer from locale. CJK locales render ambiguous chars at width 2.
	if isCJKLocale() {
		cond.EastAsianWidth = true
	}
	return cond
}

// isCJKLocale reports whether the current locale (LC_ALL/LC_CTYPE/LANG) is a CJK (Chinese,
// Japanese, Korean) locale, where terminals conventionally render ambiguous-width characters at
// 2 columns.
func isCJKLocale() bool {
	for _, key := range []string{"LC_ALL", "LC_CTYPE", "LANG"} {
		loc := strings.ToLower(os.Getenv(key))
		if loc == "" || loc == "c" || loc == "posix" {
			continue
		}
		if strings.HasPrefix(loc, "zh") || strings.HasPrefix(loc, "ja") || strings.HasPrefix(loc, "ko") {
			return true
		}
	}
	return false
}
