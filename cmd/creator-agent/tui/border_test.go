package tui

// border_test.go verifies the layout has no legacy ASCII borders or todo side panel.

import (
	"strings"
	"testing"
)

// TestNoLegacyAsciiBordersOrTodoPanel verifies the old full-width ASCII input borders ('----') and
// the right-hand todo panel title are absent.
func TestNoLegacyAsciiBordersOrTodoPanel(t *testing.T) {
	const w, h = 100, 24
	a, sim := newAppWithSim(t, w, h)
	a.addSystem("an informational message")
	// todo data is present but must NOT render as a side panel anymore.
	a.todoList = []todoItem{
		{content: "task one", status: "in_progress"},
		{content: "task two", status: "pending"},
	}
	render(a)
	for y := 0; y < h; y++ {
		rt := rowText(sim, y)
		if strings.Contains(rt, "----") {
			t.Errorf("legacy ASCII border '----' at row %d: %q", y, rt)
		}
		if strings.Contains(rt, "Todo") {
			t.Errorf("todo panel title 'Todo' at row %d (panel should be removed): %q", y, rt)
		}
	}
}
