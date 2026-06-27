package tui

// whichkey_test.go covers the Ctrl+Alt+K keybindings panel: open/close, toggle behavior, the
// content derives from the keymap registry, Esc/q close, and the keymap helpers.

import (
	"strings"
	"testing"
)

// injectCtrlAltK sends a Ctrl+Alt+K event through the normal dispatch path (the KeyCtrlK form the
// decoder produces from ESC+0x0b).
func injectCtrlAltK(a *App) {
	handleTcellEvent(a, NewEventKey(KeyCtrlK, 0, ModCtrl|ModAlt))
}

// TestWhichKeyOpenClose verifies Ctrl+Alt+K opens the panel and Esc closes it.
func TestWhichKeyOpenClose(t *testing.T) {
	a, _ := newAppWithSim(t, 80, 24)
	if a.whichKey.open {
		t.Fatalf("panel should start closed")
	}
	injectCtrlAltK(a)
	if !a.whichKey.open {
		t.Fatalf("Ctrl+Alt+K should open the panel")
	}
	// Esc closes.
	injectKey(a, KeyEsc)
	if a.whichKey.open {
		t.Fatalf("Esc should close the panel")
	}
}

// TestWhichKeyToggle verifies pressing Ctrl+Alt+K again closes the panel (toggle).
func TestWhichKeyToggle(t *testing.T) {
	a, _ := newAppWithSim(t, 80, 24)
	injectCtrlAltK(a)
	if !a.whichKey.open {
		t.Fatalf("first Ctrl+Alt+K should open")
	}
	injectCtrlAltK(a) // toggle-close
	if a.whichKey.open {
		t.Fatalf("second Ctrl+Alt+K should close the panel (toggle)")
	}
}

// TestWhichKeyQClose verifies q/Q closes the panel (read-only panel, q is a dismiss shortcut).
func TestWhichKeyQClose(t *testing.T) {
	a, _ := newAppWithSim(t, 80, 24)
	openWhichKey(a)
	injectRune(a, 'q')
	if a.whichKey.open {
		t.Errorf("q should close the panel")
	}
	// Capital Q too.
	openWhichKey(a)
	injectRune(a, 'Q')
	if a.whichKey.open {
		t.Errorf("Q should close the panel")
	}
}

// TestWhichKeyScroll verifies Up/Down/PgUp/PgDn/Home/End adjust scrollY within range.
func TestWhichKeyScroll(t *testing.T) {
	a, _ := newAppWithSim(t, 80, 24)
	openWhichKey(a)
	start := a.whichKey.scrollY
	injectKey(a, KeyDown)
	if a.whichKey.scrollY <= start {
		t.Errorf("Down should increase scrollY; %d -> %d", start, a.whichKey.scrollY)
	}
	injectKey(a, KeyHome)
	if a.whichKey.scrollY != 0 {
		t.Errorf("Home should reset scrollY to 0; got %d", a.whichKey.scrollY)
	}
	// Up at top stays 0.
	injectKey(a, KeyUp)
	if a.whichKey.scrollY != 0 {
		t.Errorf("Up at top should keep scrollY 0; got %d", a.whichKey.scrollY)
	}
}

// TestWhichKeyIgnoresOtherRunes verifies typing a non-dismiss rune is ignored (no input mutation).
func TestWhichKeyIgnoresOtherRunes(t *testing.T) {
	a, _ := newAppWithSim(t, 80, 24)
	a.input.SetValue("pre")
	openWhichKey(a)
	injectRune(a, 'x')
	if a.input.Value() != "pre" {
		t.Errorf("panel should ignore rune input; input became %q", a.input.Value())
	}
	if !a.whichKey.open {
		t.Errorf("panel should stay open on ignored rune")
	}
}

// TestKeymapRegistryNonEmpty verifies the registry has content across every declared group, so the
// panel never renders empty.
func TestKeymapRegistryNonEmpty(t *testing.T) {
	for _, g := range keyBindingGroups() {
		bs := bindingsByGroup(g)
		if len(bs) == 0 {
			t.Errorf("group %q has no bindings", g)
		}
	}
	// The toggle binding itself must be documented so users can discover how to close the panel.
	found := false
	for _, b := range keyBindings {
		if strings.Contains(b.key, "Ctrl+Alt+K") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("keymap registry should document the Ctrl+Alt+K toggle")
	}
}
