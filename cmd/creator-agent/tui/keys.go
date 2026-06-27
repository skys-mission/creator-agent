package tui

import (
	"strings"
	"time"

	"github.com/skys-mission/creator-agent/cmd/creator-agent/tui/i18n"
)

func handleTcellEvent(a *App, ev Event) {
	switch e := ev.(type) {
	case *EventResize:
		w, h := e.Size()
		a.width, a.height = w, h
		a.input.SetWidth(maxi(10, w-4))
		return
	case *EventKey:
		// Guard against typed-nil Event interface (data pointer nil but type *EventKey).
		if e == nil {
			return
		}
		handleKey(a, e)
	case *EventMouse:
		if e == nil {
			return
		}
		handleMouse(a, e)
	}
}

// handleMouse processes mouse events. Wheel up/down scrolls the message viewport line-by-line
// (mirroring Claude Code's scroll:lineUp/scroll:lineDown); in the diff overlay it scrolls the
// diff. Other buttons are ignored. Overlay pickers don't use mouse and swallow the event.
func handleMouse(a *App, e *EventMouse) {
	if a.palette.open || a.sessionPicker.open || a.modelPicker.open || a.mcpPicker.open ||
		a.agentsPicker.open || a.variantPicker.open || a.modePicker.open || a.themePicker.open ||
		a.whichKey.open || a.helpOverlay.open {
		return
	}
	if a.diffOverlay.open {
		switch e.Button() {
		case MouseWheelUp:
			a.diffOverlay.scrollY = maxi(0, a.diffOverlay.scrollY-3)
		case MouseWheelDown:
			a.diffOverlay.scrollY += 3
		}
		return
	}
	switch e.Button() {
	case MouseWheelUp:
		a.msgScroll = maxi(0, a.msgScroll-3)
		a.atBottom = false
	case MouseWheelDown:
		if a.atBottom {
			return
		}
		a.msgScroll += 3
	}
}

func handleKey(a *App, e *EventKey) {
	k := e.Key()
	ch := e.Rune()

	if a.diffOverlay.open {
		switch k {
		case KeyEsc:
			closeDiffOverlay(a)
			return
		case KeyPgUp:
			a.diffOverlay.scrollY = maxi(0, a.diffOverlay.scrollY-pageHeight(a))
			return
		case KeyPgDn:
			a.diffOverlay.scrollY += pageHeight(a)
			return
		case KeyUp:
			a.diffOverlay.scrollY = maxi(0, a.diffOverlay.scrollY-1)
			return
		case KeyDown:
			a.diffOverlay.scrollY++
			return
		case KeyHome:
			a.diffOverlay.scrollY = 0
			return
		case KeyEnd:
			a.diffOverlay.scrollY = 1 << 30
			return
		}
		if ch == 'q' || ch == 'Q' {
			closeDiffOverlay(a)
		}
		return
	}

	// Help overlay: scrollable list of slash commands.
	if a.helpOverlay.open {
		handleHelpOverlayKey(a, e)
		return
	}

	// Full-screen picker overlays (rendered instead of main). Each consumes every key
	if a.palette.open {
		handlePaletteKey(a, k, ch)
		return
	}
	if p := a.activePicker(); p != nil {
		p.onKey(a, k, ch)
		return
	}
	if a.whichKey.open {
		handleWhichKeyKey(a, e)
		return
	}

	if len(a.completions) > 0 {
		handleCompletionKey(a, k, ch)
		return
	}

	if a.asking != nil {
		handleApprovalKey(a, k, ch)
		return
	}

	if isHomeQuickSwitchable(a) && k == KeyRune && ch >= '1' && ch <= '9' {
		idx := int(ch - '1')
		if idx < len(a.homeSessions) {
			switchSession(a, a.homeSessions[idx].ID)
		}
		return
	}

	// Leader prefix (opencode-style <ctrl+x>). When active, the next key is interpreted as a
	if a.leaderActive {
		a.leaderActive = false
		a.forceRender = true
		if k == KeyCtrlX || k == KeyEsc {
			return
		}
		applyLeaderBinding(a, k, ch)
		return
	}
	if k == KeyCtrlX {
		a.leaderActive = true
		a.forceRender = true
		return
	}

	// Esc is context-aware and never quits the app (quitting on a stray Esc — e.g. right after an
	// approval — is surprising and costs in-progress work). Busy: interrupt the current turn. Idle
	// with a draft: clear the input. Idle and empty: no-op. Quit is Ctrl+D or double Ctrl+C.
	if k == KeyEsc {
		if interruptCurrent(a) {
			return
		}
		if a.input.Value() != "" {
			a.input.SetValue("")
			a.histIdx = 0
		}
		return
	}
	if k == KeyCtrlD {
		a.quitting = true
		return
	}

	if k == KeyCtrlP {
		if a.palette.open {
			closePalette(a)
		} else {
			openPalette(a)
		}
		return
	}

	if isCtrlAltK(e) {
		if a.whichKey.open {
			closeWhichKey(a)
		} else {
			openWhichKey(a)
		}
		return
	}

	if a.input.Value() == "" && k == KeyCtrlN {
		scrollToNextMessage(a)
		return
	}
	if a.input.Value() == "" && k == KeyCtrlP {
		scrollToPrevMessage(a)
		return
	}

	switch k {
	case KeyPgUp:
		a.msgScroll = maxi(0, a.msgScroll-pageHeight(a))
		a.atBottom = false
		return
	case KeyPgDn:
		a.msgScroll += pageHeight(a)
		return
	case KeyCtrlU:
		a.msgScroll = maxi(0, a.msgScroll-pageHeight(a)/2)
		a.atBottom = false
		return
	case KeyHome:
		// When editing, Home moves the cursor to the start of the input line;
		if a.input.Value() != "" {
			a.input.CursorStart()
		} else {
			a.msgScroll = 0
			a.atBottom = false
		}
		return
	case KeyEnd:
		if a.input.Value() != "" {
			a.input.CursorEnd()
		} else {
			a.atBottom = true
		}
		return
	}

	// Up/Down navigates input history (readline-style): works regardless of whether the input
	// has content. The current draft is saved on first Up and restored on Down past the newest
	// entry, so browsing history never loses what the user was typing.
	if len(a.history) > 0 {
		switch k {
		case KeyUp:
			if a.histIdx < len(a.history) {
				if a.histIdx == 0 {
					a.histDraft = a.input.Value()
				}
				a.histIdx++
				a.input.SetValue(a.history[len(a.history)-a.histIdx])
			}
			return
		case KeyDown:
			if a.histIdx > 0 {
				a.histIdx--
				if a.histIdx == 0 {
					a.input.SetValue(a.histDraft)
				} else {
					a.input.SetValue(a.history[len(a.history)-a.histIdx])
				}
			}
			return
		}
	}

	if k == KeyTab {
		v := a.input.Value()
		if strings.HasPrefix(v, "/") {
			cands := matchCommands(v)
			if len(cands) == 1 {
				a.input.SetValue(cands[0])
			} else if len(cands) > 1 {
				a.completions = cands
				a.compIdx = 0
				a.compScroll = 0
			}
		}
		return
	}

	if k == KeyBacktab {
		cyclePermissionMode(a)
		return
	}

	if k == KeyCtrlT {
		cycleThinkingMode(a)
		return
	}
	if k == KeyCtrlY {
		if text := lastAssistantText(a); text != "" {
			osc52Copy(a.screen, text)
		}
		return
	}
	// Ctrl+C: interrupt or double-tap exit.
	if k == KeyCtrlC {
		handleCtrlC(a)
		return
	}
	if k == KeyCtrlW {
		a.input.WordLeft()
		return
	}
	if k == KeyCtrlE {
		toggleLastToolExpanded(a)
		return
	}

	if k == KeyEnter {
		if e.Modifiers()&ModAlt != 0 {
			a.input.InsertRune('\n')
			return
		}
		submitInput(a)
		return
	}

	switch k {
	case KeyBackspace, KeyBackspace2:
		a.input.Backspace()
		refineCompletions(a)
	case KeyDelete:
		a.input.Delete()
		refineCompletions(a)
	case KeyLeft:
		a.input.CursorLeft()
	case KeyRight:
		a.input.CursorRight()
	// Home/End are handled in the scroll section above (context-aware: cursor
	// move when editing, scroll when idle — they never reach this switch).
	case KeyRune:
		a.input.InsertRune(ch)
		refineCompletions(a)
	default:
	}
}

// handleCtrlC implements double-tap-within-500ms exit, single-tap interrupt.
func handleCtrlC(a *App) {
	now := time.Now()
	if !a.lastCtrlC.IsZero() && now.Sub(a.lastCtrlC) < 500*time.Millisecond {
		a.quitting = true
		return
	}
	a.lastCtrlC = now
	if !interruptCurrent(a) {
		a.addSystem(i18n.T("msg.press_ctrlc_again"))
	}
}

// interruptCurrent cancels the in-flight turn (if any): it cancels the per-turn context (stopping the
// agent loop, running tools, and any pending approval wait), bumps streamGen so the cancelled stream's
// late events are dropped, finalizes the streaming block, and returns to idle. Returns true if a turn
// was actually interrupted, false when already idle. Runs on the event-loop goroutine.
func interruptCurrent(a *App) bool {
	if a.status == statusIdle {
		return false
	}
	if a.streamCancel != nil {
		a.streamCancel()
		a.streamCancel = nil
	}
	// Invalidate the cancelled stream so its trailing events / end signal are ignored by the loop.
	a.streamGen++
	// Release a pending approval so the approver goroutine unblocks immediately (ctx cancel also
	// unblocks it, but denying is explicit and avoids a race on the reply channel).
	if a.asking != nil {
		sendReply(a.asking.replyCh, false)
		a.asking = nil
	}
	if a.current != nil {
		for i := range a.current.tools {
			if a.current.tools[i].status == toolRunning {
				a.current.tools[i].status = toolError
				a.current.tools[i].errMsg = i18n.T("tool.interrupted")
			}
		}
		a.current.finalize(a.rt.md)
		a.messages = append(a.messages, *a.current)
		a.current = nil
	}
	a.addSystem(i18n.T("msg.interrupted"))
	a.status = statusIdle
	a.forceRender = true
	return true
}

func cycleThinkingMode(a *App) {
	var target *msgBlock
	if a.current != nil && a.current.thinking.Len() > 0 {
		target = a.current
	} else {
		for i := len(a.messages) - 1; i >= 0; i-- {
			if a.messages[i].kind == kindAssistant && a.messages[i].thinking.Len() > 0 {
				target = &a.messages[i]
				break
			}
		}
	}
	if target == nil {
		return
	}
	target.thinkingMode = (target.thinkingMode + 1) % 3
}

// pageHeight returns the message-area height (used for PgUp/PgDn), approximated as terminal height
// minus a fixed chrome budget. Precise value isn't critical for paging.
func pageHeight(a *App) int {
	if a.height <= 8 {
		return 1
	}
	return a.height - 8
}

func handleHelpOverlayKey(a *App, e *EventKey) {
	k := e.Key()
	ch := e.Rune()
	switch k {
	case KeyEsc, KeyEnter:
		closeHelpOverlay(a)
		return
	case KeyPgUp:
		a.helpOverlay.scroll = maxi(0, a.helpOverlay.scroll-pageHeight(a))
		return
	case KeyPgDn:
		a.helpOverlay.scroll += pageHeight(a)
		return
	case KeyUp:
		if a.helpOverlay.scroll > 0 {
			a.helpOverlay.scroll--
		}
		return
	case KeyDown:
		a.helpOverlay.scroll++
		return
	case KeyHome:
		a.helpOverlay.scroll = 0
		return
	case KeyEnd:
		a.helpOverlay.scroll = 1 << 30
		return
	case KeyRune:
		switch ch {
		case 'q', 'Q':
			closeHelpOverlay(a)
		case ' ':
			a.helpOverlay.scroll += pageHeight(a)
		case 'b', 'B':
			a.helpOverlay.scroll = maxi(0, a.helpOverlay.scroll-pageHeight(a))
		}
	}
}

const compPageSize = 10

func handleCompletionKey(a *App, k Key, ch rune) {
	n := len(a.completions)
	switch k {
	case KeyUp:
		if a.compIdx > 0 {
			a.compIdx--
		}
		ensureCompVisible(a)
		previewCompletion(a)
		return
	case KeyDown:
		if a.compIdx < n-1 {
			a.compIdx++
		}
		ensureCompVisible(a)
		previewCompletion(a)
		return
	case KeyPgUp:
		a.compIdx = maxi(0, a.compIdx-compPageSize)
		ensureCompVisible(a)
		previewCompletion(a)
		return
	case KeyPgDn:
		a.compIdx = mini(n-1, a.compIdx+compPageSize)
		ensureCompVisible(a)
		previewCompletion(a)
		return
	case KeyHome:
		a.compIdx = 0
		ensureCompVisible(a)
		previewCompletion(a)
		return
	case KeyEnd:
		a.compIdx = maxi(0, n-1)
		ensureCompVisible(a)
		previewCompletion(a)
		return
	case KeyTab, KeyEnter:
		val := a.completions[a.compIdx]
		switch a.completionsKind {
		case compSlash:
			if e := findCommand(val); e != nil && e.hasArg {
				val += " "
			}
			a.input.SetValue(val)
			a.completions = nil
			a.completionsKind = compNone
		case compFile:
			// rewrite the @<query> token to @<dir>/ and re-refine.
			if k == KeyTab && strings.HasSuffix(val, "/") {
				v := a.input.Value()
				at := mentionTriggerIndex(v)
				if at >= 0 {
					a.input.SetValue(v[:at] + "@" + val)
				}
				refineCompletions(a)
				return
			}
			// Replace the @<query> token with @<path>  (trailing space).
			// carry it forward so the commit preserves the range.
			v := a.input.Value()
			at := mentionTriggerIndex(v)
			if at >= 0 {
				query := extractMentionQuery(v)
				if suffix, ok := preserveMentionSuffix(query, val); ok {
					val += suffix
				}
				a.input.SetValue(v[:at] + "@" + val + " ")
			} else {
				a.input.SetValue(v + val)
			}
			a.completions = nil
			a.completionsKind = compNone
		default:
			a.input.SetValue(val)
			a.completions = nil
			a.completionsKind = compNone
		}
		return
	case KeyEsc:
		a.completions = nil
		return
	case KeyRune:
		a.completions = nil
		a.input.InsertRune(ch)
		refineCompletions(a)
		return
	case KeyBackspace, KeyBackspace2:
		a.completions = nil
		a.input.Backspace()
		refineCompletions(a)
		return
	}
}

func ensureCompVisible(a *App) {
	if a.compIdx < a.compScroll {
		a.compScroll = a.compIdx
	}
	if a.compIdx >= a.compScroll+compPageSize {
		a.compScroll = a.compIdx - compPageSize + 1
	}
}

// previewCompletion updates the input box to mirror the currently highlighted completion item
// without closing the menu, so the user sees what they are about to commit while navigating.
func previewCompletion(a *App) {
	// File (@mention) completions are paths spliced into surrounding input; mirroring a candidate
	// into the input box would clobber the "@" token and any leading text. Only preview slash
	// commands, where the candidate is the entire input. Tab/Enter still rewrites the @mention token
	// via handleCompletionKey's compFile branch.
	if a.completionsKind == compFile {
		return
	}
	if len(a.completions) == 0 || a.compIdx < 0 || a.compIdx >= len(a.completions) {
		return
	}
	val := a.completions[a.compIdx]
	a.input.SetValue(val)
}

func maxi(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// isHomeQuickSwitchable reports whether the app is on the home (empty) screen in a state where the
// digit-key (1-9) session quick-switch is active. Requires: idle, no streaming block, no messages,
// empty input, no open completion menu, no open overlay/picker, and at least one home session.
func isHomeQuickSwitchable(a *App) bool {
	return a.status == statusIdle &&
		a.current == nil &&
		len(a.messages) == 0 &&
		a.input.Value() == "" &&
		len(a.completions) == 0 &&
		!a.palette.open &&
		!a.sessionPicker.open &&
		!a.modelPicker.open &&
		!a.mcpPicker.open &&
		!a.agentsPicker.open &&
		!a.variantPicker.open &&
		!a.modePicker.open &&
		!a.themePicker.open &&
		!a.whichKey.open &&
		!a.helpOverlay.open &&
		!a.diffOverlay.open &&
		a.asking == nil &&
		len(a.homeSessions) > 0
}

func mini(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func toggleLastToolExpanded(a *App) {
	findDoneIdx := func(b *msgBlock) int {
		for j := len(b.tools) - 1; j >= 0; j-- {
			if b.tools[j].status == toolDone && b.tools[j].result != "" {
				return j
			}
		}
		return -1
	}
	if a.current != nil {
		if j := findDoneIdx(a.current); j >= 0 {
			a.current.tools[j].expanded = !a.current.tools[j].expanded
			a.forceRender = true
			return
		}
	}
	for i := len(a.messages) - 1; i >= 0; i-- {
		b := &a.messages[i]
		if j := findDoneIdx(b); j >= 0 {
			b.tools[j].expanded = !b.tools[j].expanded
			a.forceRender = true
			ensureBlockVisible(a, i)
			return
		}
	}
}

func ensureBlockVisible(a *App, blockIdx int) {
	contentW := maxi(4, a.width-4)
	positions := messageLinePositions(a, contentW)
	if blockIdx < 0 || blockIdx >= len(positions) {
		return
	}
	blockStart := positions[blockIdx]
	page := pageHeight(a)
	if page <= 0 {
		return
	}
	if blockStart < a.msgScroll {
		a.msgScroll = blockStart
		a.atBottom = false
	} else if blockStart >= a.msgScroll+page {
		target := blockStart - page/2
		if target < 0 {
			target = 0
		}
		a.msgScroll = target
		a.atBottom = false
	}
}

func messageLinePositions(a *App, contentW int) []int {
	allBlocks := make([]msgBlock, 0, len(a.messages)+1)
	allBlocks = append(allBlocks, a.messages...)
	if a.current != nil {
		allBlocks = append(allBlocks, *a.current)
	}
	pos := 0
	positions := make([]int, 0, len(allBlocks)+1)
	positions = append(positions, 0) // top of first message
	for i := range allBlocks {
		lines := renderBlockLinesClaude(&allBlocks[i], a.rt.md, contentW, a.spinnerFrame)
		pos += len(lines) + 1 // +1 for the blank separator line
		positions = append(positions, pos)
	}
	return positions
}

func scrollToNextMessage(a *App) {
	if len(a.messages) == 0 && a.current == nil {
		return
	}
	contentW := maxi(4, a.width-4)
	positions := messageLinePositions(a, contentW)
	center := a.msgScroll + pageHeight(a)/2
	for _, p := range positions {
		if p > center {
			a.msgScroll = p
			a.atBottom = false
			a.forceRender = true
			return
		}
	}
	a.atBottom = true
	a.forceRender = true
}

func scrollToPrevMessage(a *App) {
	if len(a.messages) == 0 && a.current == nil {
		return
	}
	contentW := maxi(4, a.width-4)
	positions := messageLinePositions(a, contentW)
	center := a.msgScroll + pageHeight(a)/2
	prev := 0
	for _, p := range positions {
		if p >= center {
			break
		}
		prev = p
	}
	a.msgScroll = prev
	a.atBottom = false
	a.forceRender = true
}

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

type whichKeyState struct {
	open    bool
	scrollY int // top visible row (across all groups)
}

func openWhichKey(a *App) {
	a.whichKey.open = true
	a.whichKey.scrollY = 0
	a.forceRender = true
}

func closeWhichKey(a *App) {
	a.whichKey.open = false
	a.forceRender = true
}

// closeDiffOverlay closes the full-screen diff overlay. forceRender requests a full repaint so the
// next frame is a clean Sync of the (cleared) main view, leaving no overlay glyphs behind.
func closeDiffOverlay(a *App) {
	a.diffOverlay.open = false
	a.forceRender = true
}

// closeHelpOverlay closes the full-screen help overlay (same repaint rationale as the diff overlay).
func closeHelpOverlay(a *App) {
	a.helpOverlay.open = false
	a.forceRender = true
}

func handleWhichKeyKey(a *App, e *EventKey) {
	k := e.Key()
	ch := e.Rune()
	switch k {
	case KeyEsc:
		closeWhichKey(a)
		return
	case KeyUp:
		a.whichKey.scrollY = maxi(0, a.whichKey.scrollY-1)
		return
	case KeyDown:
		a.whichKey.scrollY++
		return
	case KeyPgUp:
		a.whichKey.scrollY = maxi(0, a.whichKey.scrollY-whichKeyPageHeight(a))
		return
	case KeyPgDn:
		a.whichKey.scrollY += whichKeyPageHeight(a)
		return
	case KeyHome:
		a.whichKey.scrollY = 0
		return
	case KeyEnd:
		a.whichKey.scrollY = 1 << 30
		return
	}
	if isCtrlAltK(e) {
		closeWhichKey(a)
		return
	}
	if ch == 'q' || ch == 'Q' {
		closeWhichKey(a)
		return
	}
}

func isCtrlAltK(e *EventKey) bool {
	if e.Key() == KeyCtrlK && e.Modifiers()&(ModCtrl|ModAlt) == ModCtrl|ModAlt {
		return true
	}
	if e.Key() == KeyRune && (e.Rune() == 'k' || e.Rune() == 'K') && e.Modifiers()&(ModCtrl|ModAlt) == ModCtrl|ModAlt {
		return true
	}
	return false
}

// whichKeyPageHeight is the number of list rows visible in the panel (mirrors the layout budget in
// drawWhichKey: box height minus title/footer chrome).
func whichKeyPageHeight(a *App) int {
	h := whichKeyBoxHeight(a) - 4 // title + top pad + bottom footer + bottom pad
	if h < 1 {
		h = 1
	}
	return h
}

// whichKeyBoxWidth/Height size the panel. Slightly larger than the pickers since it lists many
// rows; capped to the screen.
func whichKeyBoxWidth(a *App) int {
	w := a.width * 3 / 4
	if w < 50 {
		w = 50
	}
	if w > a.width-2 {
		w = maxi(50, a.width-2)
	}
	return w
}

func whichKeyBoxHeight(a *App) int {
	h := a.height * 4 / 5
	if h < 10 {
		h = 10
	}
	if h > a.height-2 {
		h = maxi(10, a.height-2)
	}
	return h
}

// drawWhichKey draws the Ctrl+Alt+K keybindings panel: a centered overlay listing every keybinding
// grouped by category. Groups are rendered top-to-bottom with a bold header each; the list scrolls
// as one when it overflows the box. ASCII borders only (CJK-width-safe, consistent with drawPalette).
func drawWhichKey(a *App) {
	w, h := a.screen.Size()
	if w < 20 || h < 8 {
		return
	}
	boxW := whichKeyBoxWidth(a)
	boxH := whichKeyBoxHeight(a)
	boxX := (w - boxW) / 2
	boxY := (h - boxH) / 2

	savedBg := a.curBg
	a.curBg = pal().frameBg
	clearScreen(a)
	a.curBg = pal().panelBg
	fillRect(a.screen, boxX, boxY, boxW, boxH, ' ', StyleDefault.Background(pal().panelBg))

	borderStyle := styleToolDim()
	drawTextRaw(a.screen, a, boxX, boxY, boxW, strings.Repeat("-", boxW), borderStyle)
	drawTextRaw(a.screen, a, boxX, boxY+boxH-1, boxW, strings.Repeat("-", boxW), borderStyle)
	for row := boxY + 1; row < boxY+boxH-1; row++ {
		drawTextRaw(a.screen, a, boxX, row, 1, "|", borderStyle)
		drawTextRaw(a.screen, a, boxX+boxW-1, row, 1, "|", borderStyle)
	}

	innerX := boxX + 2
	innerW := boxW - 4
	if innerW < 4 {
		innerW = 4
	}

	drawTextRaw(a.screen, a, innerX, boxY+1, innerW, i18n.T("keybindings.title"), stylePaletteAccent())

	type displayRow struct {
		isHeader bool
		text     string
	}
	var rows []displayRow
	// "key ......... desc" — pad the key column so descriptions align. Cap the key width so long
	// keys (e.g. "<leader>m", "Ctrl+D / Esc") don't squeeze the description to nothing.
	const keyCol = 18
	addBinding := func(b keyBinding) {
		keyField := b.key
		if len(keyField) > keyCol-1 {
			keyField = keyField[:keyCol-1]
		}
		pad := keyCol - len(keyField)
		if pad < 1 {
			pad = 1
		}
		rows = append(rows, displayRow{text: keyField + strings.Repeat(" ", pad) + b.desc})
	}
	for _, g := range keyBindingGroups() {
		rows = append(rows, displayRow{isHeader: true, text: g})
		for _, b := range bindingsByGroup(g) {
			addBinding(b)
		}
	}
	// Leader (Ctrl+X) sequences — built dynamically from the dispatch table so the panel can never
	// drift from the actual bindings.
	rows = append(rows, displayRow{isHeader: true, text: i18n.T("key.group.leader")})
	for _, b := range leaderBindingsForDisplay() {
		addBinding(b)
	}

	listTop := boxY + 3
	listH := boxH - 5 // title + top pad + footer + bottom pad
	if listH < 1 {
		listH = 1
	}
	maxScroll := maxi(0, len(rows)-listH)
	if a.whichKey.scrollY < 0 {
		a.whichKey.scrollY = 0
	}
	if a.whichKey.scrollY > maxScroll {
		a.whichKey.scrollY = maxScroll
	}
	top := a.whichKey.scrollY
	for i := 0; i < listH; i++ {
		idx := top + i
		ry := listTop + i
		if idx < 0 || idx >= len(rows) {
			fillRect(a.screen, innerX, ry, innerW, 1, ' ', StyleDefault.Background(pal().panelBg))
			continue
		}
		r := rows[idx]
		st := stylePaletteItem()
		if r.isHeader {
			st = stylePaletteAccent()
		}
		drawTextRaw(a.screen, a, innerX, ry, innerW, r.text, st)
	}

	footerY := boxY + boxH - 2
	drawTextRaw(a.screen, a, innerX, footerY, innerW, i18n.T("keybindings.footer"), styleToolDim())

	a.curBg = savedBg
}
