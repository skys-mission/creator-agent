package tui

// theme_picker_test.go covers the live-preview theme picker lifecycle: open snapshots the committed
// theme, navigation hot-swaps the palette (preview), Enter commits, Esc reverts. Global theme state
// is saved/restored per test so the suite is order-independent.

import (
	"testing"
)

// newAppForTheme builds a sim App for theme-picker tests.
func newAppForTheme(t *testing.T) *App {
	t.Helper()
	a, _ := newAppWithSim(t, 80, 24)
	return a
}

// TestThemePickerOpenSnapshotsAndPreselects verifies opening the picker captures the committed theme
// snapshot and preselects the current theme.
func TestThemePickerOpenSnapshotsAndPreselects(t *testing.T) {
	savedName, savedPal := snapshotTheme()
	t.Cleanup(func() { restoreTheme(savedName, savedPal) })
	ApplyTheme("dark")

	a := newAppForTheme(t)
	openThemePicker(a)
	if !a.themePicker.open {
		t.Fatalf("picker should be open")
	}
	if a.themePicker.savedName != "dark" || a.themePicker.savedPal == nil {
		t.Errorf("snapshot = (%q, nil=%v), want (dark, false)", a.themePicker.savedName, a.themePicker.savedPal == nil)
	}
	// The current theme (dark) should be preselected.
	if len(a.themePicker.entries) == 0 || a.themePicker.entries[a.themePicker.selIdx].name != "dark" {
		t.Errorf("current theme should be preselected; selIdx=%d entries=%+v", a.themePicker.selIdx, a.themePicker.entries)
	}
}

// TestThemePickerDownPreviews verifies moving down hot-swaps the palette + name to the other theme
// (live preview) without committing.
func TestThemePickerDownPreviews(t *testing.T) {
	savedName, savedPal := snapshotTheme()
	t.Cleanup(func() { restoreTheme(savedName, savedPal) })
	ApplyTheme("dark")

	a := newAppForTheme(t)
	openThemePicker(a)
	injectKey(a, KeyDown)
	// Preview should now be the non-current theme (light, the only other entry).
	if CurrentTheme() != "light" {
		t.Errorf("after Down, preview theme = %q, want light", CurrentTheme())
	}
	if palettePtr.Load().mainBg == savedPal.mainBg {
		t.Errorf("after Down, palette should have changed to light")
	}
}

// TestThemePickerEnterCommits verifies Enter commits the previewed theme (snapshot cleared, theme
// stays after close).
func TestThemePickerEnterCommits(t *testing.T) {
	savedName, savedPal := snapshotTheme()
	t.Cleanup(func() { restoreTheme(savedName, savedPal) })
	ApplyTheme("dark")

	a := newAppForTheme(t)
	openThemePicker(a)
	injectKey(a, KeyDown)  // preview light
	injectKey(a, KeyEnter) // commit
	if a.themePicker.open {
		t.Errorf("Enter should close the picker")
	}
	if CurrentTheme() != "light" {
		t.Errorf("after commit, theme = %q, want light", CurrentTheme())
	}
	if a.themePicker.savedName != "" || a.themePicker.savedPal != nil {
		t.Errorf("commit should clear the snapshot; got savedName=%q savedPal-nil=%v", a.themePicker.savedName, a.themePicker.savedPal == nil)
	}
}

// TestThemePickerEscReverts verifies Esc reverts the preview to the snapshotted theme.
func TestThemePickerEscReverts(t *testing.T) {
	savedName, savedPal := snapshotTheme()
	t.Cleanup(func() { restoreTheme(savedName, savedPal) })
	ApplyTheme("dark")

	a := newAppForTheme(t)
	openThemePicker(a)
	injectKey(a, KeyDown) // preview light
	if CurrentTheme() != "light" {
		t.Fatalf("precondition: preview should be light; got %q", CurrentTheme())
	}
	injectKey(a, KeyEsc)
	if a.themePicker.open {
		t.Errorf("Esc should close the picker")
	}
	if CurrentTheme() != "dark" {
		t.Errorf("after Esc, theme = %q, want dark (reverted to snapshot)", CurrentTheme())
	}
	if palettePtr.Load().mainBg != savedPal.mainBg {
		t.Errorf("after Esc, palette should be restored to the snapshot")
	}
}

// TestThemePickerEnterOnCurrentKeepsTheme verifies committing the already-current theme is a no-op
// (theme unchanged, picker closes).
func TestThemePickerEnterOnCurrentKeepsTheme(t *testing.T) {
	savedName, savedPal := snapshotTheme()
	t.Cleanup(func() { restoreTheme(savedName, savedPal) })
	ApplyTheme("dark")

	a := newAppForTheme(t)
	openThemePicker(a)
	// Don't move — current (dark) is preselected.
	injectKey(a, KeyEnter)
	if CurrentTheme() != "dark" {
		t.Errorf("committing the current theme should keep it; got %q", CurrentTheme())
	}
	if a.themePicker.open {
		t.Errorf("Enter should close the picker")
	}
}

// TestThemePickerSlashThemesOpens verifies /themes (no arg) opens the picker and /themes <name>
// still sets directly (without opening).
func TestThemePickerSlashThemesOpens(t *testing.T) {
	savedName, savedPal := snapshotTheme()
	t.Cleanup(func() { restoreTheme(savedName, savedPal) })

	t.Run("no_arg_opens", func(t *testing.T) {
		ApplyTheme("dark")
		a := newAppForTheme(t)
		if !handleSlashCommand(a, "/themes") {
			t.Fatalf("/themes should be handled")
		}
		if !a.themePicker.open {
			t.Errorf("bare /themes should open the picker")
		}
	})

	t.Run("named_arg_sets_directly", func(t *testing.T) {
		ApplyTheme("dark")
		a := newAppForTheme(t)
		if !handleSlashCommand(a, "/themes light") {
			t.Fatalf("/themes light should be handled")
		}
		if a.themePicker.open {
			t.Errorf("/themes <name> should NOT open the picker")
		}
		if CurrentTheme() != "light" {
			t.Errorf("/themes light should set directly; got %q", CurrentTheme())
		}
	})
}
