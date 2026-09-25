package tui

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/skys-mission/creator-agent/cmd/creator-agent/tui/i18n"
)

// Slash-command dispatch for the minimal rebuild shell. Only /model-new is registered; the rest of
// the command surface returns as its runtime is rebuilt (see docs/tui-cut.md).

// submitInput handles one Enter. Slash commands run locally; free text is acknowledged with a
// notice — the agent runtime lands with the P2 rebuild, so the draft is kept (not consumed).
func submitInput(a *App) {
	closeCompletions(a) // never leave a stale hint menu behind after a submit
	text := strings.TrimSpace(a.input.Value())
	if text == "" {
		return
	}
	if strings.HasPrefix(text, "/") {
		if handleSlashCommand(a, text) {
			a.input.SetValue("")
			return
		}
		// A command-like token that is not registered is a typo, not a prompt: intercept it with a
		// hint instead of letting it look like ordinary text. Non-command-like slashes (e.g. an
		// absolute path "/etc/hosts") fall through as plain text.
		if isSlashCommandLike(text) {
			name, _, _ := strings.Cut(text, " ")
			a.notice = fmt.Sprintf(i18n.T("msg.unknown_command"), name)
			a.input.SetValue("")
			return
		}
	}
	a.notice = i18n.T("msg.runtime_not_ready")
}

type commandEntry struct {
	name    string
	handler func(a *App, arg string) bool
}

// commandRegistry is the ordered slash-command list. During the rebuild only /model exists: it
// opens the second-level menu that owns all model-object management (create / delete).
var commandRegistry = []commandEntry{
	{name: "/model", handler: func(a *App, _ string) bool {
		openModelMenu(a)
		return true
	}},
}

func isSlashCommandLike(text string) bool {
	if len(text) < 2 || text[0] != '/' {
		return false
	}
	name, _, _ := strings.Cut(text, " ")
	for _, r := range name[1:] {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '-' && r != '_' {
			return false
		}
	}
	return true
}

func findCommand(name string) *commandEntry {
	for i := range commandRegistry {
		if commandRegistry[i].name == name {
			return &commandRegistry[i]
		}
	}
	return nil
}

func handleSlashCommand(a *App, text string) bool {
	cmd, arg, _ := strings.Cut(text, " ")
	entry := findCommand(cmd)
	if entry == nil {
		return false
	}
	return entry.handler(a, arg)
}
