package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/skys-mission/creator-agent/cmd/creator-agent/tui/i18n"
)

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
