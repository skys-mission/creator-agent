package tui

import (
	"strings"

	"github.com/skys-mission/creator-agent/cmd/creator-agent/tui/i18n"
)

// wizard rendering. Drawing goes through the package's Surface primitive (not the App-bound
// drawTextRaw/fillRect helpers) so the wizard stays decoupled from the agent App. The visual style
// mirrors the command picker: a centered, single-source box on a dimmed full-screen background.

// render paints the current wizard frame and flushes it.
func (wz *wizard) render() {
	surf := newSurface(wz.screen)
	surf.FillFrame(' ', pal().frameBg)

	boxW := mini(wz.w-4, 76)
	boxH := mini(wz.h-2, 20)
	if boxW < 24 || boxH < 10 {
		// Terminal too small to draw the framed wizard; show a minimal hint and bail on layout.
		surf.DrawPlain(0, 0, i18n.T("wizard.too_small"), stylePaletteItem())
		_ = wz.screen.Sync()
		return
	}
	boxX := (wz.w - boxW) / 2
	boxY := (wz.h - boxH) / 2

	box := surf.Sub(Rect{boxX, boxY, boxW, boxH}).WithBg(pal().panelBg)
	box.FillRectLocal(Rect{0, 0, boxW, boxH}, ' ', StyleDefault)
	wz.drawBorder(box, boxW, boxH)

	innerX := 2
	innerW := boxW - 4
	if innerW < 4 {
		innerW = 4
	}

	box.DrawPlain(innerX, 1, i18n.T("wizard.title"), stylePaletteAccent())
	box.DrawPlain(innerX, 2, wz.stepIndicator(), styleToolDim())

	wz.screen.HideCursor()
	switch wz.step {
	case stepProvider:
		wz.drawProvider(box, innerX, innerW)
	case stepBaseURL:
		wz.drawField(box, boxX, boxY, innerX, innerW, i18n.T("wizard.baseurl.prompt"), i18n.T("wizard.baseurl.hint"), &wz.baseURL, false)
	case stepAPIKey:
		wz.drawField(box, boxX, boxY, innerX, innerW, i18n.T("wizard.apikey.prompt"), i18n.T("wizard.apikey.hint"), &wz.apiKey, true)
	case stepModel:
		wz.drawField(box, boxX, boxY, innerX, innerW, i18n.T("wizard.model.prompt"), i18n.T("wizard.model.hint"), &wz.model, false)
	case stepConfirm:
		wz.drawConfirm(box, innerX, innerW)
	}

	box.DrawPlain(innerX, boxH-2, wz.footer(), styleToolDim())
	_ = wz.screen.Sync()
}

// drawBorder draws the picker-style dashed border around the box.
func (wz *wizard) drawBorder(box *Surface, boxW, boxH int) {
	border := styleToolDim()
	box.DrawPlain(0, 0, strings.Repeat("-", boxW), border)
	box.DrawPlain(0, boxH-1, strings.Repeat("-", boxW), border)
	for row := 1; row < boxH-1; row++ {
		box.DrawPlain(0, row, "|", border)
		box.DrawPlain(boxW-1, row, "|", border)
	}
}

// stepIndicator renders a short "step N/total" label.
func (wz *wizard) stepIndicator() string {
	n := int(wz.step) + 1
	// stepBaseURL only exists for custom providers; collapse the count so the label stays honest.
	total := 4
	if wz.presets[wz.selIdx].custom {
		total = 5
	} else if wz.step > stepProvider {
		// Non-custom flow skips the base-URL step; renumber so it reads 2/4, 3/4, 4/4.
		n = int(wz.step)
	}
	return i18n.T("wizard.step", n, total)
}

func (wz *wizard) drawProvider(box *Surface, innerX, innerW int) {
	box.DrawPlain(innerX, 4, i18n.T("wizard.provider.prompt"), stylePaletteItem())
	for i, p := range wz.presets {
		row := 6 + i
		marker := "  "
		st := stylePaletteItem()
		if i == wz.selIdx {
			marker = "> "
			st = stylePaletteAccent()
		}
		label := p.label
		if !p.custom {
			label = p.label + "  (" + p.baseURL + ")"
		}
		box.DrawPlain(innerX, row, truncatePlain(marker+label, innerW), st)
	}
}

// drawField draws a single text-entry step. When masked, the value is rendered as bullets but the
// cursor still tracks the true rune count.
func (wz *wizard) drawField(box *Surface, boxX, boxY, innerX, innerW int, prompt, hint string, buf *inputBuffer, masked bool) {
	box.DrawPlain(innerX, 4, prompt, stylePaletteItem())

	fieldRow := 6
	box.DrawPlain(innerX, fieldRow, "> ", stylePaletteAccent())
	valX := innerX + 2
	valW := innerW - 2

	value := buf.Value()
	display := value
	if masked {
		display = strings.Repeat("•", len([]rune(value)))
	}
	if display == "" {
		box.DrawPlain(valX, fieldRow, truncatePlain(hint, valW), styleToolDim())
	} else {
		box.DrawPlain(valX, fieldRow, truncatePlain(display, valW), stylePaletteItem())
	}

	// Place the hardware cursor after the (possibly masked) text, clamped to the field width.
	curCols := strW(display)
	if curCols > valW {
		curCols = valW
	}
	wz.screen.ShowCursor(boxX+valX+curCols, boxY+fieldRow)
}

func (wz *wizard) drawConfirm(box *Surface, innerX, innerW int) {
	preset := wz.presets[wz.selIdx]
	box.DrawPlain(innerX, 4, i18n.T("wizard.confirm.prompt"), stylePaletteItem())

	rows := []struct{ label, value string }{
		{i18n.T("wizard.confirm.provider"), preset.label},
		{i18n.T("wizard.confirm.baseurl"), strings.TrimSpace(wz.baseURL.Value())},
		{i18n.T("wizard.confirm.apikey"), maskKey(strings.TrimSpace(wz.apiKey.Value()))},
		{i18n.T("wizard.confirm.model"), strings.TrimSpace(wz.model.Value())},
	}
	for i, r := range rows {
		line := r.label + ": " + r.value
		box.DrawPlain(innerX, 6+i, truncatePlain(line, innerW), stylePaletteItem())
	}
}

// footer returns the contextual key hints for the current step.
func (wz *wizard) footer() string {
	switch wz.step {
	case stepProvider:
		return i18n.T("wizard.footer.provider")
	case stepConfirm:
		return i18n.T("wizard.footer.confirm")
	default:
		return i18n.T("wizard.footer.field")
	}
}

// maskKey shows only the last 4 characters of an API key (or bullets when short), so the confirm
// screen reveals enough to recognize the key without printing the secret in full.
func maskKey(k string) string {
	r := []rune(k)
	if len(r) <= 4 {
		return strings.Repeat("•", len(r))
	}
	return strings.Repeat("•", len(r)-4) + string(r[len(r)-4:])
}

// truncatePlain clips s to a maximum display width, appending an ellipsis when it overflows.
func truncatePlain(s string, maxWidth int) string {
	if maxWidth <= 0 {
		return ""
	}
	if strW(s) <= maxWidth {
		return s
	}
	if maxWidth == 1 {
		return "…"
	}
	var b strings.Builder
	used := 0
	for _, ch := range s {
		w := runeW(ch)
		if w <= 0 {
			continue
		}
		if used+w > maxWidth-1 {
			break
		}
		b.WriteRune(ch)
		used += w
	}
	b.WriteRune('…')
	return b.String()
}
