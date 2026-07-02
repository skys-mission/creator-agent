package tui

import (
	"fmt"
	"sort"
	"strings"

	"github.com/skys-mission/creator-agent/cmd/creator-agent/tui/i18n"
	"github.com/skys-mission/creator-agent/config"
)

// paletteMaxEntries caps the visible filtered list. The registry is small today, but the cap keeps
// rendering cost bounded and the list scannable.
const paletteMaxEntries = 20

func openPalette(a *App) {
	p := &a.palette
	closeAllOtherPickers(a, &a.palette.open)
	p.initPicker(a.width)
	p.entries = filterCommands("")
	a.forceRender = true
}

func closePalette(a *App) {
	a.palette.close(a)
}

func refilterPalette(a *App) {
	p := &a.palette
	p.entries = filterCommands(p.query.Value())
	if p.selIdx >= len(p.entries) {
		p.selIdx = maxi(0, len(p.entries)-1)
	}
	p.ensureVisible(a)
}

func allPaletteItems() []paletteItem {
	out := make([]paletteItem, 0, len(commandRegistry)+8)
	for _, e := range commandRegistry {
		out = append(out, paletteItem{name: e.name, desc: commandDesc(e.name)})
	}
	for _, theme := range SupportedThemes() {
		theme := theme
		label := themeDisplayName(theme)
		out = append(out, paletteItem{
			name:   "theme " + theme,
			desc:   i18n.T("palette.theme", label),
			action: func(a *App) { handleSlashCommand(a, "/themes "+theme) },
		})
	}
	return out
}

// filterCommands returns the registry entries matching the query, in a stable order: matched
// entries sorted by score (best first), with non-matching entries excluded. An empty query returns
// all commands in registry order.
func filterCommands(query string) []paletteItem {
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return allPaletteItems()
	}

	type scored struct {
		item  paletteItem
		score int
	}
	var hits []scored
	for _, it := range allPaletteItems() {
		s, ok := fuzzyScore(it.name, it.desc, q)
		if !ok {
			continue
		}
		hits = append(hits, scored{item: it, score: s})
	}
	// Stable descending sort by score (insertion sort; n is tiny). Stability preserves registry
	for i := 1; i < len(hits); i++ {
		for j := i; j > 0 && hits[j].score > hits[j-1].score; j-- {
			hits[j], hits[j-1] = hits[j-1], hits[j]
		}
	}

	n := len(hits)
	if n > paletteMaxEntries {
		n = paletteMaxEntries
	}
	out := make([]paletteItem, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, hits[i].item)
	}
	return out
}

// fuzzyScore reports whether q fuzzy-matches the name or description and returns a score.
func fuzzyScore(name, desc, q string) (int, bool) {
	ns, nOk := subseqScore(name, q)
	ds, dOk := subseqScore(desc, q)
	if !nOk && !dOk {
		return 0, false
	}
	switch {
	case nOk && dOk:
		return ns + ds/2, true
	case nOk:
		return ns, true
	default:
		return ds, true
	}
}

func subseqScore(target, q string) (int, bool) {
	if q == "" {
		return 0, true
	}
	t := strings.ToLower(target)
	qi := 0
	score := 0
	prevMatchIdx := -1
	for i := 0; i < len(t) && qi < len(q); i++ {
		if t[i] == q[qi] {
			if qi == 0 && i == 0 {
				score += 50
			}
			if prevMatchIdx >= 0 && i == prevMatchIdx+1 {
				score += 15
			}
			prevMatchIdx = i
			qi++
		}
	}
	if qi != len(q) {
		return 0, false
	}
	score += maxi(0, 20-len(t))
	return score, true
}

func handlePaletteKey(a *App, k Key, ch rune) {
	switch k {
	case KeyEsc, KeyCtrlP, KeyCtrlC:
		closePalette(a)
		return
	}
	if k == KeyRune && ch == '/' && a.palette.query.Value() == "" {
		return
	}
	a.palette.handleKey(a, k, ch, func() { commitPalette(a) }, func() { refilterPalette(a) })
}

func commitPalette(a *App) {
	p := &a.palette
	if len(p.entries) == 0 {
		return
	}
	item := p.entries[p.selIdx]
	closePalette(a)
	if item.action != nil {
		item.action(a)
		return
	}
	entry := findCommand(item.name)
	if entry == nil {
		return
	}
	if entry.hasArg {
		a.input.SetValue(entry.name + " ")
		return
	}
	entry.handler(a, "")
}

func paletteListHeight(a *App) int {
	h := paletteBoxHeight(a)
	listRows := h - 6
	if listRows < 1 {
		listRows = 1
	}
	return listRows
}

func paletteBoxHeight(a *App) int {
	h := a.height / 2
	if h < 8 {
		h = 8
	}
	if h > a.height-2 {
		h = a.height - 2
	}
	return h
}

func paletteBoxWidth(a *App) int {
	w := a.width * 3 / 5
	if w < 40 {
		w = 40
	}
	if w > a.width-2 {
		w = a.width - 2
	}
	return w
}

func clampScroll(scrollY, sel, h int) int {
	if h <= 0 {
		return 0
	}
	if sel < scrollY {
		return sel
	}
	if sel >= scrollY+h {
		return sel - h + 1
	}
	return scrollY
}

func drawPalette(a *App) {
	a.palette.draw(a, paletteRenderer{})
}

type paletteRenderer struct{}

func (paletteRenderer) title() string       { return i18n.T("picker.commands.title") }
func (paletteRenderer) placeholder() string { return i18n.T("picker.commands.placeholder") }
func (paletteRenderer) footer(a *App) string {
	return i18n.T("picker.commands.footer")
}
func (paletteRenderer) queryPrompt(a *App) (string, int) { return "> ", 2 }
func (paletteRenderer) cursor(a *App, innerX, queryY, promptWidth int) (int, int, bool) {
	return pickerQueryCursor(&a.palette.query, innerX, queryY, promptWidth)
}
func (paletteRenderer) drawItem(a *App, it paletteItem, selected bool, innerX, ry, innerW int) {
	const nameCol = 12
	marker := "  "
	nameStyle := stylePaletteItem()
	descStyle := stylePaletteItemDesc()
	if selected {
		marker = "> "
		nameStyle = stylePaletteSelected()
		descStyle = stylePaletteSelectedDesc()
	}
	drawTextRaw(a.screen, a, innerX, ry, 2, marker, nameStyle)
	drawTextRaw(a.screen, a, innerX+2, ry, nameCol, it.name, nameStyle)
	descX := innerX + 2 + nameCol + 1
	descW := innerW - (2 + nameCol + 1)
	if descW > 0 {
		drawTextRaw(a.screen, a, descX, ry, descW, truncateStr(it.desc, descW), descStyle)
	}
}

// --- agents picker ---

type agentsPickerItem struct {
	name        string
	description string
	readOnly    bool
}

type agentsPickerState struct {
	picker[agentsPickerItem]
}

func openAgentsPicker(a *App) {
	if len(a.rt.agents) == 0 {
		a.addSystem(i18n.T("picker.agents.empty"))
		return
	}
	items := buildAgentEntries(a)
	p := &a.agentsPicker
	closeAllOtherPickers(a, &a.agentsPicker.open)
	p.initPicker(a.width)
	p.entries = pinCurrentAgentEntry(items, a.rt.currentAgent)
	p.selIdx = 0
	for i, e := range p.entries {
		if e.name == a.rt.currentAgent {
			p.selIdx = i
			break
		}
	}
	a.forceRender = true
}

func closeAgentsPicker(a *App) {
	a.agentsPicker.close(a)
}

func buildAgentEntries(a *App) []agentsPickerItem {
	out := make([]agentsPickerItem, 0, len(a.rt.agents))
	for _, s := range a.rt.agents {
		out = append(out, agentsPickerItem{
			name:        s.Name,
			description: s.Description,
			readOnly:    s.ReadOnly,
		})
	}
	return out
}

func pinCurrentAgentEntry(items []agentsPickerItem, current string) []agentsPickerItem {
	out := make([]agentsPickerItem, 0, len(items))
	rest := make([]agentsPickerItem, 0, len(items))
	for _, it := range items {
		if it.name == current {
			out = append(out, it)
		} else {
			rest = append(rest, it)
		}
	}
	return append(out, rest...)
}

func refilterAgentsPicker(a *App) {
	if len(a.rt.agents) == 0 {
		a.agentsPicker.entries = nil
		return
	}
	a.agentsPicker.refilter(a, func() []agentsPickerItem { return buildAgentEntries(a) }, func(all []agentsPickerItem, q string) []agentsPickerItem {
		return filterAgentEntries(all, q, a.rt.currentAgent)
	})
}

func filterAgentEntries(all []agentsPickerItem, query, current string) []agentsPickerItem {
	q := normalizeLower(query)
	var matched []agentsPickerItem
	if q == "" {
		matched = all
	} else {
		for _, it := range all {
			if score, ok := fuzzyScore(it.name, it.description, q); ok && score > 0 {
				matched = append(matched, it)
			}
		}
	}
	pinned := make([]agentsPickerItem, 0, len(matched))
	rest := make([]agentsPickerItem, 0, len(matched))
	for _, it := range matched {
		if it.name == current {
			pinned = append(pinned, it)
		} else {
			rest = append(rest, it)
		}
	}
	return append(pinned, rest...)
}

func (*agentsPickerState) onKey(a *App, k Key, ch rune) {
	a.agentsPicker.handleKeyOrClose(a, k, ch,
		func() { closeAgentsPicker(a) }, func() { commitAgentsPicker(a) }, func() { refilterAgentsPicker(a) })
}

func commitAgentsPicker(a *App) {
	p := &a.agentsPicker
	if len(p.entries) == 0 {
		return
	}
	sel := p.entries[p.selIdx]
	closeAgentsPicker(a)
	if sel.name != a.rt.currentAgent {
		if err := applyAgentSwitch(a, sel.name); err != nil {
			a.addSystem(fmt.Sprintf(i18n.T("picker.agents.switch_failed"), err))
		}
	}
}

func (*agentsPickerState) render(a *App) {
	a.agentsPicker.draw(a, agentsRenderer{})
}

type agentsRenderer struct{}

func (agentsRenderer) title() string       { return i18n.T("picker.agents.title") }
func (agentsRenderer) placeholder() string { return i18n.T("picker.agents.placeholder") }
func (agentsRenderer) footer(a *App) string {
	return i18n.T("picker.agents.footer")
}
func (agentsRenderer) queryPrompt(a *App) (string, int) { return "> ", 2 }
func (agentsRenderer) cursor(a *App, innerX, queryY, promptWidth int) (int, int, bool) {
	return pickerQueryCursor(&a.agentsPicker.query, innerX, queryY, promptWidth)
}
func (agentsRenderer) drawItem(a *App, it agentsPickerItem, selected bool, innerX, ry, innerW int) {
	const nameCol = 16
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
	if it.name == a.rt.currentAgent {
		nameText = "• " + it.name
	}
	if it.readOnly {
		nameText += " [ro]"
	}
	drawTextRaw(a.screen, a, innerX+2, ry, nameCol+2, truncateStr(nameText, nameCol+2), nameStyle)
	drawTextRaw(a.screen, a, innerX+2+nameCol+2, ry, descW, truncateStr(it.description, descW), descStyle)
}

// --- models picker ---

type modelPickerItem struct {
	name  string
	model string
	host  string
}

type modelPickerState struct {
	picker[modelPickerItem]
}

func openModelPicker(a *App) {
	if len(a.rt.profiles) == 0 {
		a.addSystem(i18n.T("picker.models.empty"))
		return
	}
	items := buildModelEntries(a)
	p := &a.modelPicker
	closeAllOtherPickers(a, &a.modelPicker.open)
	p.initPicker(a.width)
	p.entries = pinCurrentModelEntry(items, a.rt.prof.Name)
	p.selIdx = 0
	for i, e := range p.entries {
		if e.name == a.rt.prof.Name {
			p.selIdx = i
			break
		}
	}
	a.forceRender = true
}

func closeModelPicker(a *App) {
	a.modelPicker.close(a)
}

func buildModelEntries(a *App) []modelPickerItem {
	out := make([]modelPickerItem, 0, len(a.rt.profiles))
	for name, prof := range a.rt.profiles {
		out = append(out, modelPickerItem{
			name:  name,
			model: prof.Model,
			host:  hostOf(prof.BaseURL),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].name < out[j].name })
	return out
}

func pinCurrentModelEntry(items []modelPickerItem, current string) []modelPickerItem {
	out := make([]modelPickerItem, 0, len(items))
	rest := make([]modelPickerItem, 0, len(items))
	for _, it := range items {
		if it.name == current {
			out = append(out, it)
		} else {
			rest = append(rest, it)
		}
	}
	return append(out, rest...)
}

func refilterModelPicker(a *App) {
	if len(a.rt.profiles) == 0 {
		a.modelPicker.entries = nil
		return
	}
	a.modelPicker.refilter(a, func() []modelPickerItem { return buildModelEntries(a) }, func(all []modelPickerItem, q string) []modelPickerItem {
		return filterModelEntries(all, q, a.rt.prof.Name)
	})
}

func filterModelEntries(all []modelPickerItem, query, current string) []modelPickerItem {
	q := normalizeLower(query)
	var matched []modelPickerItem
	if q == "" {
		matched = all
	} else {
		for _, it := range all {
			if score, ok := fuzzyScore(it.name, it.model+" "+it.host, q); ok && score > 0 {
				matched = append(matched, it)
			}
		}
	}
	pinned := make([]modelPickerItem, 0, len(matched))
	rest := make([]modelPickerItem, 0, len(matched))
	for _, it := range matched {
		if it.name == current {
			pinned = append(pinned, it)
		} else {
			rest = append(rest, it)
		}
	}
	return append(pinned, rest...)
}

func (*modelPickerState) onKey(a *App, k Key, ch rune) {
	a.modelPicker.handleKeyOrClose(a, k, ch,
		func() { closeModelPicker(a) }, func() { commitModelPicker(a) }, func() { refilterModelPicker(a) })
}

func commitModelPicker(a *App) {
	p := &a.modelPicker
	if len(p.entries) == 0 {
		closeModelPicker(a)
		return
	}
	sel := p.entries[p.selIdx]
	closeModelPicker(a)
	if sel.name != a.rt.prof.Name {
		if err := applyProfileSwitch(a, sel.name); err != nil {
			a.addSystem(fmt.Sprintf(i18n.T("picker.models.switch_failed"), err))
		}
	}
}

func (*modelPickerState) render(a *App) {
	a.modelPicker.draw(a, modelRenderer{})
}

type modelRenderer struct{}

func (modelRenderer) title() string       { return i18n.T("picker.models.title") }
func (modelRenderer) placeholder() string { return i18n.T("picker.models.placeholder") }
func (modelRenderer) footer(a *App) string {
	return i18n.T("picker.models.footer")
}
func (modelRenderer) queryPrompt(a *App) (string, int) { return "> ", 2 }
func (modelRenderer) cursor(a *App, innerX, queryY, promptWidth int) (int, int, bool) {
	return pickerQueryCursor(&a.modelPicker.query, innerX, queryY, promptWidth)
}
func (modelRenderer) drawItem(a *App, it modelPickerItem, selected bool, innerX, ry, innerW int) {
	const nameCol = 14
	mhW := innerW - 2 - nameCol - 1
	if mhW < 8 {
		mhW = 8
	}
	nameStyle := stylePaletteItem()
	mhStyle := stylePaletteItemDesc()
	if selected {
		nameStyle = stylePaletteSelected()
		mhStyle = stylePaletteSelectedDesc()
	}
	marker := "  "
	if selected {
		marker = "> "
	}
	drawTextRaw(a.screen, a, innerX, ry, 2, marker, nameStyle)
	nameText := it.name
	if it.name == a.rt.prof.Name {
		nameText = "• " + it.name
	}
	drawTextRaw(a.screen, a, innerX+2, ry, nameCol+2, truncateStr(nameText, nameCol+2), nameStyle)
	mh := it.model
	if it.host != "" {
		mh = it.model + " @ " + it.host
	}
	drawTextRaw(a.screen, a, innerX+2+nameCol+2, ry, mhW, truncateStr(mh, mhW), mhStyle)
}

// --- variants picker ---

const variantDefaultName = "default"

type variantPickerItem struct {
	name        string
	description string
}

type variantPickerState struct {
	picker[variantPickerItem]
}

func openVariantPicker(a *App) {
	if len(a.rt.variants) == 0 {
		a.addSystem(i18n.T("picker.variants.empty"))
		return
	}
	items := buildVariantEntries(a)
	p := &a.variantPicker
	closeAllOtherPickers(a, &a.variantPicker.open)
	p.initPicker(a.width)
	p.entries = items
	cur := normalizeLower(a.rt.currentVariant)
	if cur == "" {
		cur = variantDefaultName
	}
	p.selIdx = 0
	for i, e := range p.entries {
		if normalizeLower(e.name) == cur {
			p.selIdx = i
			break
		}
	}
	a.forceRender = true
}

func closeVariantPicker(a *App) {
	a.variantPicker.close(a)
}

func buildVariantEntries(a *App) []variantPickerItem {
	out := []variantPickerItem{{name: variantDefaultName, description: i18n.T("picker.variants.no_overrides")}}
	names := make([]string, 0, len(a.rt.variants))
	for n := range a.rt.variants {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		out = append(out, variantPickerItem{
			name:        n,
			description: variantSummary(a.rt.variants[n]),
		})
	}
	return out
}

func variantSummary(v config.Variant) string {
	var parts []string
	if len(v.Headers) > 0 {
		parts = append(parts, fmt.Sprintf(i18n.T("variant.summary.header"), len(v.Headers)))
	}
	if len(v.Body) > 0 {
		parts = append(parts, fmt.Sprintf(i18n.T("variant.summary.body"), len(v.Body)))
	}
	if v.Temperature != nil {
		parts = append(parts, fmt.Sprintf(i18n.T("variant.summary.temp"), *v.Temperature))
	}
	if v.TopP != nil {
		parts = append(parts, fmt.Sprintf(i18n.T("variant.summary.top_p"), *v.TopP))
	}
	if v.MaxTokens != nil {
		parts = append(parts, fmt.Sprintf(i18n.T("variant.summary.max_tokens"), *v.MaxTokens))
	}
	if len(parts) == 0 {
		return i18n.T("picker.variants.no_overrides")
	}
	return strings.Join(parts, ", ")
}

func refilterVariantPicker(a *App) {
	if len(a.rt.variants) == 0 {
		a.variantPicker.entries = nil
		return
	}
	a.variantPicker.refilter(a, func() []variantPickerItem { return buildVariantEntries(a) }, filterVariantEntries)
}

func filterVariantEntries(all []variantPickerItem, query string) []variantPickerItem {
	q := normalizeLower(query)
	var matched []variantPickerItem
	if q == "" {
		matched = all
	} else {
		for _, it := range all {
			if it.name == variantDefaultName {
				matched = append(matched, it)
				continue
			}
			if score, ok := fuzzyScore(it.name, it.description, q); ok && score > 0 {
				matched = append(matched, it)
			}
		}
	}
	return matched
}

func (*variantPickerState) onKey(a *App, k Key, ch rune) {
	a.variantPicker.handleKeyOrClose(a, k, ch,
		func() { closeVariantPicker(a) }, func() { commitVariantPicker(a) }, func() { refilterVariantPicker(a) })
}

func commitVariantPicker(a *App) {
	p := &a.variantPicker
	if len(p.entries) == 0 {
		closeVariantPicker(a)
		return
	}
	sel := p.entries[p.selIdx]
	closeVariantPicker(a)
	if normalizeLower(sel.name) == normalizeLower(a.rt.currentVariant) {
		return
	}
	target := sel.name
	if target == variantDefaultName {
		target = ""
	}
	if err := applyVariantSwitch(a, target); err != nil {
		a.addSystem(fmt.Sprintf(i18n.T("picker.variants.switch_failed"), err))
	}
}

func (*variantPickerState) render(a *App) {
	a.variantPicker.draw(a, variantRenderer{})
}

type variantRenderer struct{}

func (variantRenderer) title() string       { return i18n.T("picker.variants.title") }
func (variantRenderer) placeholder() string { return i18n.T("picker.variants.placeholder") }
func (variantRenderer) footer(a *App) string {
	return i18n.T("picker.variants.footer")
}
func (variantRenderer) queryPrompt(a *App) (string, int) { return "> ", 2 }
func (variantRenderer) cursor(a *App, innerX, queryY, promptWidth int) (int, int, bool) {
	return pickerQueryCursor(&a.variantPicker.query, innerX, queryY, promptWidth)
}
func (variantRenderer) drawItem(a *App, it variantPickerItem, selected bool, innerX, ry, innerW int) {
	const nameCol = 16
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
	cur := normalizeLower(a.rt.currentVariant)
	if cur == "" {
		cur = variantDefaultName
	}
	nameText := it.name
	if normalizeLower(it.name) == cur {
		nameText = "• " + it.name
	}
	drawTextRaw(a.screen, a, innerX+2, ry, nameCol+2, truncateStr(nameText, nameCol+2), nameStyle)
	drawTextRaw(a.screen, a, innerX+2+nameCol+2, ry, descW, truncateStr(it.description, descW), descStyle)
}

// --- themes picker ---

type themePickerItem struct {
	name string
}

type themePickerState struct {
	picker[themePickerItem]

	savedName string
	savedPal  *palette
}

func openThemePicker(a *App) {
	items := buildThemeEntries(a)
	p := &a.themePicker
	closeAllOtherPickers(a, &a.themePicker.open)
	p.initPicker(a.width)
	p.entries = items
	a.themePicker.savedName = currentTheme()
	a.themePicker.savedPal = palettePtr.Load()
	p.selIdx = 0
	for i, e := range p.entries {
		if e.name == currentTheme() {
			p.selIdx = i
			break
		}
	}
	a.forceRender = true
}

func closeThemePicker(a *App) {
	a.themePicker.close(a)
}

func buildThemeEntries(a *App) []themePickerItem {
	names := SupportedThemes()
	out := make([]themePickerItem, 0, len(names))
	for _, n := range names {
		out = append(out, themePickerItem{name: n})
	}
	return out
}

func previewTheme(a *App, name string) {
	normalized := normalizeThemeName(name)
	palettePtr.Store(paletteForName(normalized))
	a.forceRender = true
}

func refilterThemePicker(a *App) {
	p := &a.themePicker
	p.refilter(a, func() []themePickerItem { return buildThemeEntries(a) }, filterThemeEntries)
	if len(p.entries) > 0 {
		previewTheme(a, p.entries[p.selIdx].name)
	} else if a.themePicker.savedName != "" {
		previewTheme(a, a.themePicker.savedName)
	}
}

func filterThemeEntries(all []themePickerItem, query string) []themePickerItem {
	q := normalizeLower(query)
	if q == "" {
		return all
	}
	var matched []themePickerItem
	for _, it := range all {
		if score, ok := fuzzyScore(it.name, "", q); ok && score > 0 {
			matched = append(matched, it)
		}
	}
	return matched
}

func (*themePickerState) onKey(a *App, k Key, ch rune) {
	if a.themePicker.handleKeyOrClose(a, k, ch,
		func() { revertAndCloseThemePicker(a) }, func() { commitThemePicker(a) }, func() { refilterThemePicker(a) }) && isPickerNavKey(k) {
		previewSelectedTheme(a)
	}
}

func previewSelectedTheme(a *App) {
	p := &a.themePicker
	if len(p.entries) == 0 {
		return
	}
	previewTheme(a, p.entries[p.selIdx].name)
}

func commitThemePicker(a *App) {
	p := &a.themePicker
	if len(p.entries) == 0 {
		revertAndCloseThemePicker(a)
		return
	}
	sel := p.entries[p.selIdx].name
	applyTheme(sel)
	a.themePicker.savedName = ""
	a.themePicker.savedPal = nil
	closeThemePicker(a)
}

func revertAndCloseThemePicker(a *App) {
	p := &a.themePicker
	if p.savedName != "" && p.savedPal != nil {
		palettePtr.Store(p.savedPal)
		a.forceRender = true
	}
	closeThemePicker(a)
}

func (*themePickerState) render(a *App) {
	a.themePicker.draw(a, themeRenderer{})
}

type themeRenderer struct{}

func (themeRenderer) title() string       { return i18n.T("picker.themes.title") }
func (themeRenderer) placeholder() string { return i18n.T("picker.themes.placeholder") }
func (themeRenderer) footer(a *App) string {
	return i18n.T("picker.themes.footer")
}
func (themeRenderer) queryPrompt(a *App) (string, int) { return "> ", 2 }
func (themeRenderer) cursor(a *App, innerX, queryY, promptWidth int) (int, int, bool) {
	return pickerQueryCursor(&a.themePicker.query, innerX, queryY, promptWidth)
}
func (themeRenderer) drawItem(a *App, it themePickerItem, selected bool, innerX, ry, innerW int) {
	nameStyle := stylePaletteItem()
	if selected {
		nameStyle = stylePaletteSelected()
	}
	marker := "  "
	if selected {
		marker = "> "
	}
	drawTextRaw(a.screen, a, innerX, ry, 2, marker, nameStyle)
	nameText := it.name
	if it.name == a.themePicker.savedName {
		nameText = "• " + it.name
	}
	drawTextRaw(a.screen, a, innerX+2, ry, innerW-2, nameText, nameStyle)
}

// themeDisplayName returns a human-readable label for a theme canonical id.
func themeDisplayName(theme string) string {
	switch theme {
	case "dark":
		return i18n.T("theme.display.dark")
	case "light":
		return i18n.T("theme.display.light")
	case "catppuccin":
		return i18n.T("theme.display.catppuccin")
	default:
		return LocaleTitlecase(theme)
	}
}
