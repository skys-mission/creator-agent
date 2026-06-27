package tui

import (
	"testing"
)

func TestHelpOverlayRepeatedScroll(t *testing.T) {
	a, _ := newAppWithSim(t, 80, 12)
	a.helpOverlay.open = true
	a.helpOverlay.scroll = 0

	// 12 height: title(0), blank(1), section(2), rows 3..10 (8 rows), footer(11)
	// We have ~25 slash commands, so maxScroll > 0.
	for i := 0; i < 5; i++ {
		injectKey(a, KeyDown)
	}
	if a.helpOverlay.scroll != 5 {
		t.Fatalf("expected scroll 5 after 5 KeyDown, got %d", a.helpOverlay.scroll)
	}

	for i := 0; i < 3; i++ {
		injectKey(a, KeyUp)
	}
	if a.helpOverlay.scroll != 2 {
		t.Fatalf("expected scroll 2 after 3 KeyUp, got %d", a.helpOverlay.scroll)
	}

	injectKey(a, KeyPgDn)
	if a.helpOverlay.scroll <= 2 {
		t.Fatalf("expected scroll > 2 after PgDn, got %d", a.helpOverlay.scroll)
	}

	injectKey(a, KeyHome)
	if a.helpOverlay.scroll != 0 {
		t.Fatalf("expected scroll 0 after Home, got %d", a.helpOverlay.scroll)
	}

	// Space pages down like PgDn; 'b' pages up like PgUp.
	injectRune(a, ' ')
	if a.helpOverlay.scroll <= 0 {
		t.Fatalf("expected scroll > 0 after Space, got %d", a.helpOverlay.scroll)
	}
	pageScroll := a.helpOverlay.scroll
	injectRune(a, 'b')
	if a.helpOverlay.scroll >= pageScroll {
		t.Fatalf("expected scroll < %d after 'b', got %d", pageScroll, a.helpOverlay.scroll)
	}

	injectRune(a, 'q')
	if a.helpOverlay.open {
		t.Fatal("expected help overlay closed after 'q'")
	}
}
