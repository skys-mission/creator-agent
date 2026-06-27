package tui

import (
	"strings"
)

// picker holds the common state for a fuzzy overlay picker. Concrete picker state structs embed
// picker[T] so their existing field access (e.g. a.palette.entries) keeps working through promotion.
type picker[T any] struct {
	open    bool
	query   inputBuffer
	entries []T
	selIdx  int
	scrollY int
}

// pickerRenderer supplies the per-picker bits that generic draw cannot know: title, prompts,
// footer, query prompt, per-row rendering, and cursor placement.
type pickerRenderer[T any] interface {
	title() string
	placeholder() string
	footer(a *App) string
	queryPrompt(a *App) (string, int)
	drawItem(a *App, item T, selected bool, innerX, ry, innerW int)
	cursor(a *App, innerX, queryY, promptWidth int) (x, y int, visible bool)
}

func (p *picker[T]) initPicker(width int) {
	p.open = true
	p.query.SetValue("")
	p.query.SetWidth(maxi(10, width-10))
	p.selIdx = 0
	p.scrollY = 0
}

func (p *picker[T]) close(a *App) {
	p.open = false
	a.forceRender = true
}

// isOpen reports whether this picker overlay is currently shown. Promoted to every embedding picker
// state so each satisfies pickerController.
func (p *picker[T]) isOpen() bool { return p.open }

// pickerController is the uniform handle the event loop (handleKey) and renderer (render) use to
// drive whichever picker overlay is currently open. It replaces the two parallel per-picker dispatch
// switches that previously had to be hand-kept in sync — exactly the drift this file's generic
// picker[T] was introduced to eliminate.
type pickerController interface {
	isOpen() bool
	onKey(a *App, k Key, ch rune)
	render(a *App)
}

// pickerControllers lists every full-screen picker overlay in dispatch/draw order. This is the single
// source of truth for the picker set: register new pickers here only (the keys.go and render.go
// dispatch derive from it). The command palette is intentionally excluded — it is handled separately
// ahead of these in both dispatch sites.
func (a *App) pickerControllers() []pickerController {
	return []pickerController{
		&a.sessionPicker, &a.modelPicker, &a.mcpPicker, &a.agentsPicker,
		&a.variantPicker, &a.modePicker, &a.themePicker,
	}
}

// activePicker returns the currently open picker controller, or nil when none is open. At most one is
// open at a time (enforced by closeAllOtherPickers).
func (a *App) activePicker() pickerController {
	for _, p := range a.pickerControllers() {
		if p.isOpen() {
			return p
		}
	}
	return nil
}

// openFlags returns the addresses of every picker's `open` flag in the App, so one helper can close
// all of them. New pickers must be added here so the mutual-exclusion contract stays complete.
func openFlags(a *App) []*bool {
	return []*bool{
		&a.palette.open,
		&a.sessionPicker.open,
		&a.modelPicker.open,
		&a.mcpPicker.open,
		&a.agentsPicker.open,
		&a.variantPicker.open,
		&a.modePicker.open,
		&a.themePicker.open,
	}
}

// closeAllOtherPickers clears the `open` flag of every picker except `except` (which may be nil to
// close all). Callers pass &a.<thisPicker>.open. Sets a.forceRender so the next render reflects the
// closed overlays. Replaces the per-picker hand-maintained exclusion lists, which had drifted out of
// sync (some pickers closed 2 others, some closed 6).
func closeAllOtherPickers(a *App, except *bool) {
	changed := false
	for _, f := range openFlags(a) {
		if f == except {
			continue
		}
		if *f {
			*f = false
			changed = true
		}
	}
	if changed {
		a.forceRender = true
	}
}

func (p *picker[T]) ensureVisible(a *App) {
	p.scrollY = clampScroll(p.scrollY, p.selIdx, paletteListHeight(a))
}

func (p *picker[T]) refilter(a *App, load func() []T, filter func([]T, string) []T) {
	if load == nil {
		p.entries = nil
		return
	}
	all := load()
	if filter != nil {
		p.entries = filter(all, p.query.Value())
	} else {
		p.entries = all
	}
	if p.selIdx >= len(p.entries) {
		p.selIdx = maxi(0, len(p.entries)-1)
	}
	p.ensureVisible(a)
}

func (p *picker[T]) handleKey(a *App, k Key, ch rune, commit func(), refilter func()) bool {
	switch k {
	case KeyUp:
		if p.selIdx > 0 {
			p.selIdx--
		}
		p.ensureVisible(a)
		return true
	case KeyDown:
		if p.selIdx < len(p.entries)-1 {
			p.selIdx++
		}
		p.ensureVisible(a)
		return true
	case KeyPgUp:
		h := paletteListHeight(a)
		p.selIdx = maxi(0, p.selIdx-h)
		p.ensureVisible(a)
		return true
	case KeyPgDn:
		h := paletteListHeight(a)
		p.selIdx = maxi(0, mini(len(p.entries)-1, p.selIdx+h))
		p.ensureVisible(a)
		return true
	case KeyHome:
		p.selIdx = 0
		p.scrollY = 0
		return true
	case KeyEnd:
		p.selIdx = maxi(0, len(p.entries)-1)
		p.ensureVisible(a)
		return true
	case KeyEnter, KeyTab:
		if commit != nil {
			commit()
		}
		return true
	case KeyBackspace, KeyBackspace2:
		p.query.Backspace()
		if refilter != nil {
			refilter()
		}
		return true
	case KeyDelete:
		p.query.Delete()
		if refilter != nil {
			refilter()
		}
		return true
	case KeyLeft:
		p.query.CursorLeft()
		return true
	case KeyRight:
		p.query.CursorRight()
		return true
	case KeyCtrlU:
		p.query.SetValue("")
		if refilter != nil {
			refilter()
		}
		return true
	}
	if k == KeyRune {
		p.query.InsertRune(ch)
		if refilter != nil {
			refilter()
		}
		return true
	}
	return false
}

// handleKeyOrClose handles the standard picker close keys (Esc / Ctrl+P / Ctrl+C) via closeFn, and
// otherwise delegates to handleKey. It returns whether the key was consumed, so callers that need to
// react to navigation (e.g. live-preview pickers) can chain on the result. Pickers with non-standard
// close keys or extra actions (mode, session, mcp) keep their own onKey instead.
func (p *picker[T]) handleKeyOrClose(a *App, k Key, ch rune, closeFn, commit, refilter func()) bool {
	switch k {
	case KeyEsc, KeyCtrlP, KeyCtrlC:
		if closeFn != nil {
			closeFn()
		}
		return true
	}
	return p.handleKey(a, k, ch, commit, refilter)
}

func (p *picker[T]) draw(a *App, r pickerRenderer[T]) {
	w, h := a.screen.Size()
	if w < 20 || h < 8 {
		return
	}

	boxW := paletteBoxWidth(a)
	boxH := paletteBoxHeight(a)
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

	drawTextRaw(a.screen, a, innerX, boxY+1, innerW, r.title(), stylePaletteAccent())

	queryY := boxY + 3
	prompt, promptWidth := r.queryPrompt(a)
	if prompt != "" {
		drawTextRaw(a.screen, a, innerX, queryY, promptWidth, prompt, stylePaletteAccent())
	}
	qText := p.query.Value()
	drawTextRaw(a.screen, a, innerX+promptWidth, queryY, innerW-promptWidth, qText, stylePaletteItem())
	if qText == "" {
		drawTextRaw(a.screen, a, innerX+promptWidth, queryY, innerW-promptWidth, r.placeholder(), styleToolDim())
	}

	listTop := boxY + 5
	listH := boxH - 7
	if listH < 1 {
		listH = 1
	}
	if p.scrollY < 0 {
		p.scrollY = 0
	}
	p.scrollY = clampScroll(p.scrollY, p.selIdx, listH)

	for i := 0; i < listH; i++ {
		idx := p.scrollY + i
		ry := listTop + i
		if idx < 0 || idx >= len(p.entries) {
			fillRect(a.screen, innerX, ry, innerW, 1, ' ', StyleDefault.Background(pal().panelBg))
			continue
		}
		selected := idx == p.selIdx
		rowBg := pal().panelBg
		if selected {
			rowBg = pal().frameBg
		}
		fillRect(a.screen, innerX, ry, innerW, 1, ' ', StyleDefault.Background(rowBg))
		a.curBg = rowBg
		r.drawItem(a, p.entries[idx], selected, innerX, ry, innerW)
	}
	a.curBg = pal().panelBg

	footerY := boxY + boxH - 2
	drawTextRaw(a.screen, a, innerX, footerY, innerW, r.footer(a), styleToolDim())

	cx, cy, visible := r.cursor(a, innerX, queryY, promptWidth)
	if !visible || cx < 0 || cx >= w || cy < 0 || cy >= h {
		a.screen.HideCursor()
	} else {
		a.screen.ShowCursor(cx, cy)
	}

	a.curBg = savedBg
}

func pickerQueryCursor(buf *inputBuffer, innerX, queryY, promptWidth int) (int, int, bool) {
	_, qc := buf.cursorPos()
	return innerX + promptWidth + qc, queryY, true
}

func isPickerNavKey(k Key) bool {
	switch k {
	case KeyUp, KeyDown, KeyPgUp, KeyPgDn, KeyHome, KeyEnd:
		return true
	}
	return false
}
