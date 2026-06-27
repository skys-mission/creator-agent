//go:build windows

package terminal

import (
	"fmt"
	"io"
	"os"
)

type termios struct{}

func Open() (*Terminal, error) {
	return nil, fmt.Errorf("TUI mode is not supported on Windows; please use the REPL mode")
}

func (t *Terminal) Close() {}

// Restore is a no-op on Windows: TUI mode is unsupported here (Open returns an error), so there
// are no private modes to reset. Mirrors terminal_unix.Restore for cross-platform callers.
func Restore(w io.Writer) error { return nil }

func (t *Terminal) enterRaw() error       { return fmt.Errorf("not supported on windows") }
func (t *Terminal) leaveRaw() error       { return nil }
func (t *Terminal) querySize() (int, int) { return 80, 24 }

func NewStderrTerminal() *Terminal {
	return &Terminal{tty: os.Stderr}
}
