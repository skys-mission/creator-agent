package tui

import "testing"

func TestColorBlackNotDefault(t *testing.T) {
	if ColorBlack.IsDefault() {
		t.Error("ColorBlack should not be treated as ColorDefault")
	}
	if ColorWhite.IsDefault() {
		t.Error("ColorWhite should not be treated as ColorDefault")
	}
	r, g, b, ok := ColorBlack.RGB()
	if !ok || r != 0 || g != 0 || b != 0 {
		t.Errorf("ColorBlack.RGB() = (%d,%d,%d,%v), want (0,0,0,true)", r, g, b, ok)
	}
}
