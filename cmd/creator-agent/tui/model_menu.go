package tui

// The /model panel: the single first-level command opens this second-level menu, which owns all
// model-object management — "New model…" (the create form, stacked over this panel) and "Manage
// models…" (list + delete). Deletion is destructive: the model's API key exists only in the store
// file and dies with the entry, so it takes an explicit confirm step.
//
// Layout contract with model_menu_view.go: the view only reads this state; all mutation happens
// here in the event-loop goroutine (App state discipline).

import (
	"fmt"

	"github.com/skys-mission/creator-agent/cmd/creator-agent/tui/i18n"
	"github.com/skys-mission/creator-agent/contract"
)

// modelMenuView enumerates the screens of the panel.
type modelMenuView int

const (
	modelMenuRoot    modelMenuView = iota // second-level menu: New / Manage
	modelMenuManage                       // model list + delete
	modelMenuConfirm                      // delete confirmation
)

// Second-level menu items (root view), top to bottom.
const (
	modelMenuIdxNew = iota
	modelMenuIdxManage
	modelMenuItemCount
)

// modelMenuState is the /model panel state.
type modelMenuState struct {
	open bool
	view modelMenuView
	sel  int // root: menu item index; manage/confirm: model row index
	err  string
	// flash is a success message shown in-panel (e.g. after a delete). It lives here, not in
	// App.notice, because the panel covers the main notice line — the feedback must appear
	// where the user is looking. Cleared on navigation.
	flash string

	list []contract.Model // store snapshot while in the manage/confirm views
}

// openModelMenu opens the panel at its second-level menu.
func openModelMenu(a *App) {
	a.modelMenu = modelMenuState{open: true}
	a.forceRender = true
}

// closeModelMenu dismisses the whole panel.
func closeModelMenu(a *App) {
	a.modelMenu = modelMenuState{}
	a.forceRender = true
}

// refreshModelMenuList reloads the store snapshot behind the manage/confirm views and clamps the
// selection (the list shrinks on every delete).
func refreshModelMenuList(a *App) {
	a.modelMenu.list = a.models.load()
	if a.modelMenu.sel >= len(a.modelMenu.list) {
		a.modelMenu.sel = maxi(0, len(a.modelMenu.list)-1)
	}
}

// handleModelMenuKey routes keys to the panel by view. Ctrl+D always quits (the panel can never
// trap the exit); Ctrl+C/Esc back out one level (root: close the panel).
func handleModelMenuKey(a *App, e *EventKey) {
	k := e.Key()
	if k == KeyCtrlD {
		a.quitting = true
		return
	}
	switch a.modelMenu.view {
	case modelMenuRoot:
		handleModelMenuRootKey(a, k)
	case modelMenuManage:
		handleModelMenuManageKey(a, k, e.Rune())
	case modelMenuConfirm:
		handleModelMenuConfirmKey(a, k)
	}
}

// handleModelMenuRootKey drives the second-level menu: ↑↓/Tab move, Enter opens, Esc closes.
func handleModelMenuRootKey(a *App, k Key) {
	m := &a.modelMenu
	switch k {
	case KeyEsc, KeyCtrlC:
		closeModelMenu(a)
	case KeyUp, KeyBacktab:
		if m.sel > 0 {
			m.sel--
		}
		m.flash = ""
	case KeyDown, KeyTab:
		if m.sel < modelMenuItemCount-1 {
			m.sel++
		}
		m.flash = ""
	case KeyEnter:
		if m.sel == modelMenuIdxNew {
			// Stacked over the panel: Esc on the form lands back on this menu.
			openModelForm(a)
			return
		}
		m.view = modelMenuManage
		m.sel = 0
		m.err = ""
		refreshModelMenuList(a)
		a.forceRender = true
	}
}

// handleModelMenuManageKey drives the model list: ↑↓ move, Enter/d/Delete ask for a delete
// confirm, Esc backs out to the second-level menu.
func handleModelMenuManageKey(a *App, k Key, r rune) {
	m := &a.modelMenu
	switch k {
	case KeyEsc, KeyCtrlC:
		m.view = modelMenuRoot
		m.sel = 0
		m.err = ""
		a.forceRender = true
	case KeyUp:
		if m.sel > 0 {
			m.sel--
		}
		m.flash = ""
	case KeyDown:
		if m.sel < len(m.list)-1 {
			m.sel++
		}
		m.flash = ""
	case KeyEnter, KeyDelete:
		if len(m.list) == 0 {
			return
		}
		m.view = modelMenuConfirm
		m.err = ""
		m.flash = ""
		a.forceRender = true
	case KeyRune:
		if (r == 'd' || r == 'D') && len(m.list) > 0 {
			m.view = modelMenuConfirm
			m.err = ""
			m.flash = ""
			a.forceRender = true
		}
	}
}

// handleModelMenuConfirmKey drives the delete confirmation: Enter deletes and returns to the
// list, Esc cancels without touching the store.
func handleModelMenuConfirmKey(a *App, k Key) {
	m := &a.modelMenu
	switch k {
	case KeyEsc, KeyCtrlC:
		m.view = modelMenuManage
		m.err = ""
		a.forceRender = true
	case KeyEnter:
		name := m.list[m.sel].DisplayName()
		n, err := a.models.removeAt(m.sel)
		if err != nil {
			m.err = fmt.Sprintf(i18n.T("model_menu.err.delete"), err)
			a.forceRender = true
			return
		}
		m.view = modelMenuManage
		m.err = ""
		refreshModelMenuList(a)
		// Feedback in two places: the in-panel flash (visible now) and the main notice line
		// (still there after the panel closes).
		m.flash = fmt.Sprintf(i18n.T("model_menu.deleted"), name, n)
		a.notice = m.flash
		a.forceRender = true
	}
}
