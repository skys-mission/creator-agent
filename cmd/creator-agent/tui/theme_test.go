package tui

// theme_test.go covers runtime theme switching: the dark/light palettes differ, ApplyTheme updates
// currentTheme and the active palette, /themes cycles and sets, and config-driven normalization
// matches the tui package's normalization. Global theme state is saved/restored per test so the
// suite is order-independent.

import (
	"testing"
)

// snapshotTheme captures the current theme+palette so a test can restore it. Theme state is
// package-global; without save/restore, one test's ApplyTheme leaks into every later test.
func snapshotTheme() (string, *palette) {
	p := palettePtr.Load()
	return p.name, p
}

// restoreTheme reverts to the saved theme state. The palette carries its own name
// so only the palette pointer needs to be restored (themeName is no longer a separate variable).
func restoreTheme(name string, p *palette) {
	palettePtr.Store(p)
}

// TestThemeApplyDarkLight verifies ApplyTheme switches the active palette and name, and that the
// two palettes are observably different (not just the same colors).
func TestThemeApplyDarkLight(t *testing.T) {
	savedName, savedPal := snapshotTheme()
	t.Cleanup(func() { restoreTheme(savedName, savedPal) })

	// Start from light.
	if got := ApplyTheme("light"); got != "light" {
		t.Fatalf("ApplyTheme(light) = %q, want light", got)
	}
	if CurrentTheme() != "light" {
		t.Fatalf("CurrentTheme() = %q after ApplyTheme(light)", CurrentTheme())
	}
	lightMainBg := pal().mainBg

	// Switch to dark.
	if got := ApplyTheme("dark"); got != "dark" {
		t.Fatalf("ApplyTheme(dark) = %q, want dark", got)
	}
	if CurrentTheme() != "dark" {
		t.Fatalf("CurrentTheme() = %q after ApplyTheme(dark)", CurrentTheme())
	}
	darkMainBg := pal().mainBg

	// The two palettes must differ (otherwise switching is a no-op and the feature is broken).
	if lightMainBg == darkMainBg {
		t.Fatalf("dark and light mainBg are identical (%v); palettes did not actually differ", lightMainBg)
	}
	// Sanity: the light background should be perceptually lighter (higher channel sum) than dark.
	lr, lg, lb, _ := lightMainBg.RGB()
	dr, dg, db, _ := darkMainBg.RGB()
	if lr+lg+lb <= dr+dg+db {
		t.Fatalf("expected light mainBg brighter than dark; light=%d,%d,%d dark=%d,%d,%d", lr, lg, lb, dr, dg, db)
	}
}

// TestThemeApplyUnknownFallsBackDark verifies an unrecognized theme name falls back to dark and
// does not corrupt state.
func TestThemeApplyUnknownFallsBackDark(t *testing.T) {
	savedName, savedPal := snapshotTheme()
	t.Cleanup(func() { restoreTheme(savedName, savedPal) })

	ApplyTheme("light") // start from light
	if got := ApplyTheme("nonsense"); got != "dark" {
		t.Fatalf("ApplyTheme(nonsense) = %q, want dark fallback", got)
	}
	if CurrentTheme() != "dark" {
		t.Fatalf("CurrentTheme() = %q, want dark after unknown", CurrentTheme())
	}
}

// TestThemeCycleToggle verifies CycleTheme rotates dark -> light -> catppuccin -> dark.
func TestThemeCycleToggle(t *testing.T) {
	savedName, savedPal := snapshotTheme()
	t.Cleanup(func() { restoreTheme(savedName, savedPal) })

	ApplyTheme("dark")
	if got := CycleTheme(); got != "light" {
		t.Fatalf("CycleTheme from dark = %q, want light", got)
	}
	if got := CycleTheme(); got != "catppuccin" {
		t.Fatalf("CycleTheme from light = %q, want catppuccin", got)
	}
	if got := CycleTheme(); got != "dark" {
		t.Fatalf("CycleTheme from catppuccin = %q, want dark", got)
	}
}

// The /themes command and its picker were cut with the 2026-09 rebuild (docs/tui-cut.md); the
// palette API above stays as infrastructure for when theming returns.

// TestThemeConfigNormalization verifies the tui package's normalizeThemeName handles the values a
// user would type, and that isSupportedTheme only accepts the two real themes.
func TestThemeConfigNormalization(t *testing.T) {
	normCases := []struct {
		in   string
		want string
	}{
		{"", "dark"},
		{"dark", "dark"},
		{"light", "light"},
		{"  Dark  ", "dark"},
		{"LIGHT", "light"},
		{"bogus", "dark"},
	}
	for _, tc := range normCases {
		if got := normalizeThemeName(tc.in); got != tc.want {
			t.Errorf("normalizeThemeName(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
	supportedCases := []struct {
		in   string
		want bool
	}{
		{"dark", true},
		{"light", true},
		{"Dark", true},
		{" LIGHT ", true},
		{"", false},
		{"bogus", false},
	}
	for _, tc := range supportedCases {
		if got := isSupportedTheme(tc.in); got != tc.want {
			t.Errorf("isSupportedTheme(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}
