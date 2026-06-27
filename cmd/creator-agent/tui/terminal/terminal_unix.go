//go:build darwin || linux

package terminal

import (
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"golang.org/x/sys/unix"
)

// restoreModes is the exact inverse of the private-mode sequence enabled in Open(): it disables
// SGR (1006) and X10 (1000) mouse tracking, then re-shows the cursor (25) and leaves the alternate
// screen (1049). Mouse tracking must be disabled before exiting the alt screen; otherwise the
// terminal keeps emitting mouse reports while the main screen is restored, leaking raw
// CSI <btn;col;row M/m bytes into the parent shell. Shared by Close() and Restore() so the
// process-level panic recover reuses the identical bytes and never drifts out of sync.
const restoreModes = "\x1b[?1006l\x1b[?1000l\x1b[?25h\x1b[?1049l"

// Restore writes the terminal-mode restore sequence to w. It is the stateless inverse of Open()'s
// mode setup, used by the process-level panic recover (main.recoverMain) as a last-resort cleanup
// when the TUI's own deferred Close() may not have run. Safe on a plain pipe/file stdout: the
// bytes are inert there, and terminals that never entered the modes simply ignore them.
func Restore(w io.Writer) error {
	_, err := io.WriteString(w, restoreModes)
	return err
}

// Open puts the terminal into raw mode, enables the alternate screen, mouse tracking, and the
// input parser, then starts watching for resize events.
func Open() (*Terminal, error) {
	tty := os.Stdout
	t := &Terminal{tty: tty}
	if err := t.enterRaw(); err != nil {
		return nil, err
	}
	// 1049 = alt screen; 25 = hide cursor. Mouse tracking: 1000 = X10 (wheel/button press);
	// 1006 = SGR encoding (CSI < btn;col;row M/m). We omit 1002 (button-event/drag) and 1003
	// (any-event/hover) to avoid motion-event floods; 1000 is enough for wheel scrolling.
	// A legacy X10 decoder is also present for terminals that ignore SGR encoding.
	if err := t.writeRaw("\x1b[?1049h\x1b[?25l\x1b[?1000h\x1b[?1006h\x1b[2J\x1b[H"); err != nil {
		_ = t.leaveRaw()
		return nil, fmt.Errorf("init terminal modes: %w", err)
	}
	w, h := t.querySize()
	t.w.Store(int32(w))
	t.h.Store(int32(h))
	t.resize = make(chan struct{}, 1)
	t.stopSz = make(chan struct{})
	go t.watchSize()
	return t, nil
}

// Close restores the terminal to its original state. It is safe to call more than once.
func (t *Terminal) Close() {
	if !t.closed.CompareAndSwap(false, true) {
		return
	}
	if t.stopSz != nil {
		close(t.stopSz)
		t.stopSz = nil
	}
	// Order matters: disable mouse tracking before leaving the alt screen (see restoreModes) so the
	// terminal stops emitting mouse bytes while the main screen is restored.
	if err := t.writeRaw(restoreModes); err != nil {
		fmt.Fprintf(os.Stderr, "warn: failed to restore terminal modes: %v\n", err)
	}
	if err := t.leaveRaw(); err != nil {
		fmt.Fprintf(os.Stderr, "warn: failed to restore terminal: %v\n", err)
	}
}

func (t *Terminal) watchSize() {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGWINCH)
	for {
		select {
		case <-ch:
			w, h := t.querySize()
			if w > 0 && h > 0 {
				t.w.Store(int32(w))
				t.h.Store(int32(h))
				select {
				case t.resize <- struct{}{}:
				default:
				}
			}
		case <-t.stopSz:
			signal.Stop(ch)
			return
		}
	}
}

func (t *Terminal) enterRaw() error {
	fd := int(os.Stdin.Fd())
	ts, err := unix.IoctlGetTermios(fd, ioctlGet)
	if err != nil {
		return err
	}
	t.orig = *ts
	raw := *ts
	raw.Iflag &^= iflagICRNL | iflagIXON | iflagISTRIP
	raw.Oflag &^= oflagOPOST
	raw.Lflag &^= lflagICANON | lflagECHO | lflagIEXTEN | lflagISIG
	raw.Cflag &^= cflagCSIZE | cflagPARENB
	raw.Cflag |= cflagCS8
	raw.Cc[vminIdx] = 1
	raw.Cc[vtimeIdx] = 0
	return unix.IoctlSetTermios(fd, ioctlSet, &raw)
}

func (t *Terminal) leaveRaw() error {
	return unix.IoctlSetTermios(int(os.Stdin.Fd()), ioctlSet, &t.orig)
}

func (t *Terminal) querySize() (int, int) {
	ws, err := unix.IoctlGetWinsize(int(os.Stdout.Fd()), unix.TIOCGWINSZ)
	if err != nil {
		return 80, 24
	}
	return int(ws.Col), int(ws.Row)
}
