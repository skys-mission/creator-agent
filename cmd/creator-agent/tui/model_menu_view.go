package tui

// Rendering for the /model panel (second-level menu, model list, delete confirm). Mirrors the
// create-form dialog: a centered dashed-border box on a dimmed full-screen background, drawn
// through the Surface primitive so every draw fills its own rect (rendering invariant 1).

import (
	"fmt"

	"github.com/skys-mission/creator-agent/cmd/creator-agent/tui/i18n"
	"github.com/skys-mission/creator-agent/contract"
)

// drawModelMenu paints the /model panel and flushes the frame.
func drawModelMenu(a *App) {
	surf := newSurface(a.screen)
	surf.FillFrame(' ', pal().frameBg)

	w, h := a.screen.Size()
	m := &a.modelMenu

	boxW := mini(w-4, 64)
	boxH := modelMenuBoxH(m, h)
	if boxW < 34 || boxH < 9 {
		surf.DrawPlain(0, 0, i18n.T("model_form.too_small"), stylePaletteItem())
		_ = a.screen.Sync()
		return
	}
	boxX := (w - boxW) / 2
	boxY := (h - boxH) / 2

	box := surf.Sub(Rect{boxX, boxY, boxW, boxH}).WithBg(pal().panelBg)
	box.FillRectLocal(Rect{0, 0, boxW, boxH}, ' ', StyleDefault)
	drawDashedBorder(box, boxW, boxH)

	innerX := 2
	innerW := boxW - 4
	a.screen.HideCursor()

	switch m.view {
	case modelMenuRoot:
		drawModelMenuRoot(a, box, innerX, innerW, boxH)
	case modelMenuManage:
		drawModelMenuManage(a, box, innerX, innerW, boxH)
	case modelMenuConfirm:
		drawModelMenuConfirm(a, box, innerX, innerW, boxH)
	}
	_ = a.screen.Sync()
}

// modelMenuBoxH returns the panel height for the current view: the root and confirm screens have
// a fixed shape; the manage list grows with the collection (capped to the screen).
func modelMenuBoxH(m *modelMenuState, h int) int {
	switch m.view {
	case modelMenuManage:
		return mini(h-2, maxi(9, len(m.list)+6))
	default:
		return 9
	}
}

// drawModelMenuRoot draws the second-level menu: the two items and the footer.
func drawModelMenuRoot(a *App, box *Surface, innerX, innerW, boxH int) {
	m := &a.modelMenu
	box.DrawPlain(innerX, 1, i18n.T("model_menu.title"), stylePaletteAccent())
	items := []string{i18n.T("model_menu.item.new"), i18n.T("model_menu.item.manage")}
	for i, label := range items {
		marker := "  "
		st := stylePaletteItem()
		if i == m.sel {
			marker = "> "
			st = stylePaletteAccent()
		}
		box.DrawPlain(innerX, 3+i, truncatePlain(marker+label, innerW), st)
	}
	drawModelMenuErr(a, box, innerX, innerW, boxH)
	box.DrawPlain(innerX, boxH-2, truncatePlain(i18n.T("model_menu.footer.menu"), innerW), styleToolDim())
}

// drawModelMenuManage draws the model list. Rows show name / protocol / model ID only — never a
// key. The selected row is marked "> " in the accent style; the window scrolls to keep the
// selection visible when the collection outgrows the box.
func drawModelMenuManage(a *App, box *Surface, innerX, innerW, boxH int) {
	m := &a.modelMenu
	box.DrawPlain(innerX, 1, i18n.T("model_menu.manage.title"), stylePaletteAccent())

	if len(m.list) == 0 {
		box.DrawPlain(innerX, 3, truncatePlain(i18n.T("model_menu.manage.empty"), innerW), styleToolDim())
		drawModelMenuErr(a, box, innerX, innerW, boxH)
		box.DrawPlain(innerX, boxH-2, truncatePlain(i18n.T("model_menu.manage.footer"), innerW), styleToolDim())
		return
	}

	visRows := boxH - 6 // rows 3..boxH-4 are the list window (err at boxH-3)
	if visRows < 1 {
		visRows = 1
	}
	top := 0
	if m.sel >= visRows {
		top = m.sel - visRows + 1
	}
	if top > len(m.list)-visRows {
		top = maxi(0, len(m.list)-visRows)
	}
	for i := 0; i < visRows; i++ {
		idx := top + i
		if idx >= len(m.list) {
			break
		}
		marker := "  "
		st := stylePaletteItem()
		if idx == m.sel {
			marker = "> "
			st = stylePaletteAccent()
		}
		box.DrawPlain(innerX, 3+i, truncatePlain(marker+modelMenuRowText(m.list[idx]), innerW), st)
	}
	drawModelMenuErr(a, box, innerX, innerW, boxH)
	box.DrawPlain(innerX, boxH-2, truncatePlain(i18n.T("model_menu.manage.footer"), innerW), styleToolDim())
}

// modelMenuRowText renders one list row: name, protocol, model ID (no secrets).
func modelMenuRowText(m contract.Model) string {
	name := orDash(m.DisplayName())
	return fmt.Sprintf("%-16s %-12s %s", truncatePlain(name, 16), m.Protocol, m.ModelID)
}

// drawModelMenuConfirm draws the delete confirmation: what will be deleted and the two exits.
func drawModelMenuConfirm(a *App, box *Surface, innerX, innerW, boxH int) {
	m := &a.modelMenu
	box.DrawPlain(innerX, 1, i18n.T("model_menu.manage.title"), stylePaletteAccent())
	prompt := fmt.Sprintf(i18n.T("model_menu.confirm.prompt"), m.list[m.sel].DisplayName())
	box.DrawPlain(innerX, 3, truncatePlain(prompt, innerW), styleError())
	drawModelMenuErr(a, box, innerX, innerW, boxH)
	box.DrawPlain(innerX, boxH-2, truncatePlain(i18n.T("model_menu.confirm.footer"), innerW), styleToolDim())
}

// drawModelMenuErr draws the inline status line above the footer: the error if any, else the
// success flash (styled dim, not red), else blank. The rect is filled every frame (invariant 1).
func drawModelMenuErr(a *App, box *Surface, innerX, innerW, boxH int) {
	m := &a.modelMenu
	text, st := m.flash, styleTextMuted()
	if m.err != "" {
		text, st = m.err, styleError()
	}
	box.DrawPlain(innerX, boxH-3, truncatePlain(text, innerW), st)
}
