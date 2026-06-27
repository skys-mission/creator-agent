package tui

// mode_picker_test.go covers the /mode overlay: opening the picker, /mode <name> direct switch,
// alias normalization, picker commit, the already-on hint, and the disabled (nil controller) guard.

import (
	"strings"
	"testing"

	"github.com/skys-mission/creator-agent/core/middlewares"
)

func newAppWithMode(t *testing.T, initial string) *App {
	t.Helper()
	a, _ := newAppWithSim(t, 80, 24)
	a.rt.modeCtl = middlewares.NewModeController(middlewares.Mode(initial))
	a.rt.currentMode = string(middlewares.NormalizeMode(middlewares.Mode(initial)))
	return a
}

// TestModeCommandNoArgOpensPicker verifies /mode lists the four fixed modes.
func TestModeCommandNoArgOpensPicker(t *testing.T) {
	a := newAppWithMode(t, "default")
	if !handleSlashCommand(a, "/mode") {
		t.Fatalf("/mode should be handled")
	}
	if !a.modePicker.open {
		t.Fatalf("/mode should open the picker")
	}
	if len(a.modePicker.entries) != 4 {
		t.Fatalf("entries = %d, want 4 modes: %+v", len(a.modePicker.entries), a.modePicker.entries)
	}
}

// TestModesPickerCommand verifies /modes opens the picker too.
func TestModesPickerCommand(t *testing.T) {
	a := newAppWithMode(t, "default")
	if !handleSlashCommand(a, "/modes") {
		t.Fatalf("/modes should be handled")
	}
	if !a.modePicker.open {
		t.Fatalf("/modes should open the picker")
	}
}

// TestModeCommandWithArgSwitches verifies /mode <name> switches both the display cache and controller.
func TestModeCommandWithArgSwitches(t *testing.T) {
	a := newAppWithMode(t, "default")
	if !handleSlashCommand(a, "/mode auto") {
		t.Fatalf("/mode auto should be handled")
	}
	if a.modePicker.open {
		t.Errorf("arg form should not open the picker")
	}
	if a.rt.currentMode != "auto" {
		t.Errorf("currentMode = %q, want auto", a.rt.currentMode)
	}
	if a.rt.modeCtl.Get() != middlewares.ModeAuto {
		t.Errorf("controller = %q, want auto", a.rt.modeCtl.Get())
	}
}

// TestModeCommandAliasNormalizes verifies aliases are normalized before switching.
func TestModeCommandAliasNormalizes(t *testing.T) {
	cases := map[string]string{
		"read-only": "readonly",
		"YOLO":      "auto",
		"trusted":   "trust",
		"safe":      "readonly",
	}
	for in, want := range cases {
		a := newAppWithMode(t, "default")
		handleSlashCommand(a, "/mode "+in)
		if a.rt.currentMode != want {
			t.Errorf("/mode %q -> currentMode = %q, want %q", in, a.rt.currentMode, want)
		}
	}
}

// TestModePickerCommitSwitches verifies selecting a row in the picker switches mode.
func TestModePickerCommitSwitches(t *testing.T) {
	a := newAppWithMode(t, "default")
	openModePicker(a)
	// entries order: default(0), trust(1), auto(2), readonly(3) — move to trust.
	injectKey(a, KeyDown)
	if got := a.modePicker.entries[a.modePicker.selIdx].name; got != "trust" {
		t.Fatalf("precondition: trust should be selected after Down; got %q", got)
	}
	commitModePicker(a)
	if a.rt.currentMode != "trust" {
		t.Errorf("commit: currentMode = %q, want trust", a.rt.currentMode)
	}
	if a.rt.modeCtl.Get() != middlewares.ModeTrust {
		t.Errorf("commit: controller = %q, want trust", a.rt.modeCtl.Get())
	}
	if a.modePicker.open {
		t.Errorf("picker should be closed after commit")
	}
}

// TestModeSwitchAlreadyHint verifies switching to the current mode shows an "already" hint.
func TestModeSwitchAlreadyHint(t *testing.T) {
	a := newAppWithMode(t, "auto")
	handleSlashCommand(a, "/mode auto")
	last := a.messages[len(a.messages)-1].content.String()
	if !strings.Contains(strings.ToLower(last), "already") {
		t.Errorf("expected an 'already' hint; got %q", last)
	}
}

// TestModePickerDisabledWhenControllerNil verifies the guard when mode features are off.
func TestModePickerDisabledWhenControllerNil(t *testing.T) {
	a, _ := newAppWithSim(t, 80, 24) // a.rt.modeCtl is nil
	if handleSlashCommand(a, "/mode") {
		// handled=true is fine (it produced a message), but picker must not open.
	}
	if a.modePicker.open {
		t.Errorf("picker should not open when controller is nil")
	}
	last := a.messages[len(a.messages)-1].content.String()
	if !strings.Contains(strings.ToLower(last), "not enabled") {
		t.Errorf("expected a disabled hint; got %q", last)
	}
}

// TestModePickerCurrentMarked verifies the current mode is preselected in the picker.
func TestModePickerCurrentMarked(t *testing.T) {
	a := newAppWithMode(t, "auto")
	openModePicker(a)
	if got := a.modePicker.entries[a.modePicker.selIdx].name; got != "auto" {
		t.Errorf("current mode auto should be preselected; got %q", got)
	}
}

// TestCyclePermissionModeOrder verifies the cycle wraps default→trust→auto→readonly→default
// and keeps the display cache in sync with the controller.
func TestCyclePermissionModeOrder(t *testing.T) {
	a := newAppWithMode(t, "default")

	want := []string{"trust", "auto", "readonly", "default"}
	for _, w := range want {
		cyclePermissionMode(a)
		if a.rt.currentMode != w {
			t.Errorf("after cycle: currentMode = %q, want %q", a.rt.currentMode, w)
		}
		if a.rt.modeCtl.Get() != middlewares.Mode(w) {
			t.Errorf("after cycle: controller = %q, want %q (out of sync with display)", a.rt.modeCtl.Get(), w)
		}
	}
}

// TestCyclePermissionModeNilNoOp verifies the cycle is a safe no-op when mode features are off.
func TestCyclePermissionModeNilNoOp(t *testing.T) {
	a, _ := newAppWithSim(t, 80, 24) // a.rt.modeCtl is nil
	cyclePermissionMode(a)           // must not panic
	if a.rt.currentMode != "" {
		t.Errorf("nil controller should leave currentMode empty, got %q", a.rt.currentMode)
	}
}

// TestCyclePermissionModeViaShiftTab verifies the key binding reaches the cycle.
func TestCyclePermissionModeViaShiftTab(t *testing.T) {
	a := newAppWithMode(t, "default")
	injectKey(a, KeyBacktab)
	if a.rt.currentMode != "trust" {
		t.Errorf("Shift+Tab should cycle default→trust, got %q", a.rt.currentMode)
	}
}
