package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"github.com/skys-mission/creator-agent/cmd/creator-agent/diag"
	"github.com/skys-mission/creator-agent/cmd/creator-agent/tui/i18n"
	"github.com/skys-mission/creator-agent/config"
	"github.com/skys-mission/creator-agent/core"
	"github.com/skys-mission/creator-agent/paths"
)

func submitInput(a *App) {
	text := strings.TrimSpace(a.input.Value())
	if text == "" {
		return
	}
	// Slash commands are always runnable.
	if strings.HasPrefix(text, "/") {
		if handleSlashCommand(a, text) {
			a.input.SetValue("")
			return
		}
		// A command-like token that is not registered is a typo, not a prompt: intercept it with a
		// hint instead of forwarding "/unknwon ..." to the model (which wastes a turn and confuses it).
		// Non-command-like slashes (e.g. an absolute path "/etc/hosts") fall through as normal text.
		if isSlashCommandLike(text) {
			name, _, _ := strings.Cut(text, " ")
			a.addSystem(fmt.Sprintf(i18n.T("msg.unknown_command"), name))
			a.input.SetValue("")
			return
		}
	}
	// Concurrency guard: block a second submit while running.
	if a.status != statusIdle {
		a.addSystem(i18n.T("msg.execution_in_progress"))
		return
	}
	// Submit to agent. Resolve @path[#range] references into injected file content (best-effort);
	resolved := resolveMentions(text)
	ub := msgBlock{kind: kindUser}
	ub.content.WriteString(resolved)
	a.messages = append(a.messages, ub)
	a.input.SetValue("")
	a.history = appendHistory(a.history, text) // history keeps the raw typed text
	saveHistory(a.history)
	a.histIdx = 0
	a.status = statusThinking
	a.statusStarted = time.Now()
	a.current = &msgBlock{kind: kindAssistant}
	clearHomeTip(a)
	startStream(a, core.StreamInput{
		SessionID: a.rt.sessionID,
		Messages:  []core.Message{core.UserMessage(resolved)},
	})
}

type commandEntry struct {
	name    string                        // canonical slash name incl. "/", e.g. "/help"
	hasArg  bool                          // true if the command consumes an argument after the name
	handler func(a *App, arg string) bool // returns true if recognized+handled; false to fall through
}

// commandRegistry is the ordered list of slash commands. Order is completion/help priority.
// Handlers are methods that return true when the command is recognized (always, here) so the
// caller knows not to forward the text to the agent.
var commandRegistry = []commandEntry{
	{name: "/help", handler: func(a *App, _ string) bool {
		a.helpOverlay.open = true
		a.helpOverlay.scroll = 0
		return true
	}},
	{name: "/clear", handler: func(a *App, _ string) bool {
		if a.rt.ag != nil {
			a.rt.ag.ClearSession(a.rt.sessionID)
		}
		a.messages = nil
		a.current = nil
		a.totalIn, a.totalOut = 0, 0
		a.lastInput = 0
		a.usage = ""
		a.err = nil
		a.changes = nil
		a.todoList = nil
		a.status = statusIdle
		return true
	}},
	{name: "/copy", handler: func(a *App, _ string) bool {
		// (mouse capture is off, so native selection works too; this is a fallback / convenience).
		text := lastAssistantText(a)
		if text == "" {
			a.addSystem(i18n.T("copy.empty"))
			return true
		}
		path, err := saveLastReply(text)
		if err != nil {
			a.addSystem(fmt.Sprintf(i18n.T("copy.failed"), err))
		} else {
			a.addSystem(fmt.Sprintf(i18n.T("copy.saved"), path))
		}
		return true
	}},
	{name: "/compact", handler: func(a *App, _ string) bool {
		startCompact(a)
		return true
	}},
	{name: "/cost", handler: func(a *App, _ string) bool {
		a.addSystem(fmt.Sprintf(i18n.T("cost.result"),
			a.totalIn, a.totalOut, orDash(a.usage), orDash(costEstimateStr(a.rt.prof.Model, a.totalIn, a.totalOut))))
		return true
	}},
	{name: "/model", hasArg: true, handler: func(a *App, arg string) bool {
		return handleModelCommand(a, arg)
	}},
	{name: "/models", handler: func(a *App, _ string) bool {
		openModelPicker(a)
		return true
	}},
	{name: "/mcps", handler: func(a *App, _ string) bool {
		openMCPPicker(a)
		return true
	}},
	{name: "/agents", handler: func(a *App, _ string) bool {
		openAgentsPicker(a)
		return true
	}},
	{name: "/variants", handler: func(a *App, _ string) bool {
		openVariantPicker(a)
		return true
	}},
	{name: "/mode", hasArg: true, handler: func(a *App, arg string) bool {
		return handleModeCommand(a, arg)
	}},
	{name: "/modes", handler: func(a *App, _ string) bool {
		return handleModeCommand(a, "")
	}},
	{name: "/sandbox", hasArg: true, handler: func(a *App, arg string) bool {
		return handleSandboxCommand(a, arg)
	}},
	{name: "/changes", handler: func(a *App, _ string) bool {
		a.addSystem(renderChangesList(a.changes))
		return true
	}},
	{name: "/diff", hasArg: true, handler: func(a *App, arg string) bool {
		if arg == "" {
			a.addSystem(i18n.T("diff.usage"))
			return true
		}
		openDiffForPath(a, arg)
		return true
	}},
	{name: "/themes", hasArg: true, handler: func(a *App, arg string) bool {
		return handleThemesCommand(a, arg)
	}},
	{name: "/new", handler: func(a *App, _ string) bool {
		newSession(a)
		return true
	}},
	{name: "/rename", hasArg: true, handler: func(a *App, arg string) bool {
		return handleRenameCommand(a, arg)
	}},
	{name: "/pin", handler: func(a *App, _ string) bool {
		return handlePinCommand(a)
	}},
	{name: "/sessions", handler: func(a *App, _ string) bool {
		openSessionPicker(a)
		return true
	}},
	{name: "/resume", handler: func(a *App, _ string) bool {
		openSessionPicker(a)
		return true
	}},
	{name: "/tools", handler: func(a *App, _ string) bool {
		handleToolsCommand(a)
		return true
	}},
	{name: "/exit", handler: func(a *App, _ string) bool {
		a.quitting = true
		return true
	}},
	{name: "/quit", handler: func(a *App, _ string) bool {
		a.quitting = true
		return true
	}},
}

// isSlashCommandLike reports whether text looks like a slash command invocation: a leading '/'
// followed by a single token of command characters (letters, digits, '-', '_'), optionally followed
// by whitespace and arguments. This distinguishes "/foo bar" (command-like) from "/etc/hosts" or
// "/ note" (ordinary text a user may legitimately send to the model).
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

// handleSandboxCommand implements /sandbox [on|off|auto]:
//   - no arg: report the current override state;
//   - on/off: force OS bash isolation on or off for this session;
//   - auto: clear the override and follow the base policy (config tri-state + permission mode).
//
// The override is shared with the bash tool's PolicySandbox, so it takes effect on the next command
// with no agent rebuild.
func handleSandboxCommand(a *App, arg string) bool {
	if a.rt.sandboxCtl == nil {
		a.addSystem("Sandbox control is not available in this session.")
		return true
	}
	switch strings.ToLower(strings.TrimSpace(arg)) {
	case "":
		a.addSystem("Sandbox override: " + sandboxOverrideLabel(a.rt.sandboxCtl.Override()) +
			"\nUse /sandbox on|off|auto. 'auto' follows the config + permission mode policy (isolation on in auto mode).")
	case "on", "true", "enable", "enabled":
		v := true
		a.rt.sandboxCtl.Set(&v)
		a.addSystem("Sandbox override set to ON: bash commands run OS-isolated (fails closed if unavailable).")
	case "off", "false", "disable", "disabled":
		v := false
		a.rt.sandboxCtl.Set(&v)
		a.addSystem("Sandbox override set to OFF: bash commands run without OS isolation.")
	case "auto", "reset", "clear", "default":
		a.rt.sandboxCtl.Set(nil)
		a.addSystem("Sandbox override cleared: following the config + permission mode policy.")
	default:
		a.addSystem("Usage: /sandbox [on|off|auto]")
	}
	a.forceRender = true
	return true
}

// sandboxOverrideLabel renders the tri-state override for display.
func sandboxOverrideLabel(ov *bool) string {
	if ov == nil {
		return "auto (following policy)"
	}
	if *ov {
		return "on (forced)"
	}
	return "off (forced)"
}

func handleToolsCommand(a *App) {
	if len(a.rt.toolInfos) == 0 {
		a.addSystem(i18n.T("msg.no_tools"))
		return
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf(i18n.T("tools.header"), len(a.rt.toolInfos)))
	for _, ti := range a.rt.toolInfos {
		ro := i18n.T("tools.read_write")
		if ti.ReadOnly {
			ro = i18n.T("tools.read_only")
		}
		desc := toolShortDesc(ti.Description)
		sb.WriteString(fmt.Sprintf("  %-12s [%s] %s\n", ti.Name, ro, desc))
	}
	a.addSystem(strings.TrimRight(sb.String(), "\n"))
}

// handleModelCommand implements /model:
//   - no arg: open the /models picker (so the user can browse profiles), or report the current
//     profile + usage when no profiles are configured;
//   - arg present: switch at runtime via the rebuild factory (preserves conversation history).
func handleModelCommand(a *App, arg string) bool {
	if arg == "" {
		// When profiles are configured, open the picker; otherwise fall back to the text summary.
		if len(a.rt.profiles) > 0 {
			openModelPicker(a)
			return true
		}
		a.addSystem(fmt.Sprintf(i18n.T("model.current"),
			orDash(a.rt.prof.Name), a.rt.prof.Model, config.HostOf(a.rt.prof.BaseURL)))
		return true
	}
	if err := applyProfileSwitch(a, arg); err != nil {
		a.addSystem(fmt.Sprintf(i18n.T("msg.switch_failed"), err))
	}
	return true
}

// applyProfileSwitch rebuilds the agent for the named profile and updates the displayed profile +
// token counters. Shared by the /model <name> text path and the /models picker commit. Returns an
// error when runtime switching is unavailable or the rebuild fails; on error the app state is
// left unchanged.
func applyProfileSwitch(a *App, name string) error {
	if a.rt.rebuild == nil {
		return fmt.Errorf("%s", i18n.T("model.switch_unavailable"))
	}
	newProf, err := a.rt.rebuild(name)
	if err != nil {
		return err
	}
	a.rt.prof = profile{Name: name, Model: newProf.Model, BaseURL: newProf.BaseURL}
	a.totalIn, a.totalOut = 0, 0
	a.lastInput = 0
	a.usage = ""
	a.forceRender = true
	a.addSystem(fmt.Sprintf(i18n.T("model.switched"), name, newProf.Model))
	return nil
}

func applyAgentSwitch(a *App, name string) error {
	if a.rt.agentSwitch == nil {
		return fmt.Errorf("%s", i18n.T("agent.switch_unavailable"))
	}
	if err := a.rt.agentSwitch(name); err != nil {
		return err
	}
	a.rt.currentAgent = name
	a.forceRender = true
	a.addSystem(fmt.Sprintf(i18n.T("agent.switched"), name))
	return nil
}

// applyVariantSwitch rebuilds the provider with the named variant's request overrides applied via
// the injected switch factory. An empty name clears the variant (back to Default). The factory
// enqueues a switchAgentMsg to apply the swap on the event loop. Shared by the /variants picker
// commit.
func applyVariantSwitch(a *App, name string) error {
	if a.rt.variantSwitch == nil {
		return fmt.Errorf("%s", i18n.T("variant.switch_unavailable"))
	}
	if err := a.rt.variantSwitch(name); err != nil {
		return err
	}
	a.rt.currentVariant = name
	a.forceRender = true
	label := name
	if label == "" {
		label = i18n.T("variant.default")
	}
	a.addSystem(fmt.Sprintf(i18n.T("variant.switched"), label))
	return nil
}

// handleRenameCommand implements /rename <new title>: renames the active session by persisting
// the new title via the session store's metadata mutator (JSONFileStore and MemoryStore both
// implement SessionMetaMutator.Rename).
func handleRenameCommand(a *App, arg string) bool {
	arg = strings.TrimSpace(arg)
	if arg == "" {
		a.addSystem(i18n.T("rename.usage"))
		return true
	}
	if a.rt.store == nil {
		a.addSystem(i18n.T("rename.no_store"))
		return true
	}
	mut, ok := a.rt.store.(core.SessionMetaMutator)
	if !ok {
		a.addSystem(i18n.T("rename.unsupported"))
		return true
	}
	if err := mut.Rename(a.rt.sessionID, arg); err != nil {
		a.addSystem(fmt.Sprintf(i18n.T("rename.failed"), err))
		return true
	}
	a.addSystem(fmt.Sprintf(i18n.T("rename.done"), arg))
	a.forceRender = true
	return true
}

func handlePinCommand(a *App) bool {
	if a.rt.store == nil {
		a.addSystem(i18n.T("pin.no_store"))
		return true
	}
	mut, ok := a.rt.store.(core.SessionMetaMutator)
	if !ok {
		a.addSystem(i18n.T("pin.unsupported"))
		return true
	}
	// Best-effort read of current pinned state for the response message.
	pinned := false
	type metaReader interface {
		LoadMeta(id string) (string, time.Time, bool, error)
	}
	if mr, ok2 := a.rt.store.(metaReader); ok2 {
		_, _, p, _ := mr.LoadMeta(a.rt.sessionID)
		pinned = p
	}
	newPinned := !pinned
	if err := mut.SetPinned(a.rt.sessionID, newPinned); err != nil {
		a.addSystem(fmt.Sprintf(i18n.T("pin.failed"), pinAction(newPinned), err))
		return true
	}
	a.addSystem(fmt.Sprintf(i18n.T("pin.done"), pinAction(newPinned)))
	return true
}

func pinAction(pinned bool) string {
	if pinned {
		return i18n.T("pin.pinned")
	}
	return i18n.T("pin.unpinned")
}

func handleThemesCommand(a *App, arg string) bool {
	names := supportedThemeNames()
	if arg == "" {
		// Consistent with /models /mcps /agents /variants.
		openThemePicker(a)
		return true
	}
	if !isSupportedTheme(arg) {
		a.addSystem(fmt.Sprintf(i18n.T("theme.unknown"), arg, strings.Join(names, ", ")))
		return true
	}
	applied := applyTheme(arg)
	a.forceRender = true
	a.addSystem(fmt.Sprintf(i18n.T("theme.set"), applied))
	return true
}

func supportedThemeNames() []string { return SupportedThemes() }

func handleApprovalKey(a *App, k Key, ch rune) {
	const nOpt = 3
	switch k {
	case KeyUp, KeyLeft:
		if a.approveIdx > 0 {
			a.approveIdx--
		}
		return
	case KeyDown, KeyRight:
		if a.approveIdx < nOpt-1 {
			a.approveIdx++
		}
		return
	case KeyEnter:
		applyApprovalChoice(a)
		return
	case KeyBacktab: // shift+tab: allow this tool for the rest of the session
		a.approveIdx = 1
		applyApprovalChoice(a)
		return
	case KeyEsc, KeyCtrlC:
		sendReply(a.asking.replyCh, false)
		a.asking = nil
		return
	}
	switch ch {
	case '1', '2', '3':
		a.approveIdx = int(ch - '1')
		applyApprovalChoice(a)
	case 'd', 'D':
		openApprovalDiffOverlay(a)
	}
}

func applyApprovalChoice(a *App) {
	switch a.approveIdx {
	case 0: // allow once
		sendReply(a.asking.replyCh, true)
		a.asking = nil
	case 1: // allow this session
		sendReply(a.asking.replyCh, true)
		if a.rt.approver != nil {
			switch a.asking.toolName {
			case "write", "edit":
				a.rt.approver.rememberAllEdits()
			default:
				a.rt.approver.remember(a.asking.toolName, a.asking.input)
			}
		}
		a.asking = nil
	case 2: // deny
		sendReply(a.asking.replyCh, false)
		a.asking = nil
	}
}

func handleCoreEvent(a *App, ev core.Event) {
	switch e := ev.(type) {
	case core.TextEvent:
		if a.current == nil {
			a.current = &msgBlock{kind: kindAssistant}
		}
		a.current.content.WriteString(e.Delta)
		a.status = statusThinking
		a.atBottom = true
	case core.ThinkingEvent:
		if a.current == nil {
			a.current = &msgBlock{kind: kindAssistant}
		}
		a.current.thinking.WriteString(e.Delta)
		a.status = statusThinking
		a.atBottom = true
	case core.ToolUseStartEvent:
		if a.current == nil {
			a.current = &msgBlock{kind: kindAssistant}
		}
		tc := toolCard{
			id: e.ID, name: e.Name, status: toolRunning, started: time.Now(),
		}
		a.current.tools = append(a.current.tools, tc)
		a.current.toolCallName = e.Name
		a.status = statusRunningTool
		a.statusStarted = time.Now()
		diag.Trace("tool.start name=%s id=%s", e.Name, e.ID)
	case core.ToolUseDeltaEvent:
		if a.current != nil {
			for i := len(a.current.tools) - 1; i >= 0; i-- {
				if a.current.tools[i].id == e.ID && a.current.tools[i].status == toolRunning {
					a.current.tools[i].argsJSON += e.DeltaJSON
					break
				}
			}
			if a.current.toolCallName != "" {
				a.current.toolCallArgs.WriteString(e.DeltaJSON)
			}
		}
	case core.ToolResultEvent:
		diag.Trace("tool.result id=%s err=%v", e.ID, e.Err != nil)
		if a.current != nil {
			for i := range a.current.tools {
				if a.current.tools[i].id == e.ID {
					a.current.tools[i].duration = time.Since(a.current.tools[i].started)
					if e.Err != nil {
						a.current.tools[i].status = toolError
						a.current.tools[i].errMsg = e.Err.Error()
					} else if e.Result.IsError {
						a.current.tools[i].status = toolError
						a.current.tools[i].errMsg = e.Result.Content
					} else {
						a.current.tools[i].status = toolDone
						a.current.tools[i].result = e.Result.Content
						a = recordChange(a, a.current.tools[i].name, a.current.tools[i].argsJSON, e.Result.Content)
						a = recordTodo(a, a.current.tools[i].name, a.current.tools[i].argsJSON)
					}
					break
				}
			}
		}
	case core.UsageEvent:
		a.usage = fmt.Sprintf("%d->%d", e.Usage.InputTokens, e.Usage.OutputTokens)
		a.totalIn += e.Usage.InputTokens
		a.totalOut += e.Usage.OutputTokens
		a.lastInput = e.Usage.InputTokens
	case core.FinishEvent:
		if e.Reason == core.FinishStepLimit {
			a.status = statusError
		}
	case core.ErrorEvent:
		diag.Trace("core.error: %v", e.Err)
		a.err = e.Err
		a.status = statusError
	}
}

func handleStreamEnd(a *App) {
	diag.Trace("stream.end")
	a.streamCancel = nil
	if a.current != nil {
		a.current.finalize(a.rt.md)
		a.messages = append(a.messages, *a.current)
		a.current = nil
	}
	a.status = statusIdle
	// title generator can now see the fresh turn. Best-effort + async + one-shot-per-session.
	maybeTriggerTitleGeneration(a)
}

// recordTodo parses todo_write tool argsJSON and updates the session-level todo list.
func recordTodo(a *App, toolName, argsJSON string) *App {
	if toolName != "todo_write" {
		return a
	}
	var args struct {
		Todos []struct {
			Content string `json:"content"`
			Status  string `json:"status"`
		} `json:"todos"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return a
	}
	items := make([]todoItem, 0, len(args.Todos))
	for _, td := range args.Todos {
		if strings.TrimSpace(td.Content) == "" {
			continue
		}
		items = append(items, todoItem{content: td.Content, status: td.Status})
	}
	a.todoList = items
	return a
}

func recordChange(a *App, toolName, argsJSON, result string) *App {
	if toolName != "write" && toolName != "edit" {
		return a
	}
	info := parseToolInput(toolName, argsJSON)
	if info.path == "" {
		return a
	}
	a.changes = append(a.changes, changeRecord{
		path:      info.path,
		tool:      toolName,
		argsJSON:  argsJSON,
		result:    result,
		timestamp: time.Now(),
	})
	return a
}

// switchSession loads another session's history into the message area, replacing the current view.
// It refuses while a stream is running (the agent loop would race the message array swap).
//
// The current session need not be explicitly saved here: the agent's Stream already persists each
// turn via the SessionStore on stream completion, so by the time the user switches, the latest
// history is on disk/in-memory. Switching only needs to load the destination.
func switchSession(a *App, id string) {
	if a == nil || id == "" || id == a.rt.sessionID {
		return
	}
	if a.status != statusIdle {
		a.addSystem(i18n.T("session.switch_busy"))
		return
	}
	if a.rt.store == nil {
		a.addSystem(i18n.T("session.switch_no_store"))
		return
	}
	msgs, err := a.rt.store.Load(id)
	if err != nil {
		a.addSystem(fmt.Sprintf(i18n.T("session.load_failed"), shortID(id), err))
		return
	}
	a.rt.sessionID = id
	resetConversationView(a)
	a.messages = rebuildMessagesFromCore(a, msgs)
	if len(a.messages) > 0 {
		a.addSystem(fmt.Sprintf(i18n.T("session.resumed"), shortID(id)))
	}
	a.atBottom = true
	a.msgScroll = 0
	a.forceRender = true
}

func newSession(a *App) {
	if a == nil {
		return
	}
	if a.status != statusIdle {
		a.addSystem(i18n.T("session.new_busy"))
		return
	}
	a.rt.sessionID = core.GenerateSessionID()
	resetConversationView(a)
	a.addSystem(fmt.Sprintf(i18n.T("session.new"),
		shortID(a.rt.sessionID)))
	a.forceRender = true
}

// loadCurrentSessionHistory populates the message area with the current session's persisted history.
// Called once on startup so a resumed session (-c/--continue) shows its prior conversation instead
// of the home screen. No-op when the session has no stored history (a fresh session -> home screen).
// Unlike switchSession it does not print a "Resumed ..." notice (the user opted in via -c).
func loadCurrentSessionHistory(a *App) {
	if a == nil || a.rt.store == nil || a.rt.sessionID == "" {
		return
	}
	msgs, err := a.rt.store.Load(a.rt.sessionID)
	if err != nil || len(msgs) == 0 {
		return
	}
	a.messages = rebuildMessagesFromCore(a, msgs)
	a.atBottom = true
	a.msgScroll = 0
}

// resetConversationView clears all per-conversation UI state so a new/switched session starts
// visually clean. Counters (tokens/cost) are reset since they belong to the displayed conversation.
func resetConversationView(a *App) {
	a.messages = nil
	a.current = nil
	a.totalIn, a.totalOut = 0, 0
	a.lastInput = 0
	a.usage = ""
	a.err = nil
	a.changes = nil
	a.todoList = nil
	a.status = statusIdle
	a.completions = nil
	a.compIdx = 0
	a.compScroll = 0
	a.msgScroll = 0
	a.atBottom = true
	clearHomeTip(a)
}

func rebuildMessagesFromCore(a *App, msgs []core.Message) []msgBlock {
	if len(msgs) == 0 {
		return nil
	}
	out := make([]msgBlock, 0, len(msgs))
	for _, m := range msgs {
		switch m.Role {
		case core.RoleUser:
			if m.Content == "" {
				continue
			}
			mb := msgBlock{kind: kindUser}
			mb.content.WriteString(m.Content)
			out = append(out, mb)
		case core.RoleAssistant:
			if m.Content == "" && m.Reasoning == "" {
				continue
			}
			mb := msgBlock{kind: kindAssistant}
			mb.content.WriteString(m.Content)
			if m.Reasoning != "" {
				mb.thinking.WriteString(m.Reasoning)
			}
			if a != nil && a.rt.md != nil {
				mb.finalize(a.rt.md)
			}
			out = append(out, mb)
		}
	}
	return out
}

func shortID(id string) string {
	const keep = 4
	if len(id) <= keep+4 {
		return id
	}
	return id[:4] + "…" + id[len(id)-keep:]
}

func currentSessionTitle(a *App) string {
	if a == nil || a.rt.store == nil {
		return core.DefaultSessionTitle(time.Now())
	}
	if title, _, _, err := loadMetaBestEffort(a.rt.store, a.rt.sessionID); err == nil && title != "" {
		return title
	}
	// No persisted metadata yet (session has no saved turn): synthesize from the in-memory messages.
	msgs, _ := a.rt.store.Load(a.rt.sessionID)
	if title := core.DeriveTitle(msgs, time.Now()); title != "" && !core.IsDefaultSessionTitle(title) {
		return title
	}
	return core.DefaultSessionTitle(time.Now())
}

func loadMetaBestEffort(s core.SessionStore, id string) (string, time.Time, bool, error) {
	// Prefer the JSONFileStore's cheap meta read when available (avoids loading full history).
	type metaReader interface {
		LoadMeta(id string) (string, time.Time, bool, error)
	}
	if mr, ok := s.(metaReader); ok {
		return mr.LoadMeta(id)
	}
	// Fallback: scan List for the matching id (works for any SessionStore).
	infos, err := s.List()
	if err != nil {
		return "", time.Time{}, false, err
	}
	for _, info := range infos {
		if info.ID == id {
			return info.Title, info.UpdatedAt, info.Pinned, nil
		}
	}
	return "", time.Time{}, false, nil
}

// sessionMutator returns the store's SessionMetaMutator capability (SetPinned/Rename) if the store
// supports it, else nil. Used by the session picker's pin/rename actions.
func sessionMutator(s core.SessionStore) core.SessionMetaMutator {
	if s == nil {
		return nil
	}
	if m, ok := s.(core.SessionMetaMutator); ok {
		return m
	}
	return nil
}

// compactDoneMsg carries the result of an async manual compact. rawOut is the new message list from
// the compactor (nil on error). Runs on the event loop.
type compactDoneMsg struct {
	sessionID string
	rawOut    []core.Message
	err       error
}

func startCompact(a *App) {
	if a.rt.compactor == nil {
		a.addSystem(i18n.T("compact.unavailable"))
		return
	}
	if a.rt.sessionID == "" {
		a.addSystem(i18n.T("compact.no_session"))
		return
	}
	if a.status != statusIdle {
		a.addSystem(i18n.T("compact.busy"))
		return
	}
	a.status = statusCompacting
	a.statusStarted = time.Now()
	a.forceRender = true
	// Root the compaction at the app context so process shutdown cancels it. Fall back to Background
	// when no app ctx is wired (e.g. in tests).
	parent := a.rt.ctx
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithCancel(parent)
	compactor := a.rt.compactor
	sessionID := a.rt.sessionID
	sender := a.rt.sender
	go func() {
		defer cancel() // release the ctx (and parent chain) once compaction is done
		out, err := compactor(ctx, sessionID)
		if sender != nil {
			sender(compactDoneMsg{sessionID: sessionID, rawOut: out, err: err})
		}
	}()
}

// applyCompactDone handles a compactDoneMsg on the event loop: on success it replaces the displayed
// message blocks with the compacted history; on error it surfaces a system note. Always restores
// idle status.
func applyCompactDone(a *App, msg compactDoneMsg) {
	a.status = statusIdle
	if msg.err != nil {
		a.addSystem(fmt.Sprintf(i18n.T("compact.failed"), msg.err))
		a.forceRender = true
		return
	}
	if msg.sessionID != a.rt.sessionID {
		a.addSystem(i18n.T("compact.previous"))
		a.forceRender = true
		return
	}
	if msg.rawOut == nil {
		a.addSystem(i18n.T("compact.nothing"))
		a.forceRender = true
		return
	}
	a.messages = rebuildMessagesFromCore(a, msg.rawOut)
	a.msgScroll = 0
	a.atBottom = true
	a.forceRender = true
	a.addSystem(fmt.Sprintf(i18n.T("compact.done"), len(msg.rawOut)))
}

func logCrashToFile(r interface{}, stack []byte) {
	dir, err := paths.LogDir()
	if err != nil {
		// No home dir to write to: still surface the crash on stderr so it is not lost entirely.
		fmt.Fprintf(os.Stderr, "creator-agent crash (no home dir, could not write log): %v\n%s\n", r, stack)
		return
	}
	_ = os.MkdirAll(dir, 0o755)
	msg := fmt.Sprintf("time: %s\npanic: %v\n\n%s\n", time.Now().Format(time.RFC3339), r, stack)
	// Overwrite mode: keep only the most recent crash for quick diagnosis.
	// The user is pointed here on crash, so a write failure must not be silent: mirror to stderr so
	// the diagnostic survives even when the file cannot be written (read-only home, full disk, etc.).
	if werr := os.WriteFile(filepath.Join(dir, "tui-crash.log"), []byte(msg), 0o600); werr != nil {
		fmt.Fprintf(os.Stderr, "creator-agent crash (could not write tui-crash.log under ~/.creator: %v):\n%s\n", werr, msg)
	}
}

// LogCrashToDisk writes a panic value and its stack to the crash log. Exported so
// the top-level main goroutine recover shares the same diagnostic path as the
// TUI's per-goroutine recovers, regardless of where the panic propagated from.
func LogCrashToDisk(r interface{}, stack []byte) {
	logCrashToFile(r, stack)
}

func renderChangesList(changes []changeRecord) string {
	if len(changes) == 0 {
		return "No file changes in this session."
	}
	type agg struct {
		count    int
		lastTool string
		lastTime time.Time
	}
	byPath := make(map[string]*agg, len(changes))
	var order []string
	for _, c := range changes {
		x, ok := byPath[c.path]
		if !ok {
			x = &agg{}
			byPath[c.path] = x
			order = append(order, c.path)
		}
		x.count++
		x.lastTool = c.tool
		if c.timestamp.After(x.lastTime) {
			x.lastTime = c.timestamp
		}
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf(i18n.T("changes.header"), len(order), len(changes)))
	for _, p := range order {
		x := byPath[p]
		times := ""
		if x.count > 1 {
			times = fmt.Sprintf(" x%d", x.count)
		}
		sb.WriteString(fmt.Sprintf("  - [%s%s] %s  (%s)\n", x.lastTool, times, p, x.lastTime.Format("15:04:05")))
	}
	return strings.TrimRight(sb.String(), "\n")
}

func openDiffForPath(a *App, path string) {
	var rec *changeRecord
	for i := len(a.changes) - 1; i >= 0; i-- {
		if a.changes[i].path == path {
			rec = &a.changes[i]
			break
		}
	}
	if rec == nil {
		a.addSystem(fmt.Sprintf(i18n.T("diff.not_found"), path))
		return
	}
	info := parseToolInput(rec.tool, rec.argsJSON)
	diff := fullFileDiff(info)
	a.diffOverlay.open = true
	a.diffOverlay.title = fmt.Sprintf(i18n.T("diff.title"), path)
	if diff == "" {
		a.diffOverlay.lines = []string{i18n.T("diff.no_changes")}
	} else {
		a.diffOverlay.lines = strings.Split(renderDiffHunked(diff, a.width), "\n")
	}
	a.diffOverlay.scrollY = 0
}

func openApprovalDiffOverlay(a *App) {
	if a.asking == nil {
		return
	}
	ask := *a.asking
	info := parseToolInput(ask.toolName, ask.input)
	title := "Approval diff - " + ask.toolName
	if info.path != "" {
		title = info.path
	}
	var body string
	switch ask.toolName {
	case "write", "edit":
		body = renderDiffHunked(fullFileDiff(info), a.width)
	case "bash":
		if info.command != "" {
			body = "$ " + info.command
		}
	default:
		body = ""
	}
	if body == "" {
		body = truncateStr(ask.input, maxi(40, a.width-4))
	}
	a.diffOverlay.open = true
	a.diffOverlay.title = title
	a.diffOverlay.lines = strings.Split(body, "\n")
	a.diffOverlay.scrollY = 0
}
