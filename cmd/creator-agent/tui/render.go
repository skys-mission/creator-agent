package tui

import (
	"strings"

	"github.com/clipperhouse/uax29/v2/graphemes"
	"github.com/skys-mission/creator-agent/cmd/creator-agent/tui/i18n"
)

// Minimal rebuild shell rendering: the bare page (notice line + input box) and the /model-new
// overlay. Rendering invariant 1 (docs/tui.md §4) applies to every draw call: the back buffer is
// never reset per frame, so a draw must fully fill its own rectangle — drawMain clears the whole
// screen each frame so nothing can residue from a previous frame or overlay.

// render draws one frame. The /model-new dialog is a full-screen overlay that draws and flushes
// itself; otherwise the bare page is drawn and flushed (Sync on transitions, Show otherwise).
func render(a *App) {
	if a.modelForm.open {
		drawModelForm(a)
		a.forceRender = false
		return
	}
	if a.modelMenu.open {
		drawModelMenu(a)
		a.forceRender = false
		return
	}
	drawMain(a)
	if a.forceRender {
		_ = a.screen.Sync()
		a.forceRender = false
		return
	}
	_ = a.screen.Show()
}

// clearScreen repaints the whole surface with default-styled spaces.
func clearScreen(a *App) {
	w, h := a.screen.Size()
	fillRect(a.screen, 0, 0, w, h, ' ', StyleDefault)
}

// drawMain paints the bare page: the notice line just above the input box, then the input box.
func drawMain(a *App) {
	w, h := a.screen.Size()
	if w < 1 || h < 1 {
		return
	}
	clearScreen(a)
	a.curBg = ColorDefault

	const padX = 2
	x0 := padX
	width := w - 2*padX
	if width < 10 {
		x0 = 0
		width = w
	}

	promptTextW := width - 2
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
	blockH := inputH + 3 // top rule + bottom rule + footer
	blockY := h - blockH
	if blockY < 0 {
		blockY = 0
	}

	// Command-hint menu: candidate rows directly above the input box with one blank gap row
	// between. The screen is already cleared, so every row is fully filled (invariant 1).
	menuRows := completionRows(a, width)
	if len(menuRows) > blockY-1 {
		menuRows = menuRows[:maxi(0, blockY-1)]
	}
	menuTop := blockY - len(menuRows) - 1
	if len(menuRows) > 0 {
		for i, row := range menuRows {
			st := styleCompItem()
			switch {
			case row.selected:
				st = styleCompSelected()
			case row.muted:
				st = styleTextMuted()
			}
			drawTextRaw(a.screen, a, x0, menuTop+i, width, row.text, st)
		}
	}

	if noticeY := menuTop - 1; noticeY >= 0 && a.notice != "" {
		drawTextRaw(a.screen, a, x0, noticeY, width, truncatePlain(a.notice, width), styleTextMuted())
	}
	drawInputBox(a, x0, blockY, width, blockH)
}

// drawInputBox paints the Claude-style input: top rule, "❯" prompt + wrapped textarea, bottom
// rule, footer hint. Transparent background (rules and glyphs only) — the terminal's default
// background shows through, which keeps wide-glyph width divergence from drifting the box.
func drawInputBox(a *App, x, y, width, blockH int) {
	inputH := blockH - 3
	if inputH < 1 {
		inputH = 1
	}
	borderCol := pal().promptBorder
	if borderCol == ColorDefault {
		borderCol = pal().borderActive
	}
	ruleStyle := StyleDefault.Foreground(borderCol)

	drawHRule(a.screen, x, y, width, ruleStyle)

	a.curBg = ColorDefault
	textY := y + 1
	for i := 0; i < inputH; i++ {
		fillRect(a.screen, x, textY+i, width, 1, ' ', StyleDefault)
	}

	promptStr := "❯ "
	drawTextRaw(a.screen, a, x+1, textY, strW(promptStr), promptStr, ruleStyle)

	val := a.input.Value()
	textW := width - 1 - strW(promptStr)
	if textW < 4 {
		textW = 4
	}
	lines := wrapPlain(val, textW)
	if len(lines) == 0 {
		lines = []string{""}
	}
	for i := 0; i < inputH; i++ {
		if i < len(lines) {
			drawTextRaw(a.screen, a, x+1+strW(promptStr), textY+i, textW, lines[i], styleAssistant())
		}
	}

	bottomY := textY + inputH
	drawHRule(a.screen, x, bottomY, width, ruleStyle)

	footerY := bottomY + 1
	fillRect(a.screen, x, footerY, width, 1, ' ', StyleDefault)
	drawTextRaw(a.screen, a, x+1, footerY, width-1, truncatePlain(i18n.T("footer.hint"), width-1), styleTextMuted())

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

// drawHRule paints a full-width horizontal rule (─) in one style, with no corner glyphs.
func drawHRule(s *Screen, x, y, width int, st Style) {
	if width <= 0 || y < 0 {
		return
	}
	fillRect(s, x, y, width, 1, '─', st)
}

// drawTextRaw writes a plain string at (x,y) within width columns with one style. Routed through
// a transient Surface band so a wide (CJK/emoji) rune that does not fully fit before x+width is
// dropped instead of spilling its follow cell into a neighbor — the fix for the persistent
// border-misalignment defect. Composites the region background under styles with no explicit bg.
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

// wrapPlain is a cheap word-wrap for plain text (grapheme-cluster-aware, CJK/emoji width 2).
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

// inputVisualHeight returns the number of visual rows the input text occupies at the given wrap
// width (each logical line counts for how many rows it wraps to).
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
