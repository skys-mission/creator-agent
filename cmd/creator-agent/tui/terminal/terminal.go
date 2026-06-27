package terminal

import (
	"fmt"
	"os"
	"sync/atomic"
)

type Terminal struct {
	tty    *os.File
	orig   termios
	w, h   atomic.Int32
	stopSz chan struct{}
	resize chan struct{}
	closed atomic.Bool
}

func (t *Terminal) ResizeCh() <-chan struct{} { return t.resize }

func (t *Terminal) Write(p []byte) (int, error) {
	return t.tty.Write(p)
}

// writeRaw writes s to the TTY and returns an error if the write is incomplete.
// Used for control sequences where partial output would leave the terminal in an
// inconsistent state.
func (t *Terminal) writeRaw(s string) error {
	n, err := t.tty.WriteString(s)
	if err != nil {
		return err
	}
	if n != len(s) {
		return fmt.Errorf("short write: wrote %d of %d bytes", n, len(s))
	}
	return nil
}

func (t *Terminal) Size() (int, int) { return int(t.w.Load()), int(t.h.Load()) }
