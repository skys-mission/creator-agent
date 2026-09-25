package tui

import (
	"strings"

	"github.com/skys-mission/creator-agent/cmd/creator-agent/tui/i18n"
	"github.com/skys-mission/creator-agent/contract"
)

// Rendering for the create-model dialog. Drawing goes through the Surface primitive (like the
// wizard) so the dialog is self-contained: it fills its own rect, the overlay chain handles the
// rest. Style mirrors the wizard: a centered dashed-border box on a dimmed full-screen background.

// drawModelForm paints the create-model dialog (edit page or confirm page) and flushes the frame.
func drawModelForm(a *App) {
	surf := newSurface(a.screen)
	surf.FillFrame(' ', pal().frameBg)

	w, h := a.screen.Size()
	boxW := mini(w-4, 72)
	// The row list is dynamic (kind-dependent), so the box grows with it. Layout needs
	// rows + 6: title/blank above, err + footer + border below.
	needed := len(formRows(&a.modelForm)) + 6
	boxH := mini(h-2, needed)
	if boxW < 34 || boxH < needed {
		// Too small to lay out the field rows; show a hint and bail on layout.
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

	box.DrawPlain(innerX, 1, i18n.T("model_form.title"), stylePaletteAccent())
	a.screen.HideCursor()

	f := &a.modelForm
	if f.confirm {
		drawModelFormConfirm(a, box, innerX, innerW)
	} else {
		drawModelFormFields(a, box, boxX, boxY, innerX, innerW)
	}

	if f.err != "" {
		box.DrawPlain(innerX, boxH-3, truncatePlain(f.err, innerW), styleError())
	}
	footer := i18n.T("model_form.footer.form")
	if f.confirm {
		footer = i18n.T("model_form.footer.confirm")
	}
	box.DrawPlain(innerX, boxH-2, truncatePlain(footer, innerW), styleToolDim())
	_ = a.screen.Sync()
}

// drawModelFormFields draws the field rows of the edit page (the row list depends on the
// reasoning kind, see formRows). The focused row is marked with "> " and drawn in the accent
// style; its text field also gets the hardware cursor.
func drawModelFormFields(a *App, box *Surface, boxX, boxY, innerX, innerW int) {
	f := &a.modelForm
	rows := formRows(f)
	labelW := 0
	for _, k := range rows {
		labelW = maxi(labelW, strW(formLabel(k)))
	}
	valX := innerX + 2 + labelW + 2
	valW := innerW - (valX - innerX)

	for i, k := range rows {
		row := 3 + i
		focused := k == f.focus
		marker := "  "
		st := stylePaletteItem()
		if focused {
			marker = "> "
			st = stylePaletteAccent()
		}
		box.DrawPlain(innerX, row, marker+padRight(formLabel(k), labelW), st)

		text, isHint, _ := formDisplay(f, k)
		valStyle := st
		if isHint {
			valStyle = styleToolDim()
		}
		box.DrawPlain(valX, row, truncatePlain(text, valW), valStyle)

		if focused && k.isText() {
			curCols := formCursorCols(f, k)
			if curCols > valW {
				curCols = valW
			}
			a.screen.ShowCursor(boxX+valX+curCols, boxY+row)
		}
	}
}

// drawModelFormConfirm draws the review page: one label: value row per field, with the API key
// masked (maskKey reveals only the last 4 runes) and the reasoning declaration summarized.
func drawModelFormConfirm(a *App, box *Surface, innerX, innerW int) {
	m := a.modelForm.build()
	keyVal := orDash("")
	if m.APIKey != "" {
		keyVal = maskKey(m.APIKey)
	}
	rows := []struct{ label, value string }{
		{formLabel(formFieldName), orDash(m.Name)},
		{formLabel(formFieldProtocol), string(m.Protocol)},
		{formLabel(formFieldBaseURL), orDash(m.BaseURL)},
		{formLabel(formFieldModelID), m.ModelID},
		{formLabel(formFieldAPIKey), keyVal},
		{formLabel(formFieldThinkingEcho), echoValueLabel(m.Params.ThinkingEcho)},
		{formLabel(formFieldReasoningKey), orDash(m.Params.ReasoningKey)},
	}
	rows = append(rows, reasoningSummaryRows(m.Params.Reasoning)...)
	box.DrawPlain(innerX, 2, i18n.T("model_form.confirm.prompt"), styleToolDim())
	for i, r := range rows {
		line := r.label + ": " + r.value
		box.DrawPlain(innerX, 3+i, truncatePlain(line, innerW), stylePaletteItem())
	}
}

// reasoningSummaryRows turns the reasoning declaration into review rows (kind always; the
// supported set + default for effort; the default switch for toggle).
func reasoningSummaryRows(r contract.Reasoning) []struct{ label, value string } {
	rows := []struct{ label, value string }{
		{formLabel(formFieldReasoningKind), kindValueLabel(r.Kind)},
	}
	switch r.Kind {
	case contract.ReasoningKindToggle:
		rows = append(rows, struct{ label, value string }{
			formLabel(formFieldReasoningToggle), toggleValueLabel(r.Default),
		})
		rows = append(rows, struct{ label, value string }{
			formLabel(formFieldToggleDialect), dialectValueLabel(r.ToggleDialect),
		})
	case contract.ReasoningKindEffort:
		rows = append(rows, struct{ label, value string }{
			i18n.T("model_form.field.reasoning_supported"), strings.Join(r.Efforts, ", "),
		})
		rows = append(rows, struct{ label, value string }{
			formLabel(formFieldReasoningDefault), orDash(r.Default),
		})
	}
	return rows
}

// formLabel returns the localized label of a form row.
func formLabel(k modelFormField) string {
	switch k {
	case formFieldName:
		return i18n.T("model_form.field.name")
	case formFieldProtocol:
		return i18n.T("model_form.field.protocol")
	case formFieldBaseURL:
		return i18n.T("model_form.field.base_url")
	case formFieldModelID:
		return i18n.T("model_form.field.model_id")
	case formFieldAPIKey:
		return i18n.T("model_form.field.api_key")
	case formFieldThinkingEcho:
		return i18n.T("model_form.field.thinking_echo")
	case formFieldReasoningKey:
		return i18n.T("model_form.field.reasoning_key")
	case formFieldReasoningKind:
		return i18n.T("model_form.field.reasoning_kind")
	case formFieldReasoningToggle:
		return i18n.T("model_form.field.reasoning_toggle")
	case formFieldToggleDialect:
		return i18n.T("model_form.field.toggle_dialect")
	case formFieldReasoningDefault:
		return i18n.T("model_form.field.reasoning_default")
	}
	if i, ok := k.effortIdx(); ok {
		return contract.ReasoningEfforts[i]
	}
	return ""
}

// formHint returns the localized placeholder hint of a text row (empty for choice rows).
func formHint(k modelFormField) string {
	switch k {
	case formFieldName:
		return i18n.T("model_form.hint.name")
	case formFieldBaseURL:
		return i18n.T("model_form.hint.base_url")
	case formFieldModelID:
		return i18n.T("model_form.hint.model_id")
	case formFieldAPIKey:
		return i18n.T("model_form.hint.api_key")
	case formFieldReasoningKey:
		return i18n.T("model_form.hint.reasoning_key")
	}
	return ""
}

// formDisplay returns the rendered text of a row: the current value (bullets for the masked API
// key), the hint when a text field is empty, or the current label of a choice field. isHint marks
// placeholder text (drawn dim).
func formDisplay(f *modelFormState, k modelFormField) (text string, isHint bool, masked bool) {
	switch k {
	case formFieldProtocol:
		return string(protocolChoices()[f.protocolIdx]), false, false
	case formFieldThinkingEcho:
		return echoValueLabel(echoChoices()[f.echoIdx]), false, false
	case formFieldReasoningKind:
		return kindValueLabel(f.kind()), false, false
	case formFieldReasoningToggle:
		return toggleValueLabel(toggleChoices()[f.toggleIdx]), false, false
	case formFieldToggleDialect:
		return dialectValueLabel(contract.ReasoningToggleDialects[f.toggleDialectIdx]), false, false
	case formFieldReasoningDefault:
		if f.effortDefaultIdx < 0 {
			return orDash(""), false, false
		}
		return contract.ReasoningEfforts[f.effortDefaultIdx], false, false
	}
	if i, ok := k.effortIdx(); ok {
		if f.effortsOn[i] {
			return "[x]", false, false
		}
		return "[ ]", false, false
	}
	val := f.textBuf(k).Value()
	if val == "" {
		return formHint(k), true, false
	}
	if k == formFieldAPIKey {
		return strings.Repeat("•", len([]rune(val))), false, true
	}
	return val, false, false
}

// echoValueLabel localizes a thinking-echo mode.
func echoValueLabel(m contract.ThinkingEchoMode) string {
	if m == contract.ThinkingEchoOff {
		return i18n.T("model_form.value.off")
	}
	return i18n.T("model_form.value.on")
}

// toggleValueLabel localizes an on/off toggle value.
func toggleValueLabel(v string) string {
	if v == contract.ReasoningToggleOff {
		return i18n.T("model_form.value.off")
	}
	return i18n.T("model_form.value.on")
}

// kindValueLabel localizes a reasoning-control kind.
func kindValueLabel(k contract.ReasoningKind) string {
	switch k {
	case contract.ReasoningKindToggle:
		return i18n.T("model_form.value.kind.toggle")
	case contract.ReasoningKindEffort:
		return i18n.T("model_form.value.kind.effort")
	}
	return i18n.T("model_form.value.kind.none")
}

// dialectValueLabel localizes a toggle dialect (the wire name stays visible — that is what the
// user must match to their gateway).
func dialectValueLabel(d contract.ReasoningToggleDialect) string {
	switch d {
	case contract.ToggleDialectThink:
		return i18n.T("model_form.value.dialect.think")
	case contract.ToggleDialectThinkingType:
		return i18n.T("model_form.value.dialect.thinking_type")
	}
	return i18n.T("model_form.value.dialect.enable_thinking")
}

// formCursorCols returns the display column of the editing cursor within a focused text field.
// The masked API key renders one bullet per rune, so its column is the rune cursor; other fields
// measure the true display width of the text before the cursor.
func formCursorCols(f *modelFormState, k modelFormField) int {
	buf := f.textBuf(k)
	if k == formFieldAPIKey {
		return buf.cur
	}
	return strW(string(buf.runes[:buf.cur]))
}

// padRight pads s with spaces to at least w display columns.
func padRight(s string, w int) string {
	pad := w - strW(s)
	if pad <= 0 {
		return s
	}
	return s + strings.Repeat(" ", pad)
}

// drawDashedBorder draws the dashed border around the dialog box.
func drawDashedBorder(box *Surface, boxW, boxH int) {
	border := styleToolDim()
	box.DrawPlain(0, 0, strings.Repeat("-", boxW), border)
	box.DrawPlain(0, boxH-1, strings.Repeat("-", boxW), border)
	for row := 1; row < boxH-1; row++ {
		box.DrawPlain(0, row, "|", border)
		box.DrawPlain(boxW-1, row, "|", border)
	}
}
