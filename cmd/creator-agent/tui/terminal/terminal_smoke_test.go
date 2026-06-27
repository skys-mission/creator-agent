package terminal_test

import (
	"testing"

	"github.com/skys-mission/creator-agent/cmd/creator-agent/tui/terminal"
)

func TestOpenWithoutTTYReturnsError(t *testing.T) {
	// When stdin is not a TTY, Open should fail gracefully rather than panic.
	term, err := terminal.Open()
	if err == nil {
		term.Close()
		t.Skip("stdin is a TTY; skipping non-TTY smoke test")
	}
}
