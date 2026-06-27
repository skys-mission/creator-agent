package tui

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

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

// --- sessions picker ---

type sessionPickerItem struct {
	id        string
	title     string
	updatedAt time.Time
	pinned    bool
}

type sessionPickerMode int

const (
	pickModeBrowse sessionPickerMode = iota
	pickModeRename
)

type sessionPickerState struct {
	picker[sessionPickerItem]

	mode          sessionPickerMode
	renameBuf     inputBuffer
	pendingDelete string
}

func openSessionPicker(a *App) {
	if a.rt.store == nil {
		a.addSystem(i18n.T("picker.sessions.no_store"))
		return
	}
	infos, err := a.rt.store.List()
	if err != nil {
		a.addSystem(fmt.Sprintf(i18n.T("picker.sessions.list_failed"), err))
		return
	}
	p := &a.sessionPicker
	closeAllOtherPickers(a, &a.sessionPicker.open)
	p.initPicker(a.width)
	p.entries = make([]sessionPickerItem, 0, len(infos))
	for _, info := range infos {
		p.entries = append(p.entries, sessionPickerItem{
			id: info.ID, title: info.Title, updatedAt: info.UpdatedAt, pinned: info.Pinned,
		})
	}
	p.selIdx = 0
	for i, e := range p.entries {
		if e.id == a.rt.sessionID {
			p.selIdx = i
			break
		}
	}
	a.sessionPicker.mode = pickModeBrowse
	a.sessionPicker.pendingDelete = ""
	a.sessionPicker.renameBuf.SetValue("")
	a.forceRender = true
}

func closeSessionPicker(a *App) {
	a.sessionPicker.close(a)
}

func refilterSessionPicker(a *App) {
	if a.rt.store == nil {
		a.sessionPicker.entries = nil
		return
	}
	infos, err := a.rt.store.List()
	if err != nil {
		a.sessionPicker.entries = nil
		return
	}
	all := make([]sessionPickerItem, 0, len(infos))
	for _, info := range infos {
		all = append(all, sessionPickerItem{id: info.ID, title: info.Title, updatedAt: info.UpdatedAt, pinned: info.Pinned})
	}
	a.sessionPicker.refilter(a, func() []sessionPickerItem { return all }, func(items []sessionPickerItem, q string) []sessionPickerItem {
		return filterSessionsFromInfos(items, q, a.rt.sessionID)
	})
}

func filterSessionsFromInfos(all []sessionPickerItem, query, currentID string) []sessionPickerItem {
	q := normalizeLower(query)
	var matched []sessionPickerItem
	if q == "" {
		matched = all
	} else {
		for _, it := range all {
			if score, ok := fuzzyScore(it.title, it.id, q); ok && score > 0 {
				matched = append(matched, it)
			}
		}
	}
	current := make([]sessionPickerItem, 0, 1)
	pinned := make([]sessionPickerItem, 0, len(matched))
	rest := make([]sessionPickerItem, 0, len(matched))
	for _, it := range matched {
		switch {
		case it.id == currentID:
			current = append(current, it)
		case it.pinned:
			pinned = append(pinned, it)
		default:
			rest = append(rest, it)
		}
	}
	out := make([]sessionPickerItem, 0, len(matched))
	out = append(out, current...)
	out = append(out, pinned...)
	out = append(out, rest...)
	return out
}

func (*sessionPickerState) onKey(a *App, k Key, ch rune) {
	p := &a.sessionPicker
	if p.mode == pickModeRename {
		handleSessionRenameKey(a, k, ch)
		return
	}
	switch k {
	case KeyEsc, KeyCtrlP:
		closeSessionPicker(a)
		return
	case KeyCtrlC:
		closeSessionPicker(a)
		return
	}
	if isPickerNavKey(k) {
		p.pendingDelete = ""
	}
	if k == KeyRune && p.query.Value() == "" && len(p.entries) > 0 && ch == 'p' {
		togglePinSession(a)
		return
	}
	if k == KeyCtrlR && p.query.Value() == "" && len(p.entries) > 0 {
		enterRenameMode(a)
		return
	}
	if k == KeyCtrlD && p.query.Value() == "" && len(p.entries) > 0 {
		handleDeleteConfirm(a)
		return
	}
	p.handleKey(a, k, ch, func() { commitSessionPicker(a) }, func() { refilterSessionPicker(a) })
}

func commitSessionPicker(a *App) {
	p := &a.sessionPicker
	if len(p.entries) == 0 {
		closeSessionPicker(a)
		return
	}
	sel := p.entries[p.selIdx]
	closeSessionPicker(a)
	if sel.id != a.rt.sessionID {
		switchSession(a, sel.id)
	}
}

func selectedSessionID(a *App) string {
	p := &a.sessionPicker
	if len(p.entries) == 0 || p.selIdx < 0 || p.selIdx >= len(p.entries) {
		return ""
	}
	return p.entries[p.selIdx].id
}

func togglePinSession(a *App) {
	p := &a.sessionPicker
	id := selectedSessionID(a)
	if id == "" {
		return
	}
	mut := sessionMutator(a.rt.store)
	if mut == nil {
		a.addSystem(i18n.T("picker.sessions.pin_unavailable"))
		return
	}
	curPinned := false
	if p.selIdx < len(p.entries) {
		curPinned = p.entries[p.selIdx].pinned
	}
	if err := mut.SetPinned(id, !curPinned); err != nil {
		a.addSystem(fmt.Sprintf(i18n.T("picker.sessions.pin_failed"), err))
		return
	}
	refilterSessionPicker(a)
}

func enterRenameMode(a *App) {
	id := selectedSessionID(a)
	if id == "" {
		return
	}
	if sessionMutator(a.rt.store) == nil {
		a.addSystem(i18n.T("picker.sessions.rename_unavailable"))
		return
	}
	p := &a.sessionPicker
	curTitle := ""
	if p.selIdx < len(p.entries) {
		curTitle = p.entries[p.selIdx].title
	}
	p.renameBuf.SetWidth(maxi(10, a.width-16))
	p.renameBuf.SetValue(curTitle)
	p.mode = pickModeRename
}

func handleSessionRenameKey(a *App, k Key, ch rune) {
	p := &a.sessionPicker
	switch k {
	case KeyEnter:
		commitRename(a)
		return
	case KeyEsc:
		p.mode = pickModeBrowse
		return
	case KeyBackspace, KeyBackspace2:
		p.renameBuf.Backspace()
		return
	case KeyDelete:
		p.renameBuf.Delete()
		return
	case KeyLeft:
		p.renameBuf.CursorLeft()
		return
	case KeyRight:
		p.renameBuf.CursorRight()
		return
	case KeyHome:
		p.renameBuf.CursorStart()
		return
	case KeyEnd:
		p.renameBuf.CursorEnd()
		return
	case KeyCtrlU:
		p.renameBuf.SetValue("")
		return
	}
	if k == KeyRune {
		p.renameBuf.InsertRune(ch)
	}
}

func commitRename(a *App) {
	p := &a.sessionPicker
	id := selectedSessionID(a)
	p.mode = pickModeBrowse
	if id == "" {
		return
	}
	newTitle := strings.TrimSpace(p.renameBuf.Value())
	if newTitle == "" {
		return
	}
	mut := sessionMutator(a.rt.store)
	if mut == nil {
		return
	}
	if err := mut.Rename(id, newTitle); err != nil {
		a.addSystem(fmt.Sprintf(i18n.T("picker.sessions.rename_failed"), err))
		return
	}
	refilterSessionPicker(a)
}

func handleDeleteConfirm(a *App) {
	p := &a.sessionPicker
	id := selectedSessionID(a)
	if id == "" {
		return
	}
	if p.pendingDelete == id {
		confirmDeleteSession(a, id)
		return
	}
	p.pendingDelete = id
}

func confirmDeleteSession(a *App, id string) {
	p := &a.sessionPicker
	p.pendingDelete = ""
	if a.rt.store == nil {
		return
	}
	if err := a.rt.store.Clear(id); err != nil {
		a.addSystem(fmt.Sprintf(i18n.T("picker.sessions.delete_failed"), err))
		return
	}
	wasCurrent := id == a.rt.sessionID
	refilterSessionPicker(a)
	if p.selIdx >= len(p.entries) {
		p.selIdx = maxi(0, len(p.entries)-1)
	}
	a.addSystem(fmt.Sprintf(i18n.T("picker.sessions.deleted"), shortID(id)))
	if wasCurrent {
		newSession(a)
		closeSessionPicker(a)
	}
}

func (*sessionPickerState) render(a *App) {
	a.sessionPicker.draw(a, sessionRenderer{})
}

type sessionRenderer struct{}

func (sessionRenderer) title() string       { return i18n.T("picker.sessions.title") }
func (sessionRenderer) placeholder() string { return i18n.T("picker.sessions.placeholder") }
func (sessionRenderer) footer(a *App) string {
	if a.sessionPicker.pendingDelete != "" {
		return i18n.T("picker.sessions.footer.delete")
	}
	return i18n.T("picker.sessions.footer.browse")
}
func (sessionRenderer) queryPrompt(a *App) (string, int) {
	if a.sessionPicker.mode == pickModeRename {
		return i18n.T("picker.sessions.rename_prompt"), 9
	}
	return "> ", 2
}
func (sessionRenderer) cursor(a *App, innerX, queryY, promptWidth int) (int, int, bool) {
	if a.sessionPicker.mode == pickModeRename {
		_, rc := a.sessionPicker.renameBuf.cursorPos()
		return innerX + 9 + rc, queryY, true
	}
	return pickerQueryCursor(&a.sessionPicker.query, innerX, queryY, promptWidth)
}
func (sessionRenderer) drawItem(a *App, it sessionPickerItem, selected bool, innerX, ry, innerW int) {
	const idCol = 12
	const timeCol = 10
	titleW := innerW - 2 - idCol - 1 - timeCol
	if titleW < 8 {
		titleW = 8
	}
	p := &a.sessionPicker
	isCurrent := it.id == a.rt.sessionID
	isPendingDelete := p.pendingDelete == it.id

	titleStyle := stylePaletteItem()
	idStyle := stylePaletteItemDesc()
	if selected {
		titleStyle = stylePaletteSelected()
		idStyle = stylePaletteSelectedDesc()
	}
	if isPendingDelete {
		titleStyle = styleErrorBar()
	}
	marker := "  "
	if selected {
		marker = "> "
	}
	drawTextRaw(a.screen, a, innerX, ry, 2, marker, titleStyle)
	prefix := ""
	if it.pinned {
		prefix += "★ "
	}
	if isCurrent {
		prefix += "• "
	}
	titleText := prefix + displaySessionTitle(it.title)
	drawTextRaw(a.screen, a, innerX+2, ry, titleW+2, truncateStr(titleText, titleW+2), titleStyle)
	idX := innerX + 2 + titleW + 2
	drawTextRaw(a.screen, a, idX, ry, idCol, truncateStr(shortID(it.id), idCol), idStyle)
	ts := relativeTime(it.updatedAt)
	timeX := innerX + innerW - timeCol
	if timeX > idX+idCol {
		drawTextRaw(a.screen, a, timeX, ry, timeCol, ts, idStyle)
	}
}

// --- mcp picker ---

type MCPManager interface {
	Status() []MCPManagerServerStatus
	SetEnabled(ctx context.Context, name string, enabled bool) error
	SetToolEnabled(ctx context.Context, server, tool string, enabled bool) error
	HasServers() bool
}

type MCPManagerServerStatus struct {
	Name       string
	Enabled    bool
	Connected  bool
	Connecting bool
	ToolCount  int
	LastErr    string
	Tools      []MCPManagerToolStatus
}

type MCPManagerToolStatus struct {
	Name    string
	Enabled bool
}

type mcpPickerEntryKind int

const (
	mcpEntryServer mcpPickerEntryKind = iota
	mcpEntryTool
)

type mcpPickerEntry struct {
	kind       mcpPickerEntryKind
	server     string
	tool       string
	toolOn     bool
	serverOn   bool
	connected  bool
	connecting bool
	toolCount  int
	lastErr    string
	expanded   bool
}

type mcpPickerState struct {
	picker[mcpPickerEntry]

	toggling     string
	toggleCancel context.CancelFunc
	lastErr      string
	collapsed    map[string]bool
}

func openMCPPicker(a *App) {
	if a.rt.mcpManager == nil {
		a.addSystem(i18n.T("picker.mcp.no_manager"))
		return
	}
	if !a.rt.mcpManager.HasServers() {
		a.addSystem(i18n.T("picker.mcp.no_servers"))
		return
	}
	p := &a.mcpPicker
	closeAllOtherPickers(a, &a.mcpPicker.open)
	p.initPicker(a.width)
	a.mcpPicker.toggling = ""
	a.mcpPicker.lastErr = ""
	if a.mcpPicker.collapsed == nil {
		a.mcpPicker.collapsed = make(map[string]bool)
	}
	refreshMCPPickerEntries(a)
	a.forceRender = true
}

func closeMCPPicker(a *App) {
	a.mcpPicker.close(a)
}

func buildMCPEntries(a *App) []mcpPickerEntry {
	if a.rt.mcpManager == nil {
		return nil
	}
	statuses := a.rt.mcpManager.Status()
	p := &a.mcpPicker
	var entries []mcpPickerEntry
	for _, st := range statuses {
		expanded := !p.collapsed[st.Name]
		connecting := st.Connecting || p.toggling == st.Name
		entries = append(entries, mcpPickerEntry{
			kind:       mcpEntryServer,
			server:     st.Name,
			serverOn:   st.Enabled,
			connected:  st.Connected,
			connecting: connecting,
			toolCount:  st.ToolCount,
			lastErr:    st.LastErr,
			expanded:   expanded && st.Connected,
		})
		if expanded && st.Connected {
			for _, ts := range st.Tools {
				entries = append(entries, mcpPickerEntry{
					kind:     mcpEntryTool,
					server:   st.Name,
					tool:     ts.Name,
					toolOn:   ts.Enabled,
					serverOn: st.Enabled,
				})
			}
		}
	}
	return entries
}

func refreshMCPPickerEntries(a *App) {
	a.mcpPicker.refilter(a, func() []mcpPickerEntry { return buildMCPEntries(a) }, filterMCPEntries)
}

func filterMCPEntries(entries []mcpPickerEntry, query string) []mcpPickerEntry {
	q := normalizeLower(query)
	if q == "" {
		return entries
	}
	var out []mcpPickerEntry
	for _, e := range entries {
		var hit bool
		if e.kind == mcpEntryServer {
			if score, ok := fuzzyScore(e.server, "", q); ok && score > 0 {
				hit = true
			}
		} else {
			if score, ok := fuzzyScore(e.tool, e.server, q); ok && score > 0 {
				hit = true
			}
		}
		if hit {
			out = append(out, e)
		}
	}
	return out
}

func toggleMCPServer(a *App) {
	p := &a.mcpPicker
	if len(p.entries) == 0 || a.rt.mcpManager == nil {
		return
	}
	if p.selIdx >= len(p.entries) || p.entries[p.selIdx].kind != mcpEntryServer {
		return
	}
	if a.status != statusIdle {
		a.addSystem(i18n.T("picker.mcp.busy_server"))
		return
	}
	if p.toggling != "" {
		return
	}
	sel := p.entries[p.selIdx]
	newState := !sel.serverOn
	parent := a.rt.ctx
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithCancel(parent)
	p.toggling = sel.server
	p.toggleCancel = cancel
	p.lastErr = ""
	refreshMCPPickerEntries(a)
	a.forceRender = true
	mgr := a.rt.mcpManager
	sender := a.rt.sender
	server := sel.server
	// Only the network I/O (SetEnabled) runs off the event loop. The agent rebuild — which mutates
	// App state via publishSwitchAgent — is deferred to applyMCPToggleDone on the event-loop
	// goroutine, preserving the single-owner invariant for App state (no cross-goroutine mutation).
	go func() {
		err := mgr.SetEnabled(ctx, server, newState)
		if sender != nil {
			sender(mcpToggleDoneMsg{server: server, enabled: newState, err: err})
		}
	}()
}

type mcpToggleDoneMsg struct {
	server  string
	enabled bool
	err     error
}

func applyMCPToggleDone(a *App, msg mcpToggleDoneMsg) {
	p := &a.mcpPicker
	if p.toggling == msg.server {
		p.toggling = ""
		p.toggleCancel = nil
	}
	if msg.err != nil {
		p.lastErr = fmt.Sprintf(i18n.T("picker.mcp.server_err"), msg.server, msg.err)
		a.addSystem(fmt.Sprintf(i18n.T("picker.mcp.toggle_failed"), msg.server, msg.err))
	} else {
		// Rebuild the agent's tool set here on the event-loop goroutine (publishSwitchAgent mutates
		// App state and must not run from the toggle goroutine). Runs for both enable and disable:
		// EnabledTools() is only consulted at rebuild time, so without this a disabled server's tools
		// would linger in the running agent.
		if a.rt.rebuildTools != nil {
			if rerr := a.rt.rebuildTools(); rerr != nil {
				p.lastErr = fmt.Sprintf(i18n.T("picker.mcp.rebuild_err"), rerr)
				a.addSystem(fmt.Sprintf(i18n.T("picker.mcp.rebuild_failed"), rerr))
			}
		}
		if msg.enabled {
			a.addSystem(fmt.Sprintf(i18n.T("picker.mcp.server_enabled"), msg.server))
		} else {
			a.addSystem(fmt.Sprintf(i18n.T("picker.mcp.server_disabled"), msg.server))
		}
	}
	refreshMCPPickerEntries(a)
	a.forceRender = true
}

func cancelMCPToggle(a *App) {
	p := &a.mcpPicker
	if p.toggleCancel != nil {
		p.toggleCancel()
	}
}

func toggleMCPTool(a *App) {
	p := &a.mcpPicker
	if len(p.entries) == 0 || a.rt.mcpManager == nil {
		return
	}
	if p.selIdx >= len(p.entries) || p.entries[p.selIdx].kind != mcpEntryTool {
		return
	}
	if a.status != statusIdle {
		a.addSystem(i18n.T("picker.mcp.busy_tool"))
		return
	}
	sel := p.entries[p.selIdx]
	if !sel.serverOn {
		return
	}
	newState := !sel.toolOn
	if err := a.rt.mcpManager.SetToolEnabled(context.Background(), sel.server, sel.tool, newState); err != nil {
		p.lastErr = fmt.Sprintf(i18n.T("picker.mcp.tool_err"), sel.server, sel.tool, err)
		a.addSystem(fmt.Sprintf(i18n.T("picker.mcp.tool_toggle_failed"), sel.server, sel.tool, err))
		refreshMCPPickerEntries(a)
		a.forceRender = true
		return
	}
	if a.rt.rebuildTools != nil {
		if rerr := a.rt.rebuildTools(); rerr != nil {
			p.lastErr = fmt.Sprintf(i18n.T("picker.mcp.rebuild_err"), rerr)
			a.addSystem(fmt.Sprintf(i18n.T("picker.mcp.rebuild_failed"), rerr))
		}
	}
	if newState {
		a.addSystem(fmt.Sprintf(i18n.T("picker.mcp.tool_enabled"), sel.server, sel.tool))
	} else {
		a.addSystem(fmt.Sprintf(i18n.T("picker.mcp.tool_disabled"), sel.server, sel.tool))
	}
	refreshMCPPickerEntries(a)
	a.forceRender = true
}

func toggleMCPCollapse(a *App) {
	p := &a.mcpPicker
	if len(p.entries) == 0 || p.selIdx >= len(p.entries) {
		return
	}
	sel := p.entries[p.selIdx]
	if sel.kind != mcpEntryServer {
		return
	}
	if p.collapsed == nil {
		p.collapsed = make(map[string]bool)
	}
	if p.collapsed[sel.server] {
		delete(p.collapsed, sel.server)
	} else {
		p.collapsed[sel.server] = true
	}
	refreshMCPPickerEntries(a)
	a.forceRender = true
}

func (*mcpPickerState) onKey(a *App, k Key, ch rune) {
	p := &a.mcpPicker
	switch k {
	case KeyEsc, KeyCtrlP:
		if p.toggling != "" {
			cancelMCPToggle(a)
			return
		}
		closeMCPPicker(a)
		return
	case KeyCtrlC:
		if p.toggling != "" {
			cancelMCPToggle(a)
			return
		}
		closeMCPPicker(a)
		return
	case KeyEnter:
		toggleMCPSelection(a)
		return
	case KeyTab:
		toggleMCPCollapse(a)
		return
	}
	if k == KeyRune && ch == ' ' {
		toggleMCPSelection(a)
		return
	}
	p.handleKey(a, k, ch, nil, func() { refreshMCPPickerEntries(a) })
}

func toggleMCPSelection(a *App) {
	p := &a.mcpPicker
	if len(p.entries) == 0 || p.selIdx >= len(p.entries) {
		return
	}
	if p.entries[p.selIdx].kind == mcpEntryTool {
		toggleMCPTool(a)
		return
	}
	toggleMCPServer(a)
}

func (*mcpPickerState) render(a *App) {
	a.mcpPicker.draw(a, mcpRenderer{})
}

type mcpRenderer struct{}

func (mcpRenderer) title() string       { return i18n.T("picker.mcp.title") }
func (mcpRenderer) placeholder() string { return i18n.T("picker.mcp.placeholder") }
func (mcpRenderer) footer(a *App) string {
	p := &a.mcpPicker
	if p.lastErr != "" {
		return p.lastErr
	}
	if p.toggling != "" {
		return i18n.T("picker.mcp.footer.connecting")
	}
	return i18n.T("picker.mcp.footer.idle")
}
func (mcpRenderer) queryPrompt(a *App) (string, int) { return "> ", 2 }
func (mcpRenderer) cursor(a *App, innerX, queryY, promptWidth int) (int, int, bool) {
	return pickerQueryCursor(&a.mcpPicker.query, innerX, queryY, promptWidth)
}
func (mcpRenderer) drawItem(a *App, it mcpPickerEntry, selected bool, innerX, ry, innerW int) {
	const nameCol = 16
	statusW := innerW - 2 - nameCol - 1
	if statusW < 8 {
		statusW = 8
	}
	nameStyle := stylePaletteItem()
	statusStyle := stylePaletteItemDesc()
	if selected {
		nameStyle = stylePaletteSelected()
		statusStyle = stylePaletteSelectedDesc()
	}
	marker := "  "
	if selected {
		marker = "> "
	}
	drawTextRaw(a.screen, a, innerX, ry, 2, marker, nameStyle)
	if it.kind == mcpEntryServer {
		collapse := "-"
		if !it.expanded {
			collapse = "+"
		}
		drawTextRaw(a.screen, a, innerX+2, ry, nameCol, truncateStr(collapse+" "+it.server, nameCol), nameStyle)
		status := mcpStatusText(it, a.mcpPicker.toggling == it.server, a.spinnerFrame)
		stX := innerX + 2 + nameCol + 1
		drawTextRaw(a.screen, a, stX, ry, statusW, truncateStr(status, statusW), statusStyle)
	} else {
		toolStyle := nameStyle
		if !it.serverOn {
			toolStyle = styleToolDim()
		}
		toggle := "○"
		if it.toolOn {
			toggle = "✓"
		}
		drawTextRaw(a.screen, a, innerX+2, ry, innerW-2, truncateStr("  "+toggle+" "+it.tool, innerW-2), toolStyle)
	}
}

func mcpStatusText(it mcpPickerEntry, toggling bool, spinnerFrame int) string {
	if toggling || it.connecting {
		return spinnerFrameStr(spinnerFrame) + " " + i18n.T("picker.mcp.connecting")
	}
	if it.lastErr != "" {
		return "! " + fmt.Sprintf(i18n.T("picker.mcp.failed_n_tools"), it.toolCount)
	}
	if !it.connected {
		return "○ " + i18n.T("picker.mcp.disabled")
	}
	if it.serverOn {
		return "✓ " + fmt.Sprintf(i18n.T("picker.mcp.enabled_n_tools"), it.toolCount)
	}
	return "○ " + fmt.Sprintf(i18n.T("picker.mcp.disabled_n_tools"), it.toolCount)
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
