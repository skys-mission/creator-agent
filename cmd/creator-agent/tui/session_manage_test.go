package tui

// session_manage_test.go covers the session picker's management actions: pin toggle (p), rename
// (Ctrl+R inline editor + Enter commit), and delete (Ctrl+D two-press confirm). Verifies the
// actions only fire when the query is empty, pinned sessions sort to the top, and deleting the
// current session starts a fresh one.

import (
	"testing"

	"github.com/skys-mission/creator-agent/core"
)

// seedSessions saves the given (id,title) pairs into the app's store and returns the store (for
// direct assertions). Saved with a tiny delay so List() newest-first order is deterministic.
func seedSessions(t *testing.T, a *App, sessions ...struct{ id, title string }) core.SessionStore {
	t.Helper()
	for _, s := range sessions {
		if err := a.rt.store.SaveWithMeta(s.id, s.title, []core.Message{core.UserMessage("seed")}); err != nil {
			t.Fatalf("seed %s: %v", s.id, err)
		}
	}
	return a.rt.store
}

// TestSessionPickerPinToggle verifies 'p' toggles the selected session's pin and pinned sessions
// sort to the top (after the current session).
func TestSessionPickerPinToggle(t *testing.T) {
	a := newAppWithSession(t, 80, 24)
	store := seedSessions(t, a,
		struct{ id, title string }{"ses_a", "Alpha"},
		struct{ id, title string }{"ses_b", "Beta"},
	)
	openSessionPicker(a)
	// Select ses_b (the non-current, non-pinned entry). It is the only non-current entry, so find it.
	target := -1
	for i, e := range a.sessionPicker.entries {
		if e.id == "ses_b" {
			target = i
		}
	}
	if target < 0 {
		t.Fatalf("ses_b not in entries: %+v", a.sessionPicker.entries)
	}
	a.sessionPicker.selIdx = target
	// Press 'p' with an empty query -> pins ses_b.
	injectRune(a, 'p')
	// Verify the store reflects the pin.
	pinned := false
	for _, info := range mustList(t, store) {
		if info.ID == "ses_b" {
			pinned = info.Pinned
		}
	}
	if !pinned {
		t.Errorf("'p' should pin ses_b; store says unpinned")
	}
}

// TestSessionPickerPinToggleNoopWithQuery verifies 'p' does NOT pin when the query is non-empty
// (it inserts 'p' into the query instead).
func TestSessionPickerPinToggleNoopWithQuery(t *testing.T) {
	a := newAppWithSession(t, 80, 24)
	store := seedSessions(t, a, struct{ id, title string }{"ses_a", "Alpha"})
	openSessionPicker(a)
	// Type a query character first, then 'p'.
	injectRune(a, 'x')
	injectRune(a, 'p')
	for _, info := range mustList(t, store) {
		if info.ID == "ses_a" && info.Pinned {
			t.Errorf("'p' should not pin when query is non-empty; ses_a got pinned")
		}
	}
	if a.sessionPicker.query.Value() != "xp" {
		t.Errorf("query should be 'xp'; got %q", a.sessionPicker.query.Value())
	}
}

// TestSessionPickerPinnedSortsToTop verifies a pinned session sorts above unpinned ones.
func TestSessionPickerPinnedSortsToTop(t *testing.T) {
	a := newAppWithSession(t, 80, 24)
	store := seedSessions(t, a,
		struct{ id, title string }{"ses_a", "Alpha"},
		struct{ id, title string }{"ses_b", "Beta"},
		struct{ id, title string }{"ses_c", "Gamma"},
	)
	// Pin ses_c.
	if err := store.(core.SessionMetaMutator).SetPinned("ses_c", true); err != nil {
		t.Fatal(err)
	}
	openSessionPicker(a)
	// Expected order: current (repl, if present) first, then pinned (ses_c), then the rest.
	// repl has no saved history so it won't appear in List; the first entry should be the pinned one.
	if len(a.sessionPicker.entries) == 0 {
		t.Fatalf("no entries")
	}
	if a.sessionPicker.entries[0].id != "ses_c" {
		t.Errorf("pinned ses_c should be first; got %q (entries: %+v)", a.sessionPicker.entries[0].id, a.sessionPicker.entries)
	}
}

// TestSessionPickerRename verifies Ctrl+R enters rename mode, typing edits the buffer, and Enter
// commits the new title to the store.
func TestSessionPickerRename(t *testing.T) {
	a := newAppWithSession(t, 80, 24)
	store := seedSessions(t, a, struct{ id, title string }{"ses_a", "Old Title"})
	openSessionPicker(a)
	// Select ses_a.
	for i, e := range a.sessionPicker.entries {
		if e.id == "ses_a" {
			a.sessionPicker.selIdx = i
		}
	}
	// Ctrl+R -> rename mode.
	injectKey(a, KeyCtrlR)
	if a.sessionPicker.mode != pickModeRename {
		t.Fatalf("Ctrl+R should enter rename mode; mode = %v", a.sessionPicker.mode)
	}
	// Clear the seeded title and type a new one.
	a.sessionPicker.renameBuf.SetValue("")
	for _, r := range "New Title" {
		injectRune(a, r)
	}
	if a.sessionPicker.renameBuf.Value() != "New Title" {
		t.Errorf("renameBuf = %q, want 'New Title'", a.sessionPicker.renameBuf.Value())
	}
	// Enter commits.
	injectKey(a, KeyEnter)
	if a.sessionPicker.mode != pickModeBrowse {
		t.Errorf("Enter should exit rename mode; mode = %v", a.sessionPicker.mode)
	}
	// Verify the store has the new title.
	title := ""
	for _, info := range mustList(t, store) {
		if info.ID == "ses_a" {
			title = info.Title
		}
	}
	if title != "New Title" {
		t.Errorf("store title after rename = %q, want 'New Title'", title)
	}
}

// TestSessionPickerRenameEscCancels verifies Esc in rename mode returns to browse without changing
// the title.
func TestSessionPickerRenameEscCancels(t *testing.T) {
	a := newAppWithSession(t, 80, 24)
	store := seedSessions(t, a, struct{ id, title string }{"ses_a", "Keep Me"})
	openSessionPicker(a)
	for i, e := range a.sessionPicker.entries {
		if e.id == "ses_a" {
			a.sessionPicker.selIdx = i
		}
	}
	injectKey(a, KeyCtrlR)
	a.sessionPicker.renameBuf.SetValue("Discarded")
	injectKey(a, KeyEsc)
	if a.sessionPicker.mode != pickModeBrowse {
		t.Errorf("Esc should exit rename mode; mode = %v", a.sessionPicker.mode)
	}
	title := ""
	for _, info := range mustList(t, store) {
		if info.ID == "ses_a" {
			title = info.Title
		}
	}
	if title != "Keep Me" {
		t.Errorf("Esc should not change title; got %q, want 'Keep Me'", title)
	}
}

// TestSessionPickerDeleteTwoPress verifies Ctrl+D arms on first press and deletes on the second.
func TestSessionPickerDeleteTwoPress(t *testing.T) {
	a := newAppWithSession(t, 80, 24)
	store := seedSessions(t, a,
		struct{ id, title string }{"ses_a", "Alpha"},
		struct{ id, title string }{"ses_b", "Beta"},
	)
	openSessionPicker(a)
	// Select ses_a.
	for i, e := range a.sessionPicker.entries {
		if e.id == "ses_a" {
			a.sessionPicker.selIdx = i
		}
	}
	// First Ctrl+D arms the delete.
	injectKey(a, KeyCtrlD)
	if a.sessionPicker.pendingDelete != "ses_a" {
		t.Fatalf("first Ctrl+D should arm pendingDelete; got %q", a.sessionPicker.pendingDelete)
	}
	// Second Ctrl+D confirms.
	injectKey(a, KeyCtrlD)
	// ses_a should be gone from the store.
	for _, info := range mustList(t, store) {
		if info.ID == "ses_a" {
			t.Errorf("ses_a should be deleted; still in list")
		}
	}
}

// TestSessionPickerDeleteCancelsOnMove verifies moving the cursor cancels a pending delete.
func TestSessionPickerDeleteCancelsOnMove(t *testing.T) {
	a := newAppWithSession(t, 80, 24)
	store := seedSessions(t, a,
		struct{ id, title string }{"ses_a", "Alpha"},
		struct{ id, title string }{"ses_b", "Beta"},
	)
	openSessionPicker(a)
	for i, e := range a.sessionPicker.entries {
		if e.id == "ses_a" {
			a.sessionPicker.selIdx = i
		}
	}
	injectKey(a, KeyCtrlD)
	if a.sessionPicker.pendingDelete == "" {
		t.Fatal("first Ctrl+D should arm pendingDelete")
	}
	// Move down -> cancels.
	injectKey(a, KeyDown)
	if a.sessionPicker.pendingDelete != "" {
		t.Errorf("moving should cancel pendingDelete; got %q", a.sessionPicker.pendingDelete)
	}
	// ses_a must still exist.
	found := false
	for _, info := range mustList(t, store) {
		if info.ID == "ses_a" {
			found = true
		}
	}
	if !found {
		t.Errorf("ses_a should NOT be deleted after move-cancel")
	}
}

// TestSessionPickerDeleteCurrentStartsNewSession verifies deleting the current session starts a
// fresh one (the user is never stranded).
func TestSessionPickerDeleteCurrentStartsNewSession(t *testing.T) {
	a := newAppWithSession(t, 80, 24)
	store := seedSessions(t, a, struct{ id, title string }{"ses_other", "Other"})
	// Make "repl" the current session with saved history so it appears in the picker.
	if err := store.SaveWithMeta("repl", "The current one", []core.Message{core.UserMessage("hi")}); err != nil {
		t.Fatal(err)
	}
	openSessionPicker(a)
	// Select "repl".
	for i, e := range a.sessionPicker.entries {
		if e.id == "repl" {
			a.sessionPicker.selIdx = i
		}
	}
	// Two Ctrl+D presses.
	injectKey(a, KeyCtrlD)
	injectKey(a, KeyCtrlD)
	// The current session id should have changed (newSession generated a fresh ses_ id).
	if a.rt.sessionID == "repl" {
		t.Errorf("deleting the current session should start a fresh one; sessionID still 'repl'")
	}
}

// mustList is a test helper that fatals on a store List error.
func mustList(t *testing.T, s core.SessionStore) []core.SessionInfo {
	t.Helper()
	infos, err := s.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	return infos
}
