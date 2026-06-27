package tui

import (
	"fmt"
	"runtime"
	"strings"

	"github.com/skys-mission/creator-agent/cmd/creator-agent/tui/i18n"
)

// blackCircleDot is the Claude-style per-turn affordance glyph. On darwin the filled circle ⏺
// aligns better; elsewhere ● is used (mirrors Claude Code's constants/figures.ts BLACK_CIRCLE).
func blackCircleDot() string {
	if runtime.GOOS == "darwin" {
		return "⏺"
	}
	return "●"
}

// claudeBottomHeight returns the row budget for the bottom block (input box or approval prompt) and
// whether it is the approval block. The Claude input box is two horizontal rules (top + bottom)
// wrapping the textarea, plus one footer line below the bottom rule. When approval is pending, the
// shared approval-block height derivation applies.
func claudeBottomHeight(a *App, mainW, h int) (int, bool) {
	if a.asking != nil {
		return approvalBottomHeight(a, mainW, h)
	}
	// textarea width: "❯ " prefix (2 cols) leaves mainW-2 for text.
	promptTextW := mainW - 2
	if promptTextW < 4 {
		promptTextW = 4
	}
	inputH := inputVisualHeight(a.input.Value(), promptTextW)
	if maxH := maxi(6, h/3); inputH > maxH {
		inputH = maxH
	}
	if inputH < 1 {
		inputH = 1
	}
	return inputH + 3, false // top rule + bottom rule + footer
}

// approvalBottomHeight derives the approval-block height: one breathing row above the rendered
// approval lines, floored at 3, capped at h-4. Returns (height, true) so callers know the bottom
// block is the approval panel.
func approvalBottomHeight(a *App, mainW, h int) (int, bool) {
	approvalLines := buildApprovalView(a, mainW).lines
	ah := len(approvalLines) + 1
	if ah < 3 {
		ah = 3
	}
	if cap := h - 4; ah > cap {
		ah = cap
	}
	return ah, true
}

// buildMessageLinesClaude assembles styled lines for the Claude viewport. The dot (⏺) leads the
// first line of each assistant turn/tool card; user messages are bare wrapped text.
func buildMessageLinesClaude(a *App, width int) []styledLine {
	contentW := width - 1 // 1-col left margin to keep text off the screen edge
	if contentW < 4 {
		contentW = 4
	}
	var all []styledLine
	for bi := range a.messages {
		all = append(all, renderBlockLinesClaude(&a.messages[bi], a.rt.md, contentW, a.spinnerFrame)...)
		all = append(all, styledLine{}) // blank separator between blocks
	}
	if a.current != nil {
		all = append(all, renderBlockLinesClaude(a.current, a.rt.md, contentW, a.spinnerFrame)...)
	}
	if len(all) == 0 {
		all = renderHomeLines(a, contentW)
	}
	return all
}

// renderBlockLinesClaude dispatches one block to its Claude-style renderer.
func renderBlockLinesClaude(b *msgBlock, md *markdownCache, width int, spinnerFrame int) []styledLine {
	var out []styledLine
	switch b.kind {
	case kindUser:
		out = append(out, userBlockLinesClaude(b.content.String(), width)...)
	case kindAssistant:
		out = append(out, assistantBlockLinesClaude(b, md, width, spinnerFrame)...)
	case kindSystem:
		out = append(out, lineFromRaw("  "+b.content.String(), styleSystem()))
	}
	return out
}

// userBlockLinesClaude renders a user message as bare wrapped text (no bar, no panel fill). A blank
// line separates it from surrounding turns — the affordance is vertical spacing, not a glyph.
func userBlockLinesClaude(content string, width int) []styledLine {
	contentW := width
	if contentW < 4 {
		contentW = 4
	}
	out := []styledLine{styledLine{}} // top spacer
	for _, ln := range wrapPlain(content, contentW) {
		out = append(out, lineFromRaw(ln, styleUser()))
	}
	out = append(out, styledLine{}) // bottom spacer
	return out
}

// assistantBlockLinesClaude renders an assistant turn: an optional thinking line, the body (markdown)
// with a leading ⏺ on its first line, and inline tool cards (each with its own ⏺).
func assistantBlockLinesClaude(b *msgBlock, md *markdownCache, width int, spinnerFrame int) []styledLine {
	var out []styledLine
	if b.thinking.Len() > 0 && b.thinkingMode != thinkingHidden {
		thinking := strings.TrimSpace(b.thinking.String())
		if b.thinkingMode == thinkingCompact && b.rendered == nil {
			if len(thinking) > 1500 {
				thinking = "..." + thinking[len(thinking)-1500:]
			}
		} else if b.thinkingMode == thinkingCompact && b.rendered != nil {
			thinking = truncateStr(thinking, 300)
		}
		out = append(out, lineFromRaw("  "+i18n.T("thinking.prefix")+thinking, styleThinking()))
	}
	if b.toolCallName != "" && len(b.tools) == 0 {
		args := b.toolCallArgs.String()
		dot := blackCircleDot()
		out = append(out, lineFromRaw(dot+" "+fmt.Sprintf(i18n.T("layout.claude.generating_params"), b.toolCallName, len(args)), styleThinking()))
	}
	// Body: render markdown, wrap, then inject the leading dot on the first line.
	var body []styledLine
	if b.rendered != nil {
		body = wrapStyledLines(b.rendered, width)
	} else {
		text := b.content.String()
		if strings.TrimSpace(text) != "" {
			body = wrapStyledLines(parseMarkdown(text), width)
		}
	}
	if len(body) > 0 {
		// Prepend "⏺ " to the first body line; indent continuation lines by 2 to align under the text.
		dotPrefix := blackCircleDot() + " "
		first := body[0]
		var lead styledLine
		lead.appendRun(dotPrefix, styleAssistantDot(b, spinnerFrame))
		lead.runs = append(lead.runs, first.runs...)
		out = append(out, lead)
		for _, ln := range body[1:] {
			var cont styledLine
			cont.appendRun("  ", styleAssistant())
			cont.runs = append(cont.runs, ln.runs...)
			out = append(out, cont)
		}
	} else if b.thinking.Len() == 0 && b.toolCallName == "" && len(b.tools) == 0 {
		sp := spinnerFrameStr(spinnerFrame)
		dot := blackCircleDot()
		out = append(out, lineFromRaw(dot+" "+i18n.T("thinking.streaming", sp), styleThinking()))
	}
	if len(b.tools) > 0 {
		out = append(out, renderInlineToolLinesClaude(b.tools, width, spinnerFrame)...)
	}
	return out
}

// renderInlineToolLinesClaude builds one-line tool status rows: ⏺ (state-colored) + bold name +
// key field + duration, mirroring Claude Code's AssistantToolUseMessage.
func renderInlineToolLinesClaude(cards []toolCard, width int, spinnerFrame int) []styledLine {
	if len(cards) == 0 {
		return nil
	}
	innerW := width - 2
	if innerW < 4 {
		innerW = 4
	}
	dot := blackCircleDot()
	var out []styledLine
	for i := range cards {
		c := &cards[i]
		var dotStyle, nameStyle, dimStyle Style
		var statusLabel string
		switch c.status {
		case toolRunning:
			dotStyle = styleTextMuted() // grey, would blink in a richer impl
			nameStyle = styleToolName()
			dimStyle = styleTextMuted()
			statusLabel = ""
		case toolDone:
			dotStyle = styleToolOK() // green
			nameStyle = styleToolName()
			dimStyle = styleTextMuted()
			statusLabel = durationStr(c.duration)
		case toolError:
			dotStyle = styleToolErr() // red
			nameStyle = styleToolErr()
			dimStyle = styleToolErr()
			statusLabel = i18n.T("tool.failed")
		}
		keyText := extractToolKeyFieldFromInfo(c.name, c.parsed())
		if keyText == "" && c.name != "" {
			keyText = c.name
		}
		suffix := ""
		if statusLabel != "" {
			suffix = i18n.T("tool.status_separator") + statusLabel
		}
		if c.status == toolRunning {
			suffix += " " + spinnerFrameStr(spinnerFrame)
		}
		keyBudget := innerW - strW(dot+" ") - strW(c.name) - strW(suffix) - 1
		var line styledLine
		line.appendRun(dot+" ", dotStyle)
		line.appendRun(c.name, nameStyle)
		if keyText != "" && keyBudget > strW("…") {
			line.appendRun(" "+truncateStrW(keyText, keyBudget), dimStyle)
		}
		if suffix != "" {
			line.appendRun(suffix, dimStyle)
		}
		out = append(out, line)
		if c.errMsg != "" {
			out = append(out, lineFromRaw("  ⎿ "+truncateStr(c.errMsg, innerW-4), styleToolErr()))
		} else if c.result != "" && c.status == toolDone {
			outStr := c.result
			const maxShow = 500
			if len(outStr) > maxShow && !c.expanded {
				out = append(out, lineFromRaw("  ⎿ "+truncateBytesMax(outStr, maxShow)+"…", styleToolDim()))
				out = append(out, lineFromRaw("    [+] "+fmt.Sprintf(i18n.T("tool.expand_hint"), len(outStr)-maxShow), styleToolDim()))
			} else {
				out = append(out, lineFromRaw("  ⎿ "+outStr, styleToolDim()))
				if len(outStr) > maxShow && c.expanded {
					out = append(out, lineFromRaw("    [-] "+i18n.T("tool.collapse_hint"), styleToolDim()))
				}
			}
		}
	}
	return out
}

// styleAssistantDot returns the color for the leading assistant dot. While the turn is streaming
// (no finalized content yet) it stays muted; once content exists it uses the normal text color.
func styleAssistantDot(b *msgBlock, spinnerFrame int) Style {
	if b.content.Len() == 0 && b.rendered == nil {
		return styleTextMuted()
	}
	return styleText()
}

// drawInputBoxClaude paints the Claude-style input: top horizontal rule, "❯" prompt + textarea,
// bottom horizontal rule, footer meta line below the rule.
func drawInputBoxClaude(a *App, x, y, width, blockH int) {
	inputH := blockH - 3 // top rule + bottom rule + footer
	if inputH < 1 {
		inputH = 1
	}
	// Transparent background: like Claude Code, the input box paints only the horizontal rules and
	// the "❯" prompt glyph (foreground color). No backgroundColor fill — the terminal's default
	// background shows through. This keeps any emoji/wide-glyph width divergence from producing a
	// visible drift inside the input box too (see Layout.FillsMainBackground).
	borderCol := pal().promptBorder
	if borderCol == ColorDefault {
		borderCol = pal().borderActive
	}
	loading := a.status != statusIdle || a.asking != nil
	ruleStyle := StyleDefault.Foreground(borderCol)

	// Top horizontal rule: ─...─ across the full width.
	drawHRule(a.screen, x, y, width, ruleStyle)

	// Textarea region: clear with default-background spaces (invisible), no painted bg.
	a.curBg = ColorDefault
	textY := y + 1
	for i := 0; i < inputH; i++ {
		fillRect(a.screen, x, textY+i, width, 1, ' ', StyleDefault)
	}

	// Prompt char "❯" + input text.
	promptStyle := StyleDefault.Foreground(borderCol)
	if loading {
		promptStyle = StyleDefault.Foreground(pal().textMuted)
	}
	promptStr := "❯ "
	drawTextRaw(a.screen, a, x+1, textY, strW(promptStr), promptStr, promptStyle)

	val := a.input.Value()
	textW := width - 1 - strW(promptStr)
	if textW < 4 {
		textW = 4
	}
	lines := wrapPlain(val, textW)
	if len(lines) == 0 {
		lines = []string{""}
	}
	textStyle := styleAssistant()
	if loading {
		textStyle = styleTextMuted()
	}
	for i := 0; i < inputH; i++ {
		ry := textY + i
		if i < len(lines) {
			drawTextRaw(a.screen, a, x+1+strW(promptStr), ry, textW, lines[i], textStyle)
		}
	}

	// Bottom horizontal rule wrapping the textarea.
	bottomY := textY + inputH
	drawHRule(a.screen, x, bottomY, width, ruleStyle)

	// Footer meta line below the bottom rule.
	footerY := bottomY + 1
	fillRect(a.screen, x, footerY, width, 1, ' ', StyleDefault)
	drawPromptFooter(a, x+1, footerY, width-1)

	// Cursor.
	a.input.SetWidth(maxi(10, textW))
	cursorLine, cursorCol := a.input.cursorPos()
	cy := textY + cursorLine
	cx := x + 1 + strW(promptStr) + cursorCol
	if cy >= 0 && cy < a.height && cx >= 0 && cx < a.width {
		a.screen.ShowCursor(cx, cy)
	} else {
		a.screen.HideCursor()
	}
}

// drawHRule paints a full-width horizontal rule (─) in one style, with no corner glyphs. This is the
// new Claude Code input-box shape: two plain rules wrap the textarea instead of a rounded top frame.
func drawHRule(s *Screen, x, y, width int, st Style) {
	if width <= 0 || y < 0 {
		return
	}
	fillRect(s, x, y, width, 1, '─', st)
}

// drawApprovalBlockClaude paints the permission prompt as a bottom block: a top horizontal rule
// with a warning tint, the approval lines below it. Transparent background (rule-only), matching
// the input box and the Claude Code convention.
func drawApprovalBlockClaude(a *App, x, y, width, height int, view approvalView) {
	borderCol := pal().warning
	ruleStyle := StyleDefault.Foreground(borderCol)
	drawHRule(a.screen, x, y, width, ruleStyle)
	a.curBg = ColorDefault
	// Clear the approval body across the full width. The screen is front/back-diffed and the back
	// buffer is not reset per frame, so any cell not rewritten this frame keeps its previous glyph:
	// without clearing, the prompt's footer ("Tab … ^P …") and other stale text bleed through
	// wherever an approval line is shorter than the width, garbling the file path and options.
	if height > 1 {
		fillRect(a.screen, x, y+1, width, height-1, ' ', StyleDefault)
	}
	drawApprovalLinesAnchored(a, x, y, width, height, 1, StyleDefault, view)
	a.screen.HideCursor()
}
