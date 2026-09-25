package tui

// model_menu_test.go covers the /model panel: the second-level menu (New / Manage), the model
// list, and the delete flow with its confirm step.

import (
	"fmt"
	"strings"
	"testing"

	"github.com/skys-mission/creator-agent/cmd/creator-agent/tui/i18n"
	"github.com/skys-mission/creator-agent/contract"
)

// seedModels writes n dummy models into the store (fake keys only — never real secrets).
func seedModels(t *testing.T, a *App, names ...string) {
	t.Helper()
	for i, n := range names {
		m := contract.Model{
			Name:     n,
			Protocol: contract.ProtocolOpenAIChat,
			BaseURL:  "https://example.com/v1",
			ModelID:  "m-" + n,
			APIKey:   "sk-test-fake000" + string(rune('0'+i)),
		}
		if _, err := a.models.add(m); err != nil {
			t.Fatalf("seed add: %v", err)
		}
	}
}

// openMenuAtManage opens /model and walks to the manage (model list) view.
func openMenuAtManage(t *testing.T, a *App) {
	t.Helper()
	openModelMenu(a)
	a.modelMenu.sel = modelMenuIdxManage
	injectKey(a, KeyEnter)
	if a.modelMenu.view != modelMenuManage {
		t.Fatalf("view = %v, want manage (err=%q)", a.modelMenu.view, a.modelMenu.err)
	}
}

func TestModelMenuOpensFromCommand(t *testing.T) {
	a, sim := newAppWithSim(t, 80, 24)
	a.input.SetValue("/model")
	submitInput(a)
	if !a.modelMenu.open || a.modelMenu.view != modelMenuRoot {
		t.Fatalf("menu not at root: open=%v view=%v", a.modelMenu.open, a.modelMenu.view)
	}
	render(a)
	dump := screenDump(sim)
	if !strings.Contains(dump, i18nT("model_menu.title")) || !strings.Contains(dump, i18nT("model_menu.item.new")) {
		t.Fatalf("root menu missing title/items:\n%s", dump)
	}
}

func TestModelMenuRootNavigation(t *testing.T) {
	a, _ := newAppWithSim(t, 80, 24)
	openModelMenu(a)
	if a.modelMenu.sel != modelMenuIdxNew {
		t.Fatalf("sel = %d, want New first", a.modelMenu.sel)
	}
	injectKey(a, KeyUp) // clamp at top
	if a.modelMenu.sel != modelMenuIdxNew {
		t.Fatalf("sel = %d after Up at top, want %d", a.modelMenu.sel, modelMenuIdxNew)
	}
	injectKey(a, KeyDown)
	if a.modelMenu.sel != modelMenuIdxManage {
		t.Fatalf("sel = %d after Down, want Manage", a.modelMenu.sel)
	}
	injectKey(a, KeyDown) // clamp at bottom
	if a.modelMenu.sel != modelMenuIdxManage {
		t.Fatalf("sel = %d after Down at bottom, want Manage", a.modelMenu.sel)
	}
}

func TestModelMenuNewOpensFormStacked(t *testing.T) {
	a, _ := newAppWithSim(t, 80, 24)
	openModelMenu(a)
	injectKey(a, KeyEnter) // "New model…"
	if !a.modelForm.open {
		t.Fatal("Enter on New did not open the create form")
	}
	injectKey(a, KeyEsc) // form cancel returns to the panel
	if a.modelForm.open {
		t.Fatal("Esc did not close the form")
	}
	if !a.modelMenu.open || a.modelMenu.view != modelMenuRoot {
		t.Fatalf("panel should still be at root after form cancel: open=%v view=%v", a.modelMenu.open, a.modelMenu.view)
	}
}

func TestModelMenuManageListsModels(t *testing.T) {
	a, sim := newAppWithSim(t, 80, 24)
	seedModels(t, a, "alpha", "beta")
	openMenuAtManage(t, a)
	render(a)
	dump := screenDump(sim)
	if !strings.Contains(dump, "alpha") || !strings.Contains(dump, "beta") {
		t.Fatalf("manage list missing models:\n%s", dump)
	}
	if strings.Contains(dump, "sk-test-") {
		t.Fatalf("manage list leaked an API key:\n%s", dump)
	}
}

func TestModelManageDeleteFlow(t *testing.T) {
	a, sim := newAppWithSim(t, 80, 24)
	seedModels(t, a, "alpha", "beta")
	openMenuAtManage(t, a)

	// Esc from confirm cancels without touching the store.
	injectRune(a, 'd')
	if a.modelMenu.view != modelMenuConfirm {
		t.Fatalf("d did not ask for confirm; view=%v", a.modelMenu.view)
	}
	injectKey(a, KeyEsc)
	if a.modelMenu.view != modelMenuManage {
		t.Fatalf("Esc did not return to manage; view=%v", a.modelMenu.view)
	}
	if got := a.models.load(); len(got) != 2 {
		t.Fatalf("cancel deleted a model: %+v", got)
	}

	// Enter from confirm deletes the selected row and lands back on the list.
	injectKey(a, KeyEnter) // -> confirm (on "alpha")
	injectKey(a, KeyEnter) // -> delete
	if a.modelMenu.view != modelMenuManage {
		t.Fatalf("delete did not return to manage; view=%v err=%q", a.modelMenu.view, a.modelMenu.err)
	}
	got := a.models.load()
	if len(got) != 1 || got[0].Name != "beta" {
		t.Fatalf("after delete models = %+v, want only beta", got)
	}
	if !strings.Contains(a.notice, "alpha") {
		t.Fatalf("notice %q should name the deleted model", a.notice)
	}

	// The panel covers the main notice line, so the result must also render inside the panel.
	want := fmt.Sprintf(i18n.T("model_menu.deleted"), "alpha", 1)
	render(a)
	if dump := screenDump(sim); !strings.Contains(dump, want) {
		t.Fatalf("no in-panel delete feedback %q:\n%s", want, dump)
	}
	// And it must go away on the next navigation (a stale "deleted" line next to the list is a lie).
	injectKey(a, KeyDown)
	render(a)
	if dump := screenDump(sim); strings.Contains(dump, want) {
		t.Fatalf("delete flash lingered after navigation:\n%s", dump)
	}
}

func TestModelManageDeleteClampsSelection(t *testing.T) {
	a, _ := newAppWithSim(t, 80, 24)
	seedModels(t, a, "alpha", "beta")
	openMenuAtManage(t, a)
	injectKey(a, KeyDown) // select the last row
	injectKey(a, KeyEnter)
	injectKey(a, KeyEnter) // delete "beta"
	if a.modelMenu.sel != 0 {
		t.Fatalf("sel = %d after deleting the last row, want clamped to 0", a.modelMenu.sel)
	}
	if a.modelMenu.view != modelMenuConfirm {
		// Selected row is now "alpha"; a fresh delete must still be confirmed.
		injectRune(a, 'd')
	}
	if a.modelMenu.view != modelMenuConfirm {
		t.Fatalf("view = %v, want confirm", a.modelMenu.view)
	}
}

func TestModelManageDeleteEmptyNoop(t *testing.T) {
	a, _ := newAppWithSim(t, 80, 24)
	openMenuAtManage(t, a) // empty store
	injectRune(a, 'd')
	if a.modelMenu.view == modelMenuConfirm {
		t.Fatal("d on an empty list must be a no-op, not open confirm")
	}
	injectKey(a, KeyEnter) // same guard on Enter
	if a.modelMenu.view == modelMenuConfirm {
		t.Fatal("Enter on an empty list must be a no-op, not open confirm")
	}
}

func TestModelMenuCreateFlashesInPanel(t *testing.T) {
	// The create form stacks over the panel; after a successful create the result must be
	// visible inside the panel (the main notice line is covered), then persist as a notice.
	a, sim := newModelFormApp(t)
	openModelMenu(a)
	injectKey(a, KeyEnter) // "New model…" -> stacked form
	if !a.modelForm.open {
		t.Fatal("New model… did not open the create form")
	}
	a.modelForm.focus = formFieldModelID
	typeText(a, "flash-m1")
	injectKey(a, KeyEnter) // -> confirm
	injectKey(a, KeyEnter) // -> create
	if !a.modelMenu.open {
		t.Fatal("panel should stay open underneath the closed form")
	}
	want := fmt.Sprintf(i18n.T("model_form.created"), "flash-m1", modelStorePath(a), 1)
	if a.modelMenu.flash != want {
		t.Fatalf("flash = %q, want %q", a.modelMenu.flash, want)
	}
	if a.notice != want {
		t.Fatalf("notice = %q, want the same result kept for when the panel closes", a.notice)
	}
	render(a)
	if dump := screenDump(sim); !strings.Contains(dump, "flash-m1") {
		t.Fatalf("created result not rendered inside the panel:\n%s", dump)
	}
}

func TestModelMenuEscClosesFromRoot(t *testing.T) {
	a, _ := newAppWithSim(t, 80, 24)
	openModelMenu(a)
	injectKey(a, KeyEsc)
	if a.modelMenu.open {
		t.Fatal("Esc at root did not close the panel")
	}
}

func TestModelMenuCtrlDQuits(t *testing.T) {
	a, _ := newAppWithSim(t, 80, 24)
	openModelMenu(a)
	injectKey(a, KeyCtrlD) // the panel must never trap the exit
	if !a.quitting {
		t.Fatal("Ctrl+D should quit from the panel")
	}
}

// i18nT is a tiny alias so the render assertions read cleanly.
func i18nT(key string) string { return i18n.T(key) }
