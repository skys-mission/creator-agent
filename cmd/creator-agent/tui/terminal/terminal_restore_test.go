//go:build darwin || linux

package terminal_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/skys-mission/creator-agent/cmd/creator-agent/tui/terminal"
)

// TestRestoreDisablesMouseBeforeAltScreen guards the crash-recovery regression where the
// panic-deferred terminal restore omitted the mouse-tracking disable. With mouse capture (private
// modes 1000/1006) left on after a crash, the terminal keeps emitting SGR mouse reports into the
// parent shell, which renders as raw CSI <btn;col;row M/m garbage. The restore sequence must
// disable mouse tracking BEFORE leaving the alternate screen.
func TestRestoreDisablesMouseBeforeAltScreen(t *testing.T) {
	var buf bytes.Buffer
	if err := terminal.Restore(&buf); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	out := buf.String()
	for _, want := range []string{"\x1b[?1006l", "\x1b[?1000l", "\x1b[?25h", "\x1b[?1049l"} {
		if !strings.Contains(out, want) {
			t.Errorf("restore sequence missing %q; got %q", want, out)
		}
	}
	mouseOff := strings.Index(out, "\x1b[?1006l")
	altOff := strings.Index(out, "\x1b[?1049l")
	if mouseOff < 0 || altOff < 0 || mouseOff > altOff {
		t.Errorf("mouse disable must precede alt-screen exit; got %q", out)
	}
}
