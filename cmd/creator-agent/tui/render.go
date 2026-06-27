package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/clipperhouse/uax29/v2/graphemes"
	"github.com/skys-mission/creator-agent/cmd/creator-agent/tui/i18n"
	"github.com/skys-mission/creator-agent/core"
)

var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// render draws the current frame to the screen.
// render redraws the back buffer from App state and flushes it. It uses an incremental flush
// (Show) by default so only changed cells are emitted — this avoids flicker and cursor jitter on
// every repaint. A full Sync (every cell re-emitted) is used when a.forceRender is set (startup,
// resize, or overlay switches), which runLoop clears after one such frame.
func render(a *App) {
	if a.quitting {
		clearScreen(a)
		drawExitSummary(a)
		_ = a.screen.Sync()
		a.forceRender = false
		a.lastFrameOverlay = true
		return
	}
	if a.diffOverlay.open {
		drawDiffOverlay(a)
		_ = a.screen.Sync()
		a.forceRender = false
		a.lastFrameOverlay = true
		return
	}
	if a.helpOverlay.open {
		drawHelpOverlay(a)
		_ = a.screen.Sync()
		a.forceRender = false
		a.lastFrameOverlay = true
		return
	}
	if a.palette.open {
		drawPalette(a)
		_ = a.screen.Sync()
		a.forceRender = false
		a.lastFrameOverlay = true
		return
	}
	if p := a.activePicker(); p != nil {
		p.render(a)
		_ = a.screen.Sync()
		a.forceRender = false
		a.lastFrameOverlay = true
		return
	}
	if a.whichKey.open {
		drawWhichKey(a)
		_ = a.screen.Sync()
		a.forceRender = false
		a.lastFrameOverlay = true
		return
	}
	drawMain(a)
	if a.forceRender {
		_ = a.screen.Sync()
		a.forceRender = false
	} else {
		_ = a.screen.Show()
	}
	a.lastFrameOverlay = false
}

func clearScreen(a *App) {
	w, h := a.screen.Size()
	fillRect(a.screen, 0, 0, w, h, ' ', StyleDefault)
}

// drawMain draws the standard main-area layout (Claude Code style).
//
// It owns screen partitioning (padX insets, main column rect) and the bottom block (input box or
// approval prompt). The Claude layout keeps the message area on the terminal's default background
// so emoji/wide-glyph width divergence cannot produce a visible drift — there is no row-wide
// painted background to reveal the desync.
//
//	+------------------------------------------------------------+
//	| main area content                                          |
//	| ...                                                        |
//	+------------------------------------------------------------+
//	| bottom block: input box or approval prompt                 |
//	+------------------------------------------------------------+
func drawMain(a *App) {
	w, h := a.screen.Size()
	if w < 1 || h < 1 {
		return
	}

	// The screen is front/back-diffed and never auto-cleared, so any cell this view doesn't repaint
	// keeps the previous frame's glyph. The main view repaints its own rect every frame
	// (drawScrollable fills blank rows too), so the only cells it leaves untouched are the left/right
	// insets — which only a full-screen overlay (or a size change) can dirty. Clear once on those
	// transitions: on overlay-exit (lastFrameOverlay) and on forced full repaints (resize/theme
	// switch). Steady-state frames skip the clear. The clear uses the terminal default background
	// (no color) so it cannot reintroduce the emoji drift a colored fill would cause.
	if a.lastFrameOverlay || a.forceRender {
		fillRect(a.screen, 0, 0, w, h, ' ', StyleDefault)
	}
	a.curBg = ColorDefault

	const padX = 2
	mainX := padX
	mainW := w - padX*2
	if mainW < 4 {
		mainW = 4
	}

	bottomH, isApproval := claudeBottomHeight(a, mainW, h)
	bodyH := h - bottomH
	if bodyH < 1 {
		bodyH = 1
	}

	drawMainColumn(a, mainX, 0, mainW, bodyH)

	// Bottom block: the prompt, or — when a permission request is pending — the approval block.
	a.curBg = ColorDefault
	if isApproval {
		view := buildApprovalView(a, mainW)
		drawApprovalBlockClaude(a, mainX, bodyH, mainW, bottomH, view)
	} else {
		drawInputBoxClaude(a, mainX, bodyH, mainW, bottomH)
	}
}

// drawApprovalLinesAnchored paints approval lines within the block body [y+1, y+height), anchoring
// the trailing question + options to the bottom. buildApprovalView emits lines as [title, path?,
// preview..., question, options, hint] with tailCount marking the trailing must-keep rows; when the
// preview overflows the block the head (title + preview start) and the tail (question + options) are
// kept and the preview middle is dropped, so the path and the action options are never truncated.
// Without this, a large write paints only preview rows and hides the choices, leaving the user
// unable to act on the request. indent is the left inset past the block edge; baseStyle is the
// per-line base style applied to every row.
func drawApprovalLinesAnchored(a *App, x, y, width, height, indent int, baseStyle Style, view approvalView) {
	lines := view.lines
	avail := height - 1 // rows below the top rule
	if avail <= 0 || len(lines) == 0 {
		return
	}
	textX := x + indent
	textW := width - indent
	if textW < 1 {
		textW = 1
	}
	drawRow := func(row int, ln lineRuns) {
		drawText(a, textX, y+1+row, textW, baseStyle, ln.runs)
	}
	n := len(lines)
	if n <= avail {
		for i := 0; i < n; i++ {
			drawRow(i, lines[i])
		}
		return
	}
	// Overflow: reserve the bottom rows for the question + options; fill the rest from the top with
	// the title + preview head. The preview middle is dropped (the full diff remains viewable via 'd').
	tail := view.tailCount
	if tail < 1 {
		tail = 1
	}
	if tail > avail {
		tail = avail
	}
	headRows := avail - tail
	if max := n - tail; headRows > max {
		headRows = max
	}
	if headRows < 0 {
		headRows = 0
	}
	for i := 0; i < headRows; i++ {
		drawRow(i, lines[i])
	}
	for i := 0; i < tail; i++ {
		drawRow(headRows+i, lines[n-tail+i])
	}
}

// drawMainColumn draws the main column body (everything above the input box) at (x0,y0) with the
// given width/height. Message content comes from buildMessageLinesClaude; all other regions (error
// bar, completions, status line) are shared geometry.
//
// Layout is anchored BOTTOM-UP to keep the bottom structure (status line, approval, tool cards)
// stable regardless of message-area size — the previous top-down accumulation misaligned regions
// whenever a middle region's height estimate was off, causing overlapping / duplicated rows. Now:
//
//	y=0:          header (status bar) + blank
//	y=2..bottomY: message viewport (fills all remaining space)
//	bottomY..h-1: tool cards → approval → error bar → completions → status line
//
// Each region is placed at a known y computed from the bottom, so they never overlap.
func drawMainColumn(a *App, x0, y0, width, height int) {
	errBar := renderErrorBar(a)
	completionStrs := renderCompletionLines(a.completions, a.compIdx, a.compScroll, width)
	statusLine := renderStatusLine(a)

	errBarH := 0
	if errBar != "" {
		errBarH = 2
	}
	compH := 0
	if len(completionStrs) > 0 {
		compH = len(completionStrs) + 1
	}
	statusH := 0
	if statusLine != "" {
		statusH = 2
	}

	// Assign y positions bottom-up with a single cursor.
	cursor := height - 1

	statusY := cursor
	if statusH > 0 {
		statusY = cursor - statusH + 1
		cursor = statusY - 1
	}
	compY := cursor + 1
	if compH > 0 {
		compY = cursor - compH + 1
		cursor = compY - 1
	}
	errY := cursor + 1
	if errBarH > 0 {
		errY = cursor - errBarH + 1
		cursor = errY - 1
	}
	// message area: from y0+1 (top breathing room) down to cursor.
	msgTop := y0 + 1
	msgBottom := cursor
	msgH := msgBottom - msgTop + 1
	if msgH < 1 {
		msgH = 1
	}

	lines := buildMessageLinesClaude(a, width)
	drawScrollable(a, x0, msgTop, width, msgH, lines, &a.msgScroll, &a.atBottom)

	if errBar != "" {
		drawTextRaw(a.screen, a, x0, errY, width, errBar, styleErrorBar())
	}
	if len(completionStrs) > 0 {
		// Clear the completion area before drawing so that shorter replacement lines do not leave
		// trailing characters from the previous frame.
		fillRect(a.screen, x0, compY, width, len(completionStrs), ' ', StyleDefault.Background(a.curBg))
		for i, ln := range completionStrs {
			drawText(a, x0, compY+i, width, ln.style, ln.runs)
		}
	}
	if statusLine != "" {
		drawTextRaw(a.screen, a, x0, statusY, width, statusLine, styleStatusBar())
	}
}

// lineRuns is a rendered line (styled runs) + a default style fallback.
type lineRuns struct {
	runs  []styledRun
	style Style
}

// drawText writes styled runs at (x,y) within width columns. All writes go through a transient
// Surface covering exactly the [x,y,width,1] band with a.curBg, so a wide (CJK/emoji) rune that
// does not fully fit before x+width is dropped instead of spilling its follow cell into a neighbor
// — this is the fix for the persistent border-misalignment defect. Runs whose style is
// StyleDefault fall back to defStyle (which carries an explicit or region background).
func drawText(a *App, x, y, width int, defStyle Style, runs []styledRun) {
	if width <= 0 {
		return
	}
	bg := withRegionBg(a, defStyle)
	s := &Surface{screen: a.screen, rect: Rect{x, y, width, 1}, bg: styleBg(bg)}
	cx := 0
	for _, r := range runs {
		st := r.style
		if st == StyleDefault {
			st = bg
		}
		for _, ch := range r.text {
			w := runeW(ch)
			if w <= 0 {
				continue
			}
			if cx+w > width {
				return
			}
			ax := x + cx
			if y >= 0 {
				// SetContent auto-fills the follow cell of a width-2 rune (see Screen.SetContent).
				a.screen.SetContent(ax, y, ch, nil, compositeBg(s, st))
			}
			cx += w
		}
	}
}

// drawTextRaw writes a plain string at (x,y) within width columns with one style. Routed through
// the same wide-rune-safe clipping as drawText via a transient Surface band. Composites the region
// background under styles that have no explicit bg.
func drawTextRaw(s *Screen, a *App, x, y, width int, text string, st Style) {
	if width <= 0 {
		return
	}
	bg := withRegionBg(a, st)
	surf := &Surface{screen: s, rect: Rect{x, y, width, 1}, bg: styleBg(bg)}
	cx := 0
	for _, ch := range text {
		w := runeW(ch)
		if w <= 0 {
			continue
		}
		if cx+w > width {
			return
		}
		ax := x + cx
		if y >= 0 {
			// SetContent auto-fills the follow cell of a width-2 rune (see Screen.SetContent).
			s.SetContent(ax, y, ch, nil, compositeBg(surf, st))
		}
		cx += w
	}
}

func withRegionBg(a *App, st Style) Style {
	_, bg, _ := st.Decompose()
	if bg != ColorDefault {
		return st
	}
	return st.Background(a.curBg)
}

// fillRect clears a rectangle with the given rune/style, clipped to the screen. Routed through
// Surface.FillRectLocal semantics so it never writes outside the valid screen area.
func fillRect(s *Screen, x, y, w, h int, ch rune, st Style) {
	if w <= 0 || h <= 0 {
		return
	}
	surf := &Surface{screen: s, rect: Rect{x, y, w, h}, bg: ColorDefault}
	surf.FillRectLocal(Rect{0, 0, w, h}, ch, st)
}

// drawExitSummary draws the goodbye token/cost/changes summary on quit.
func drawExitSummary(a *App) {
	w, _ := a.screen.Size()
	y := 0
	drawTextRaw(a.screen, a, 0, y, w, i18n.T("exit.goodbye"), styleHeader())
	y += 2
	drawTextRaw(a.screen, a, 0, y, w, fmt.Sprintf(i18n.T("exit.tokens"), a.totalIn, a.totalOut), styleToolDim())
	if c := costEstimateStr(a.rt.prof.Model, a.totalIn, a.totalOut); c != "" {
		drawTextRaw(a.screen, a, 0, y+1, w, i18n.T("exit.cost")+c, styleToolDim())
	}
	if len(a.changes) > 0 {
		seen := make(map[string]bool, len(a.changes))
		for _, c := range a.changes {
			seen[c.path] = true
		}
		drawTextRaw(a.screen, a, 0, y+2, w, fmt.Sprintf(i18n.T("exit.changes"), len(seen)), styleToolDim())
	}
}

func profLabel(p profile) string {
	if p.Name != "" {
		return p.Name
	}
	return p.Model
}

// renderHomeLines builds the home (empty-state) screen: a centered brand, the current agent/model
// context, a tip of the day, recent sessions (1-9 quick-switch), example prompts, and a key-hint
// footer. Recent sessions are resolved from the session store (newest first, current excluded) and
// cached on the app so the digit-key quick-switch handler can map a key to a session id.
//
// Mirrors opencode's home layout (logo + prompt-area context + tip) and adds a recent-sessions list
// (an improvement: opencode surfaces sessions only in a separate /sessions modal).
func renderHomeLines(a *App, width int) []styledLine {
	// Resolve + cache recent sessions for the digit-key quick-switch handler. Run once per home
	a.homeSessions = resolveHomeSessions(a)

	var out []styledLine

	// Compact context line: one all-muted row carrying the bits NOT already shown in the always-on
	// prompt footer (host + variant). The prompt footer already shows agent · model, so they are
	// echoed here only to anchor the home view; host/variant stay because they appear nowhere else.
	ctxLine := styledLine{}
	ctxLine.appendRun(orDash(a.rt.currentAgent), styleToolDim())
	modelLabel := a.rt.prof.Model
	if modelLabel == "" {
		modelLabel = a.rt.prof.Name
	}
	ctxLine.appendRun(" · "+modelLabel, styleToolDim())
	if h := hostOf(a.rt.prof.BaseURL); h != "" {
		ctxLine.appendRun(" @ "+h, styleToolDim())
	}
	vn := normalizeLower(a.rt.currentVariant)
	if vn != "" && vn != "default" {
		ctxLine.appendRun(i18n.T("home.variant")+a.rt.currentVariant, styleToolDim())
	}
	out = append(out, ctxLine)

	out = append(out, styledLine{})

	tipLine := styledLine{}
	tipLine.appendRun(i18n.T("home.tip_label"), styleToolName())
	tipLine.appendRun(pickHomeTip(a), styleToolDim())
	out = append(out, tipLine)

	if len(a.homeSessions) > 0 {
		out = append(out, styledLine{})
		out = append(out, lineFromRaw(i18n.T("home.recent"), styleToolName()))
		infos := homeRecentInfos(a)
		for i, info := range infos {
			if i >= 9 {
				break
			}
			row := styledLine{}
			row.appendRun(fmt.Sprintf("  %d  ", i+1), styleToolName())
			row.appendRun(homeTitleOrFallback(info.Title), styleAssistant())
			ts := relativeTime(info.UpdatedAt)
			if ts != "" {
				row.appendRun("  "+ts, styleToolDim())
			}
			row.appendRun("  "+shortID(info.ID), styleToolDim())
			out = append(out, row)
		}
	}

	out = append(out, styledLine{})
	return out
}

func homeRecentInfos(a *App) []core.SessionInfo {
	if a.rt.store == nil {
		return nil
	}
	infos, err := a.rt.store.List()
	if err != nil {
		return nil
	}
	out := make([]core.SessionInfo, 0, mini(len(infos), 9))
	for _, info := range infos {
		if info.ID == a.rt.sessionID {
			continue
		}
		out = append(out, info)
		if len(out) >= 9 {
			break
		}
	}
	return out
}

// resolveHomeSessions caches the recent-session entries (id + title) for the digit-key handler.
// Mirrors homeRecentInfos so the cached ids match the rendered rows exactly.
func resolveHomeSessions(a *App) []homeSessionEntry {
	infos := homeRecentInfos(a)
	if len(infos) == 0 {
		return nil
	}
	out := make([]homeSessionEntry, 0, len(infos))
	for _, info := range infos {
		out = append(out, homeSessionEntry{ID: info.ID, Title: homeTitleOrFallback(info.Title)})
	}
	return out
}

func homeTitleOrFallback(title string) string {
	if title == "" || core.IsDefaultSessionTitle(title) {
		return i18n.T("home.untitled")
	}
	return title
}

func drawPromptFooter(a *App, x, y, width int) {
	left := styledLine{}
	if a.leaderActive {
		left.appendRun(i18n.T("footer.leader_prefix"), styleAccent())
		left.appendRun(i18n.T("footer.leader_hint"), styleTextMuted())
	} else if a.status != statusIdle || a.asking != nil {
		sp := spinnerFrameStr(a.spinnerFrame)
		left.appendRun(sp+" ", styleTextMuted())
		left.appendRun(promptStatusText(a), styleTextMuted())
	} else {
		agentName := a.rt.currentAgent
		if agentName == "" {
			agentName = "build"
		}
		left.appendRun(LocaleTitlecase(agentName), agentColorFor(agentName))
		modelLabel := a.rt.prof.Model
		if modelLabel == "" {
			modelLabel = a.rt.prof.Name
		}
		left.appendRun(" · "+modelLabel, styleTextMuted())
		if a.rt.modeCtl != nil {
			mode := currentModeNormalized(a)
			left.appendRun(" · "+mode, modeStyle(mode))
		}
	}
	drawText(a, x, y, width, StyleDefault, left.runs)

	right := ""
	if a.usage != "" {
		right = a.usage + "  "
	}
	right += i18n.T("footer.shortcuts")
	rw := strW(right)
	if x+width-rw >= x {
		drawTextRaw(a.screen, a, x+width-rw, y, rw, right, styleTextMuted())
	}
}

func promptStatusText(a *App) string {
	switch a.status {
	case statusThinking:
		if a.current != nil && a.current.content.Len() > 0 {
			return i18n.T("status.generating")
		}
		return i18n.T("status.thinking")
	case statusRunningTool:
		if a.current != nil {
			for i := range a.current.tools {
				if a.current.tools[i].status == toolRunning {
					return i18n.T("status.running_named", a.current.tools[i].name)
				}
			}
		}
		return i18n.T("status.running_tool")
	case statusCompacting:
		return i18n.T("status.compacting")
	case statusError:
		return i18n.T("status.error")
	}
	return ""
}

func inputVisualHeight(text string, width int) int {
	if width <= 0 {
		width = 1
	}
	lines := strings.Split(text, "\n")
	total := 0
	for _, l := range lines {
		w := strW(l)
		if w == 0 {
			total++
		} else {
			total += (w-1)/width + 1
		}
	}
	if total < 1 {
		total = 1
	}
	return total
}

func drawDiffOverlay(a *App) {
	clearScreen(a)
	w, h := a.screen.Size()
	drawTextRaw(a.screen, a, 0, 0, w, a.diffOverlay.title, styleDiffFile())
	footer := i18n.T("overlay.diff.footer")
	bodyH := h - 2
	lines := a.diffOverlay.lines
	top := a.diffOverlay.scrollY
	if top > len(lines)-bodyH {
		top = maxi(0, len(lines)-bodyH)
	}
	if top < 0 {
		top = 0
	}
	a.diffOverlay.scrollY = top
	for i := 0; i < bodyH; i++ {
		idx := top + i
		ry := 1 + i
		if idx >= 0 && idx < len(lines) {
			drawTextRaw(a.screen, a, 0, ry, w, lines[idx], diffLineStyle(lines[idx]))
		}
	}
	drawTextRaw(a.screen, a, 0, h-1, w, footer, styleToolDim())
}

func drawHelpOverlay(a *App) {
	clearScreen(a)
	w, h := a.screen.Size()
	drawTextRaw(a.screen, a, 0, 0, w, i18n.T("overlay.help.title"), styleHeader())
	y := 2
	drawTextRaw(a.screen, a, 0, y, w, i18n.T("overlay.help.section"), styleToolName())
	y++
	cmds := getSlashCommands()
	maxScroll := maxi(0, len(cmds)-(h-y-1))
	if a.helpOverlay.scroll > maxScroll {
		a.helpOverlay.scroll = maxScroll
	}
	if a.helpOverlay.scroll < 0 {
		a.helpOverlay.scroll = 0
	}
	for i := a.helpOverlay.scroll; i < len(cmds); i++ {
		c := cmds[i]
		drawTextRaw(a.screen, a, 0, y, w, fmt.Sprintf("  %-10s %s", c, commandDesc(c)), styleAssistant())
		y++
		if y >= h-1 {
			break
		}
	}
	footer := i18n.T("overlay.help.footer")
	if footer != "" {
		drawTextRaw(a.screen, a, 0, h-1, w, footer, styleTextMuted())
	}
}

func drawScrollable(a *App, x, y, width, h int, lines []styledLine, scroll *int, atBottom *bool) {
	n := len(lines)
	top := *scroll
	if *atBottom {
		top = n - h
		if top < 0 {
			top = 0
		}
		*scroll = top
	}
	if top > n-h {
		top = maxi(0, n-h)
	}
	if top < 0 {
		top = 0
	}
	if *atBottom && top+h >= n {
	} else if top+h < n {
		*atBottom = false
	}
	for i := 0; i < h; i++ {
		idx := top + i
		ry := y + i
		var line styledLine
		hasLine := idx >= 0 && idx < n
		if hasLine {
			line = lines[idx]
		}
		bg := line.lineBg
		if bg == ColorDefault {
			bg = a.curBg
		}
		fillRect(a.screen, x, ry, width, 1, ' ', StyleDefault.Background(bg))
		contentX := x
		contentW := width
		if line.barColor != ColorDefault && width >= 1 {
			barStyle := StyleDefault.Foreground(line.barColor).Background(bg)
			a.screen.SetContent(x, ry, '┃', nil, barStyle)
			off := line.indent
			if off < 1 {
				off = 1
			}
			if off > width {
				off = width
			}
			contentX = x + off
			contentW = width - off
		}
		if hasLine && len(line.runs) > 0 && contentW > 0 {
			drawText(a, contentX, ry, contentW, StyleDefault.Background(bg), line.runs)
		}
	}
}

func lineFromRaw(text string, st Style) styledLine {
	var sl styledLine
	sl.appendRun(text, st)
	return sl
}

// wrapPlain is a cheap word-wrap for streaming/plain text (grapheme-cluster-aware, CJK/emoji width 2).
// Iterates grapheme clusters (not runes) so ZWJ emoji sequences like 👨‍👩‍👧 are measured as a single
// unit with width 2, preventing inflated line widths that misalign dividers and borders.
func wrapPlain(s string, width int) []string {
	if width <= 1 {
		return []string{s}
	}
	var out []string
	for _, seg := range strings.Split(s, "\n") {
		var cur strings.Builder
		lineW := 0
		iter := graphemes.FromString(seg)
		for iter.Next() {
			cluster := iter.Value()
			cw := strW(cluster)
			if lineW+cw > width && cur.Len() > 0 {
				out = append(out, cur.String())
				cur.Reset()
				lineW = 0
			}
			cur.WriteString(cluster)
			lineW += cw
		}
		out = append(out, cur.String())
	}
	return out
}

// extractToolArgPreview extracts a key-field preview from streaming tool parameters.
func extractToolArgPreview(name, argsJSON string) string {
	if argsJSON == "" {
		return ""
	}
	info := parseToolInput(name, argsJSON)
	switch name {
	case "write":
		if info.newString != "" {
			return truncateStr(info.newString, 500)
		}
	case "edit":
		if info.newString != "" {
			return "-> " + truncateStr(info.newString, 300)
		}
	case "bash":
		if info.command != "" {
			return "$ " + truncateStr(info.command, 200)
		}
	}
	return truncateStr(argsJSON, 300)
}

func activeStatusStr(a *App) string {
	sp := spinnerFrameStr(a.spinnerFrame)
	switch a.status {
	case statusThinking:
		if a.current != nil && a.current.thinking.Len() > 0 && a.current.content.Len() == 0 {
			return i18n.T("active_status.thinking_with_chars", sp, a.current.thinking.Len())
		}
		if a.current != nil && a.current.content.Len() > 0 {
			return i18n.T("active_status.generating_with_chars", sp, a.current.content.Len())
		}
		return i18n.T("active_status.thinking", sp)
	case statusRunningTool:
		var name string
		var dur time.Duration
		if a.current != nil {
			for _, t := range a.current.tools {
				if t.status == toolRunning {
					name = t.name
					if !t.started.IsZero() {
						if d := time.Since(t.started); d > dur {
							dur = d
						}
					}
				}
			}
		}
		if name != "" {
			return i18n.T("active_status.executing_named", sp, name, durationStr(dur))
		}
		return i18n.T("active_status.executing_tool", sp)
	case statusCompacting:
		return i18n.T("active_status.compacting", sp)
	case statusError:
		return i18n.T("active_status.error")
	default:
		return i18n.T("active_status.idle")
	}
}

func spinnerFrameStr(i int) string {
	if i < 0 {
		i = 0
	}
	return spinnerFrames[i%len(spinnerFrames)]
}

func renderStatusLine(a *App) string {
	if a.status == statusIdle {
		return ""
	}
	elapsed := ""
	if !a.statusStarted.IsZero() {
		elapsed = " | " + durationStr(time.Since(a.statusStarted))
	}
	switch a.status {
	case statusThinking:
		if a.current != nil && a.current.thinking.Len() > 0 && a.current.content.Len() == 0 {
			return fmt.Sprintf(i18n.T("status_line.thinking_with_chars"), a.current.thinking.Len(), elapsed)
		} else if a.current != nil && a.current.content.Len() > 0 {
			return fmt.Sprintf(i18n.T("status_line.generating_with_chars"), a.current.content.Len(), elapsed)
		}
		return fmt.Sprintf(i18n.T("status_line.thinking"), elapsed)
	case statusRunningTool:
		if a.current != nil {
			for i := range a.current.tools {
				t := &a.current.tools[i]
				if t.status == toolRunning {
					dur := ""
					if !t.started.IsZero() {
						dur = " | " + durationStr(time.Since(t.started))
					}
					switch t.name {
					case "write", "edit":
						info := t.parsed()
						n := len(info.newString)
						if n == 0 {
							n = len(t.argsJSON)
						}
						return fmt.Sprintf(i18n.T("status_line.preparing"), t.name, n, dur)
					case "task":
						return fmt.Sprintf(i18n.T("status_line.subagent"), dur)
					default:
						return fmt.Sprintf(i18n.T("status_line.executing_named"), t.name, dur)
					}
				}
			}
		}
		return fmt.Sprintf(i18n.T("status_line.executing_tool"), elapsed)
	case statusError:
		return fmt.Sprintf(i18n.T("status_line.error"), elapsed)
	}
	return ""
}

func renderErrorBar(a *App) string {
	if a.err == nil {
		return ""
	}
	hint := coreUserHint(a.err)
	body := truncateStr(a.err.Error(), maxi(20, a.width-12-strW(hint)))
	return hint + body
}

func diffLineStyle(line string) Style {
	switch {
	case strings.HasPrefix(line, "+"):
		return styleDiffAdd()
	case strings.HasPrefix(line, "-"):
		return styleDiffDel()
	case strings.HasPrefix(line, "@@"):
		return styleDiffHunk()
	default:
		return styleToolDim()
	}
}

func renderCompletionLines(cands []string, sel, scroll, width int) []lineRuns {
	if len(cands) == 0 {
		return nil
	}
	end := mini(len(cands), scroll+compPageSize)
	visible := cands[scroll:end]
	var out []lineRuns
	for i, c := range visible {
		absIdx := scroll + i
		desc := commandDesc(c)
		style := styleCompItem()
		prefix := "  "
		if absIdx == sel {
			style = styleCompSelected()
			prefix = "> "
			// '>' is width-unambiguous; the arrow glyph '▶' is ambiguous-width.
		}
		out = append(out, lineRuns{runs: []styledRun{{fmt.Sprintf(prefix+"%-12s %s", c, desc), style}}})
	}
	if len(cands) > compPageSize {
		more := len(cands) - (scroll + len(visible))
		if more < 0 {
			more = 0
		}
		hint := fmt.Sprintf(i18n.T("completion.footer.more"), more)
		if hint == "" {
			hint = fmt.Sprintf("  ... %d more", more)
		}
		out = append(out, lineRuns{runs: []styledRun{{hint, styleTextMuted()}}})
	}
	return out
}

// extractToolKeyFieldFromInfo picks the display key field (path/command/description).
func extractToolKeyFieldFromInfo(name string, info toolCallInfo) string {
	if info.path != "" {
		return info.path
	}
	if info.command != "" {
		return truncateStr(info.command, 40)
	}
	if name == "task" && info.description != "" {
		return truncateStr(info.description, 50)
	}
	return ""
}
