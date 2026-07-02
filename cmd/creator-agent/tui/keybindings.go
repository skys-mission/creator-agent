package tui

import (
	"github.com/skys-mission/creator-agent/cmd/creator-agent/tui/i18n"
)

// keyBinding is one row in the which-key panel.
type keyBinding struct {
	key   string // display form, e.g. "Ctrl+P", "Enter", "Alt+Enter", "↑/↓"
	desc  string // short description of what it does
	group string // category label for grouping in the panel
}

var keyBindingGroupOrder = []string{
	i18n.T("key.group.editing"),
	i18n.T("key.group.navigation"),
	i18n.T("key.group.session"),
	i18n.T("key.group.commands"),
}

// keyBindings lists every single-key user-facing keybinding. Grouped logically; order within a
// group is the display order. Keep in sync with the switch in keys.go.
var keyBindings = []keyBinding{
	{key: "Enter", desc: i18n.T("key.desc.submit"), group: i18n.T("key.group.editing")},
	{key: "Alt+Enter", desc: i18n.T("key.desc.newline"), group: i18n.T("key.group.editing")},
	{key: "Backspace", desc: i18n.T("key.desc.backspace"), group: i18n.T("key.group.editing")},
	{key: "Ctrl+W", desc: i18n.T("key.desc.delete_word"), group: i18n.T("key.group.editing")},
	{key: "← / →", desc: i18n.T("key.desc.move_char"), group: i18n.T("key.group.editing")},
	{key: "Home / End", desc: i18n.T("key.desc.move_line_end"), group: i18n.T("key.group.editing")},
	{key: "/", desc: i18n.T("key.desc.slash_autocomplete"), group: i18n.T("key.group.editing")},
	{key: "@", desc: i18n.T("key.desc.mention_autocomplete"), group: i18n.T("key.group.editing")},

	{key: "PgUp / PgDn", desc: i18n.T("key.desc.page_scroll"), group: i18n.T("key.group.navigation")},
	{key: "Ctrl+U", desc: i18n.T("key.desc.half_page_up"), group: i18n.T("key.group.navigation")},
	{key: "Ctrl+N / Ctrl+P", desc: i18n.T("key.desc.jump_msg"), group: i18n.T("key.group.navigation")},
	{key: "Home", desc: i18n.T("key.desc.first_msg"), group: i18n.T("key.group.navigation")},
	{key: "End", desc: i18n.T("key.desc.latest_msg"), group: i18n.T("key.group.navigation")},
	{key: "↑ / ↓", desc: i18n.T("key.desc.history"), group: i18n.T("key.group.navigation")},

	{key: "/new", desc: i18n.T("key.desc.new_session"), group: i18n.T("key.group.session")},
	{key: "/sessions", desc: i18n.T("key.desc.switch_session"), group: i18n.T("key.group.session")},
	{key: "/clear", desc: i18n.T("key.desc.clear_history"), group: i18n.T("key.group.session")},

	{key: "Ctrl+P", desc: i18n.T("key.desc.command_palette"), group: i18n.T("key.group.commands")},
	{key: "Ctrl+Alt+K", desc: i18n.T("key.desc.keybindings_panel"), group: i18n.T("key.group.commands")},
	{key: "Ctrl+T", desc: i18n.T("key.desc.cycle_thinking"), group: i18n.T("key.group.commands")},
	{key: "Shift+Tab", desc: i18n.T("key.desc.cycle_mode"), group: i18n.T("key.group.commands")},
	{key: "Ctrl+E", desc: i18n.T("key.desc.expand_tool"), group: i18n.T("key.group.commands")},
	{key: "Ctrl+Y", desc: i18n.T("key.desc.copy_reply"), group: i18n.T("key.group.commands")},
	{key: "Esc / Ctrl+C", desc: i18n.T("key.desc.interrupt"), group: i18n.T("key.group.commands")},
	{key: "Ctrl+D", desc: i18n.T("key.desc.quit"), group: i18n.T("key.group.commands")},
	{key: "/help", desc: i18n.T("key.desc.help"), group: i18n.T("key.group.commands")},
	{key: "/models", desc: i18n.T("key.desc.switch_model"), group: i18n.T("key.group.commands")},
	{key: "/mode", desc: i18n.T("key.desc.switch_mode"), group: i18n.T("key.group.commands")},
	{key: "/sandbox", desc: i18n.T("key.desc.toggle_sandbox"), group: i18n.T("key.group.commands")},
	{key: "/mcps", desc: i18n.T("key.desc.toggle_mcp"), group: i18n.T("key.group.commands")},
	{key: "/themes", desc: i18n.T("key.desc.switch_theme"), group: i18n.T("key.group.commands")},
	{key: "/diff <file>", desc: i18n.T("key.desc.view_diff"), group: i18n.T("key.group.commands")},
}

func keyBindingGroups() []string {
	return keyBindingGroupOrder
}

func bindingsByGroup(group string) []keyBinding {
	var out []keyBinding
	for _, b := range keyBindings {
		if b.group == group {
			out = append(out, b)
		}
	}
	return out
}

// leaderBinding is one opencode-style <ctrl+x> sequence: the key pressed after Ctrl+X, a short
// name, a description, and the action to run. The action runs on the event-loop goroutine (leader
// dispatch happens in handleKey), so it may touch App state directly.
type leaderBinding struct {
	key    rune // lowercase; uppercase is accepted and folded
	name   string
	desc   string
	action func(a *App)
}

// leaderBindings is the dispatch table for <ctrl+x> sequences, mirroring opencode's leader map
// (adapted to this project's feature set). Order is the display order in the which-key panel.
var leaderBindings = []leaderBinding{
	{key: 'm', name: i18n.T("leader.name.model"), desc: i18n.T("leader.desc.model"), action: func(a *App) { handleSlashCommand(a, "/models") }},
	{key: 'a', name: i18n.T("leader.name.agent"), desc: i18n.T("leader.desc.agent"), action: func(a *App) { handleSlashCommand(a, "/agents") }},
	{key: 't', name: i18n.T("leader.name.theme"), desc: i18n.T("leader.desc.theme"), action: func(a *App) { handleSlashCommand(a, "/themes") }},
	{key: 'v', name: i18n.T("leader.name.variant"), desc: i18n.T("leader.desc.variant"), action: func(a *App) { handleSlashCommand(a, "/variants") }},
	{key: 'o', name: i18n.T("leader.name.mode"), desc: i18n.T("leader.desc.mode"), action: func(a *App) { handleSlashCommand(a, "/modes") }},
	{key: 'p', name: i18n.T("leader.name.mcp"), desc: i18n.T("leader.desc.mcp"), action: func(a *App) { handleSlashCommand(a, "/mcps") }},
	{key: 'n', name: i18n.T("leader.name.new"), desc: i18n.T("leader.desc.new"), action: func(a *App) { handleSlashCommand(a, "/new") }},
	{key: 'l', name: i18n.T("leader.name.list"), desc: i18n.T("leader.desc.list"), action: func(a *App) { handleSlashCommand(a, "/sessions") }},
	{key: 'c', name: i18n.T("leader.name.compact"), desc: i18n.T("leader.desc.compact"), action: func(a *App) { handleSlashCommand(a, "/compact") }},
	{key: 'k', name: i18n.T("leader.name.keys"), desc: i18n.T("leader.desc.keys"), action: func(a *App) { openWhichKey(a) }},
	{key: 'y', name: i18n.T("leader.name.copy"), desc: i18n.T("leader.desc.copy"), action: func(a *App) {
		if t := lastAssistantText(a); t != "" {
			osc52Copy(a.screen, t)
		}
	}},
	{key: 'e', name: i18n.T("leader.name.expand"), desc: i18n.T("leader.desc.expand"), action: func(a *App) { toggleLastToolExpanded(a) }},
	{key: 'h', name: i18n.T("leader.name.thinking"), desc: i18n.T("leader.desc.thinking"), action: func(a *App) { cycleThinkingMode(a) }},
	{key: 'q', name: i18n.T("leader.name.quit"), desc: i18n.T("leader.desc.quit"), action: func(a *App) { a.quitting = true }},
}

// applyLeaderBinding interprets one key as a leader sequence and runs the matching binding. It
// always returns true (the key is consumed): a matching binding runs, an unknown key is swallowed
// so it never leaks into the input editor, and a non-rune key is swallowed too.
func applyLeaderBinding(a *App, k Key, ch rune) bool {
	if k != KeyRune {
		return true
	}
	lc := ch
	if lc >= 'A' && lc <= 'Z' {
		lc += 'a' - 'A'
	}
	for _, b := range leaderBindings {
		if b.key == lc {
			b.action(a)
			return true
		}
	}
	return true
}

// leaderBindingsForDisplay returns the leader bindings as panel rows (key shown as "<leader>X").
func leaderBindingsForDisplay() []keyBinding {
	out := make([]keyBinding, 0, len(leaderBindings))
	for _, b := range leaderBindings {
		out = append(out, keyBinding{
			key:   "<leader>" + string(b.key),
			desc:  b.desc,
			group: i18n.T("key.group.leader"),
		})
	}
	return out
}

// findLeaderBinding looks up a leader binding by its lowercase key (used by the which-key panel to
// run a binding when the user activates a row).
func findLeaderBinding(key rune) (leaderBinding, bool) {
	if key >= 'A' && key <= 'Z' {
		key += 'a' - 'A'
	}
	for _, b := range leaderBindings {
		if b.key == key {
			return b, true
		}
	}
	return leaderBinding{}, false
}
