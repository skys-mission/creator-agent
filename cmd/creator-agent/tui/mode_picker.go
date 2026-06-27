package tui

import (
	"fmt"

	"github.com/skys-mission/creator-agent/cmd/creator-agent/tui/i18n"
	"github.com/skys-mission/creator-agent/core/middlewares"
)

// --- permission mode picker (default / trust / auto / readonly) ---

type modePickerItem struct {
	name        string
	description string
}

type modePickerState struct {
	picker[modePickerItem]
}

// modeEntries is the fixed set of selectable permission modes, ordered by trust gradient
// (least → most autonomous): default → trust → auto, with readonly as a hard block mode.
var modeEntries = []modePickerItem{
	{name: "default", description: i18n.T("mode.desc.default")},
	{name: "trust", description: i18n.T("mode.desc.trust")},
	{name: "auto", description: i18n.T("mode.desc.auto")},
	{name: "readonly", description: i18n.T("mode.desc.readonly")},
}

func openModePicker(a *App) {
	if a.rt.modeCtl == nil {
		a.addSystem(i18n.T("mode.not_enabled"))
		return
	}
	p := &a.modePicker
	closeAllOtherPickers(a, &a.modePicker.open)
	p.initPicker(a.width)
	p.entries = modeEntries
	cur := currentModeNormalized(a)
	p.selIdx = 0
	for i, e := range p.entries {
		if string(middlewares.NormalizeMode(middlewares.Mode(e.name))) == cur {
			p.selIdx = i
			break
		}
	}
	a.forceRender = true
}

func closeModePicker(a *App) {
	a.modePicker.close(a)
}

func refilterModePicker(a *App) {
	a.modePicker.refilter(a, func() []modePickerItem { return modeEntries }, filterModeEntries)
}

func filterModeEntries(all []modePickerItem, query string) []modePickerItem {
	q := normalizeLower(query)
	if q == "" {
		return all
	}
	var matched []modePickerItem
	for _, it := range all {
		if score, ok := fuzzyScore(it.name, it.description, q); ok && score > 0 {
			matched = append(matched, it)
		}
	}
	return matched
}

func (*modePickerState) onKey(a *App, k Key, ch rune) {
	switch k {
	case KeyEsc, KeyCtrlC:
		closeModePicker(a)
		return
	}
	a.modePicker.handleKey(a, k, ch, func() { commitModePicker(a) }, func() { refilterModePicker(a) })
}

func commitModePicker(a *App) {
	p := &a.modePicker
	if len(p.entries) == 0 {
		closeModePicker(a)
		return
	}
	sel := p.entries[p.selIdx]
	closeModePicker(a)
	applyModeSwitch(a, sel.name)
}

func (*modePickerState) render(a *App) {
	a.modePicker.draw(a, modeRenderer{})
}

type modeRenderer struct{}

func (modeRenderer) title() string       { return i18n.T("picker.mode.title") }
func (modeRenderer) placeholder() string { return i18n.T("picker.mode.placeholder") }
func (modeRenderer) footer(a *App) string {
	return i18n.T("picker.mode.footer")
}
func (modeRenderer) queryPrompt(a *App) (string, int) { return "> ", 2 }
func (modeRenderer) cursor(a *App, innerX, queryY, promptWidth int) (int, int, bool) {
	return pickerQueryCursor(&a.modePicker.query, innerX, queryY, promptWidth)
}
func (modeRenderer) drawItem(a *App, it modePickerItem, selected bool, innerX, ry, innerW int) {
	const nameCol = 12
	descW := innerW - 2 - nameCol - 1
	if descW < 8 {
		descW = 8
	}
	nameStyle := stylePaletteItem()
	descStyle := stylePaletteItemDesc()
	if selected {
		nameStyle = stylePaletteSelected()
		descStyle = stylePaletteSelectedDesc()
	}
	marker := "  "
	if selected {
		marker = "> "
	}
	drawTextRaw(a.screen, a, innerX, ry, 2, marker, nameStyle)
	nameText := it.name
	if string(middlewares.NormalizeMode(middlewares.Mode(it.name))) == currentModeNormalized(a) {
		nameText = "• " + it.name
	}
	drawTextRaw(a.screen, a, innerX+2, ry, nameCol+2, truncateStr(nameText, nameCol+2), nameStyle)
	drawTextRaw(a.screen, a, innerX+2+nameCol+2, ry, descW, truncateStr(it.description, descW), descStyle)
}

// handleModeCommand implements /mode [name]: with no arg it opens the picker; with a name it switches
// directly. The mode is shared with the permission middleware, so the switch takes effect on the next
// tool call with no agent rebuild.
func handleModeCommand(a *App, arg string) bool {
	if a.rt.modeCtl == nil {
		a.addSystem(i18n.T("mode.not_enabled"))
		return true
	}
	if arg == "" {
		openModePicker(a)
		return true
	}
	applyModeSwitch(a, arg)
	return true
}

// applyModeSwitch sets the live permission mode via the shared controller and updates the display
// cache. Takes effect on the next tool call (read by the permission middleware).
func applyModeSwitch(a *App, name string) {
	if a.rt.modeCtl == nil {
		a.addSystem(i18n.T("mode.not_enabled_build"))
		return
	}
	mode := string(middlewares.NormalizeMode(middlewares.Mode(name)))
	if a.rt.currentMode == mode {
		a.addSystem(fmt.Sprintf(i18n.T("mode.already"), mode))
		return
	}
	a.rt.modeCtl.Set(middlewares.Mode(mode))
	a.rt.currentMode = mode
	a.forceRender = true
	a.addSystem(fmt.Sprintf(i18n.T("mode.switched"), mode, modeSwitchHint(mode)))
}

// currentModeNormalized returns the display-cached mode, falling back to ModeDefault when unset.
func currentModeNormalized(a *App) string {
	if m := a.rt.currentMode; m != "" {
		return m
	}
	return string(middlewares.ModeDefault)
}

// modeSwitchHint returns a one-line behavior reminder shown after a mode switch.
func modeSwitchHint(mode string) string {
	switch middlewares.Mode(mode) {
	case middlewares.ModeDefault:
		return i18n.T("mode.hint.default")
	case middlewares.ModeTrust:
		return i18n.T("mode.hint.trust")
	case middlewares.ModeAuto:
		return i18n.T("mode.hint.auto")
	case middlewares.ModeReadonly:
		return i18n.T("mode.hint.readonly")
	}
	return ""
}

// modeStyle returns a theme-aware style for the mode badge: auto = alert (red, bold),
// trust = caution (warning), readonly = info, default = muted.
func modeStyle(mode string) Style {
	switch middlewares.Mode(mode) {
	case middlewares.ModeAuto:
		return styleError()
	case middlewares.ModeTrust:
		return styleWarning()
	case middlewares.ModeReadonly:
		return styleInfo()
	default:
		return styleTextMuted()
	}
}

// cyclePermissionMode advances the permission mode one step along the trust gradient and wraps:
// default → trust → auto → readonly → default. No-op when mode features are disabled (nil controller).
// Bound to Shift+Tab (aligns with Claude Code's chat:cycleMode). Uses a short status line rather
// than the full switch hint, since the user may tap repeatedly to find the desired mode.
func cyclePermissionMode(a *App) {
	if a.rt.modeCtl == nil {
		return
	}
	order := []middlewares.Mode{middlewares.ModeDefault, middlewares.ModeTrust, middlewares.ModeAuto, middlewares.ModeReadonly}
	cur := middlewares.NormalizeMode(middlewares.Mode(currentModeNormalized(a)))
	next := order[0]
	for i, m := range order {
		if m == cur {
			next = order[(i+1)%len(order)]
			break
		}
	}
	a.rt.modeCtl.Set(next)
	a.rt.currentMode = string(next)
	a.forceRender = true
	a.addSystem(fmt.Sprintf(i18n.T("mode.cycled"), next))
}
