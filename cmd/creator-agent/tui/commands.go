package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

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

// mentionFileCap bounds how many bytes of a referenced file are injected, to protect the context
// budget (a huge file shouldn't flood the model). Files larger than this are skipped entirely.
const mentionFileCap = 64 * 1024

// mentionTokenRe matches an @path or @path#N or @path#N-M reference at the start of input or after
// whitespace. The captured token (after '@') is non-whitespace, non-@, and may carry a trailing
// #range. Used at submit-time to resolve references (not for the live popup, which uses the
// stricter mentionTriggerIndex on the in-progress input).
var mentionTokenRe = regexp.MustCompile(`(?:^|\s)@([^\s@]+)`)

func resolveMentions(text string) string {
	var blocks []string
	seen := map[string]bool{}
	for _, m := range mentionTokenRe.FindAllStringSubmatchIndex(text, -1) {
		// m[2],m[3] = the captured token after '@'.
		token := text[m[2]:m[3]]
		path, start, end, hasRange := parseMentionRange(token)
		if path == "" {
			continue
		}
		// Avoid duplicate work for the same (path, range) reference.
		key := token
		if seen[key] {
			continue
		}
		seen[key] = true
		if strings.HasSuffix(path, "/") {
			continue
		}
		if block, ok := readMentionBlock(path, start, end, hasRange); ok {
			blocks = append(blocks, block)
		}
	}
	if len(blocks) == 0 {
		return text
	}
	var sb strings.Builder
	sb.WriteString(text)
	sb.WriteString(i18n.T("mention.header"))
	sb.WriteString(strings.Join(blocks, "\n"))
	return sb.String()
}

// readMentionBlock reads the file at path (relative to cwd), optionally slices it to the 1-indexed
// inclusive [start, end] line range, and formats it as a <path>/<content> block with line numbers.
// ok is false when the file is missing/unreadable/oversized (best-effort skip).
func readMentionBlock(path string, start, end int, hasRange bool) (string, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	if len(data) > mentionFileCap {
		return "", false // protect the context budget
	}
	lines := strings.Split(string(data), "\n")
	lo, hi := 1, len(lines)
	if hasRange {
		lo = start
		if lo < 1 {
			lo = 1
		}
		if end > 0 {
			hi = end
		} else if end == 0 && !strings.HasSuffix(path, "#") {
			hi = start
		}
		if hi > len(lines) {
			hi = len(lines)
		}
		if lo > hi {
			lo = hi
		}
	}
	var sb strings.Builder
	sb.WriteString("<path>")
	sb.WriteString(path)
	sb.WriteString("</path>\n<content>\n")
	for i := lo; i <= hi; i++ {
		sb.WriteString(strconv.Itoa(i))
		sb.WriteString(": ")
		sb.WriteString(lines[i-1])
		sb.WriteString("\n")
	}
	sb.WriteString("</content>")
	return sb.String(), true
}

// fileIgnoreDirs mirrors the glob/grep tools' defaultIgnoreDirs (core/builtins/grep.go). Kept as a
// local copy to avoid a tui -> core/builtins dependency (tui already imports core, but pulling the
// builtins package in just for this list would widen the dependency surface unnecessarily).
var fileIgnoreDirs = []string{".git", "vendor", "node_modules", "dist", "build"}

// fileIndexCap bounds the number of paths indexed. A typical project is well under this; huge
// monorepos would make the popup sluggish and the popup caps display at 10 anyway.
const fileIndexCap = 2000

func buildFileIndex(root string) []string {
	ignoreSet := make(map[string]struct{}, len(fileIgnoreDirs))
	for _, d := range fileIgnoreDirs {
		ignoreSet[d] = struct{}{}
	}
	var out []string
	stop := false
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || stop {
			return nil
		}
		if d.IsDir() {
			if path != root {
				if _, skip := ignoreSet[d.Name()]; skip {
					return filepath.SkipDir
				}
				rel, _ := filepath.Rel(root, path)
				rel = filepath.ToSlash(rel)
				out = append(out, rel+"/")
				if len(out) >= fileIndexCap {
					stop = true
					return filepath.SkipAll
				}
			}
			return nil
		}
		if shouldSkipFile(d.Name()) {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		out = append(out, rel)
		if len(out) >= fileIndexCap {
			stop = true
			return filepath.SkipAll
		}
		return nil
	})
	sort.Strings(out)
	return out
}

func shouldSkipFile(name string) bool {
	switch name {
	case ".DS_Store", "Thumbs.db":
		return true
	}
	switch filepath.Ext(name) {
	case ".pyc", ".swp", ".swo", ".log":
		return true
	}
	return false
}

// getFileIndex returns the cached file index for root, rebuilding it when the cache is empty or
// the root directory's mtime changed since the last build. All file-system access is contained
// here so callers can treat it as a pure lookup keyed on (root, root-mtime).
func getFileIndex(a *App, root string) []string {
	info, err := os.Stat(root)
	if err != nil {
		a.fileIndexDir = ""
		a.fileIndex = nil
		return nil
	}
	mtime := info.ModTime()
	if a.fileIndex != nil && a.fileIndexDir == root && eqTime(a.fileIndexMtime, mtime) {
		return a.fileIndex
	}
	a.fileIndex = buildFileIndex(root)
	a.fileIndexDir = root
	a.fileIndexMtime = mtime
	return a.fileIndex
}

// eqTime reports whether two ModTimes are equal at second precision. Sub-second drift from FS
// metadata reads should not trigger redundant rebuilds.
func eqTime(a, b time.Time) bool {
	return a.Unix() == b.Unix()
}

// mentionTriggerIndex finds the index of the `@` that should open the file-mention popup, or -1.
// Rules (mirror opencode's prompt/display.ts:mentionTriggerIndex):
//   - the `@` must be at position 0 OR immediately preceded by whitespace;
//   - there must be no whitespace between the `@` and the end of input (the user is still typing
//     the path token).
//
// Returns the byte index of the triggering `@`, or -1 when no trigger applies.
func mentionTriggerIndex(v string) int {
	// Only the last `@` matters (the one nearest the cursor / end of input).
	idx := strings.LastIndex(v, "@")
	if idx < 0 {
		return -1
	}
	// Must be at start or preceded by whitespace.
	if idx > 0 {
		prev := v[idx-1]
		if prev != ' ' && prev != '\t' && prev != '\n' {
			return -1
		}
	}
	tail := v[idx+1:]
	if strings.ContainsAny(tail, " \t\n") {
		return -1
	}
	return idx
}

func extractMentionQuery(v string) string {
	idx := mentionTriggerIndex(v)
	if idx < 0 {
		return ""
	}
	return v[idx+1:]
}

var mentionRangeRe = regexp.MustCompile(`^#(\d+)(?:-(\d*))?$`)

// parseMentionRange splits a mention query into its base path and an optional line range. ok is
// true when the query ends with a parseable `#N` / `#N-M` suffix. start is always the 1-indexed
// start line; end is the 1-indexed end line (0 means "single line / open-ended" when the suffix
// was `#N` or `#N-`).
func parseMentionRange(query string) (base string, start, end int, ok bool) {
	hash := strings.LastIndex(query, "#")
	if hash < 0 {
		return query, 0, 0, false
	}
	m := mentionRangeRe.FindStringSubmatch(query[hash:])
	if m == nil {
		return query, 0, 0, false
	}
	base = query[:hash]
	start = atoi(m[1])
	if m[2] != "" {
		end = atoi(m[2])
	}
	return base, start, end, true
}

func preserveMentionSuffix(query, sel string) (suffix string, ok bool) {
	if !strings.HasPrefix(query, sel) {
		return "", false
	}
	rest := query[len(sel):]
	if rest == "" {
		return "", false
	}
	if mentionRangeRe.MatchString(rest) {
		return rest, true
	}
	return "", false
}

func stripMentionRange(query string) string {
	base, _, _, ok := parseMentionRange(query)
	if !ok {
		return query
	}
	return base
}

// atoi is a local Atoi (avoids importing strconv for a single use); returns 0 on non-numeric.
func atoi(s string) int {
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0
		}
		n = n*10 + int(r-'0')
	}
	return n
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

// slashCommands is lazily populated on first access (to avoid init-order dependency on
// commandRegistry, which is defined in commands.go). Once populated it is immutable.
var slashCommands []string

func getSlashCommands() []string {
	if slashCommands == nil {
		out := make([]string, 0, len(commandRegistry))
		for _, e := range commandRegistry {
			out = append(out, e.name)
		}
		slashCommands = out
	}
	return slashCommands
}

func matchCommands(prefix string) []string {
	var out []string
	for _, c := range getSlashCommands() {
		if strings.HasPrefix(c, prefix) {
			out = append(out, c)
		}
	}
	return out
}

func commandDesc(c string) string {
	if e := findCommand(c); e != nil {
		return i18n.T("cmd.desc." + c)
	}
	return ""
}

func matchCommandsFuzzy(query string) []string {
	q := normalizeLower(strings.TrimSpace(query))
	q = strings.TrimPrefix(q, "/")
	if q == "" {
		out := make([]string, len(getSlashCommands()))
		copy(out, getSlashCommands())
		return out
	}
	type scored struct {
		name  string
		score int
	}
	var hits []scored
	for _, c := range getSlashCommands() {
		s, ok := fuzzyScore(c, commandDesc(c), q)
		if !ok || s <= 0 {
			continue
		}
		hits = append(hits, scored{name: c, score: s})
	}
	// Stable descending sort by score (insertion sort; the list is tiny). Stability preserves the
	for i := 1; i < len(hits); i++ {
		for j := i; j > 0 && hits[j].score > hits[j-1].score; j-- {
			hits[j], hits[j-1] = hits[j-1], hits[j]
		}
	}
	out := make([]string, 0, len(hits))
	for _, h := range hits {
		out = append(out, h.name)
	}
	return out
}

func refineSlashCompletions(a *App) {
	v := a.input.Value()
	if !shouldShowSlashCompletions(v) {
		a.completions = nil
		a.completionsKind = compNone
		a.compIdx = 0
		a.compScroll = 0
		return
	}
	a.completions = matchCommandsFuzzy(v)
	a.completionsKind = compSlash
	a.compIdx = 0 // reset to top row on every refine (matches opencode)
	a.compScroll = 0
}

// shouldShowSlashCompletions reports whether the inline slash popup should be open for input v:
// the input must start with "/" and contain no whitespace (so "/help foo" hides the menu — the
// user has moved on to arguments). A lone "/" opens the menu with all commands (empty query) for
// discoverability. Mirrors opencode's "/"-trigger condition.
func shouldShowSlashCompletions(v string) bool {
	if !strings.HasPrefix(v, "/") {
		return false
	}
	return !strings.ContainsAny(v, " \t\n")
}

// refineFileCompletions recomputes the @-mention file candidate list from the current input.
// When the input no longer has a triggering `@` token, the menu is closed. The file index is
// pulled from the working-directory cache (fileindex.go).
func refineFileCompletions(a *App) {
	v := a.input.Value()
	if !shouldShowFileCompletions(v) {
		a.completions = nil
		a.completionsKind = compNone
		a.compIdx = 0
		a.compScroll = 0
		return
	}
	q := normalizeLower(stripMentionRange(extractMentionQuery(v)))
	idx := getFileIndex(a, ".")
	a.completions = matchFilesFuzzy(idx, q)
	a.completionsKind = compFile
	a.compIdx = 0
	a.compScroll = 0
}

// shouldShowFileCompletions reports whether the @-mention popup should be open for input v: there
// must be a triggering `@` (per mentionTriggerIndex rules) with the user still typing the path
// token (no whitespace after it).
func shouldShowFileCompletions(v string) bool {
	return mentionTriggerIndex(v) >= 0
}

func matchFilesFuzzy(files []string, query string) []string {
	const cap = 10
	if query == "" {
		out := make([]string, 0, cap)
		for _, f := range files {
			out = append(out, f)
			if len(out) >= cap {
				break
			}
		}
		return out
	}
	type scored struct {
		path  string
		score int
	}
	var hits []scored
	for _, f := range files {
		s, ok := fuzzyScore(f, "", query)
		if !ok || s <= 0 {
			continue
		}
		s += maxi(0, 40-len(f))
		hits = append(hits, scored{path: f, score: s})
	}
	for i := 1; i < len(hits); i++ {
		for j := i; j > 0 && hits[j].score > hits[j-1].score; j-- {
			hits[j], hits[j-1] = hits[j-1], hits[j]
		}
	}
	out := make([]string, 0, cap)
	for _, h := range hits {
		out = append(out, h.path)
		if len(out) >= cap {
			break
		}
	}
	return out
}

// refineCompletions is the single dispatcher called after every input mutation: it decides
// whether the slash popup, the file-mention popup, or neither should be open, and updates
// a.completions / a.completionsKind accordingly. Slash takes precedence over file (a leading "/"
// never also triggers "@").
func refineCompletions(a *App) {
	v := a.input.Value()
	switch {
	case shouldShowSlashCompletions(v):
		refineSlashCompletions(a)
	case shouldShowFileCompletions(v):
		refineFileCompletions(a)
	default:
		a.completions = nil
		a.completionsKind = compNone
		a.compIdx = 0
		a.compScroll = 0
	}
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

func historyPath() (string, bool) {
	p, err := paths.HistoryFile()
	if err != nil {
		return "", false
	}
	return p, true
}

// loadHistory loads input history from disk (nil on missing/corrupt).
func loadHistory() []string {
	p, ok := historyPath()
	if !ok {
		return nil
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return nil
	}
	var h []string
	if json.Unmarshal(data, &h) != nil {
		return nil
	}
	return h
}

// saveHistory persists input history (best-effort; failures are logged for diagnosis).
func saveHistory(h []string) {
	p, ok := historyPath()
	if !ok {
		return
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		core.Warnf("save TUI history: mkdir: %v", err)
		return
	}
	data, err := json.Marshal(h)
	if err != nil {
		core.Warnf("save TUI history: marshal: %v", err)
		return
	}
	if err := os.WriteFile(p, data, 0o600); err != nil {
		core.Warnf("save TUI history: write: %v", err)
	}
}

func appendHistory(h []string, s string) []string {
	if s == "" {
		return h
	}
	if len(h) > 0 && h[len(h)-1] == s {
		return h
	}
	h = append(h, s)
	if len(h) > 1000 {
		h = h[len(h)-1000:]
	}
	return h
}
