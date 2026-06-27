package tui

// render_dump_test.go renders a full representative session and asserts the Claude-style layout's
// key invariants in one place: user + assistant content present, the ❯ prompt at the bottom, and no
// legacy top status bar / ASCII borders. It also logs the decoded frame for visual diagnosis via:
// go test -v -run TestFullConversationRenders ./cmd/creator-agent/tui/

import (
	"strings"
	"testing"
)

func TestFullConversationRenders(t *testing.T) {
	a, sim := newAppWithSim(t, 90, 24)
	a.messages = append(a.messages, msgBlock{kind: kindUser})
	a.messages[0].content.WriteString("Hello, can you fix the bug in main.go?")
	mb := msgBlock{kind: kindAssistant}
	mb.content.WriteString("Sure. Let me **read** the file then `edit` it.\n\n```go\nfmt.Println(\"hi\")\n```")
	mb.finalize(a.rt.md)
	a.messages = append(a.messages, mb)
	render(a)
	screen := dumpScreen(sim)
	t.Logf("90x24 SESSION:\n%s", screen)

	if !strings.Contains(screen, "Hello, can you fix") {
		t.Errorf("user content not rendered")
	}
	if !strings.Contains(screen, "Sure. Let me") {
		t.Errorf("assistant content not rendered")
	}
	if !strings.Contains(screen, "fmt.Println") {
		t.Errorf("code block not rendered")
	}
	if !strings.Contains(screen, "❯") {
		t.Errorf("prompt ❯ missing")
	}
	if !strings.Contains(screen, "Tab agents") {
		t.Errorf("prompt footer hints missing")
	}
	if strings.Contains(screen, "creator-agent -") {
		t.Errorf("top status bar should be removed")
	}
	if strings.Contains(screen, "----") {
		t.Errorf("legacy ASCII border should be removed")
	}
}
