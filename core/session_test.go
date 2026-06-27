package core

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

// MemoryStore: basic Save/Load/Clear behavior.
func TestMemoryStoreBasic(t *testing.T) {
	s := NewMemoryStore()
	if h, err := s.Load("x"); err != nil || h != nil {
		t.Errorf("Load nonexistent = %v %v, want nil nil", h, err)
	}
	msgs := []Message{UserMessage("a"), AssistantMessage("b")}
	if err := s.Save("x", msgs); err != nil {
		t.Fatal(err)
	}
	got, err := s.Load("x")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, msgs) {
		t.Errorf("Load mismatch: %+v", got)
	}
	if err := s.Clear("x"); err != nil {
		t.Fatal(err)
	}
	if h, _ := s.Load("x"); h != nil {
		t.Error("Clear did not remove")
	}
	// Clear non-existent should not error
	if err := s.Clear("never"); err != nil {
		t.Errorf("Clear nonexistent err = %v", err)
	}
}

// JSONFileStore: write -> read -> clear (persisted + survives restart).
func TestJSONFileStoreRoundTrip(t *testing.T) {
	dir := t.TempDir()
	s := &JSONFileStore{Dir: dir}

	if h, err := s.Load("sess"); err != nil || h != nil {
		t.Errorf("Load nonexistent = %v %v, want nil nil", h, err)
	}
	msgs := []Message{
		SystemMessage("sys"),
		UserMessage("hi"),
		AssistantMessage("hey", ToolCall{ID: "c1", Name: "bash", Input: []byte("{}")}),
		ToolMessage("done", "c1", "bash"),
	}
	if err := s.Save("sess", msgs); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) == 0 {
		t.Fatal("no session file written")
	}
	got, err := s.Load("sess")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, msgs) {
		t.Errorf("round-trip mismatch:\n got=%+v\nwant=%+v", got, msgs)
	}
	// Simulate restart: new store pointing to the same directory
	s2 := &JSONFileStore{Dir: dir}
	got2, err := s2.Load("sess")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got2, msgs) {
		t.Errorf("restart Load mismatch: %+v", got2)
	}
	if err := s.Clear("sess"); err != nil {
		t.Fatal(err)
	}
	if h, _ := s.Load("sess"); h != nil {
		t.Error("Clear did not remove file")
	}
}

// JSONFileStore: MkdirAll creates directories automatically.
func TestJSONFileStoreMkdirAll(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "deeper")
	s := &JSONFileStore{Dir: dir}
	if err := s.Save("x", []Message{UserMessage("a")}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Load("x"); err != nil {
		t.Fatal(err)
	}
}

// JSONFileStore: path traversal protection.
func TestJSONFileStorePathTraversal(t *testing.T) {
	s := &JSONFileStore{Dir: t.TempDir()}
	if _, err := s.Load("../../etc/passwd"); err != nil {
		// error is fine; the key is that it must not read outside Dir
	}
	if got := SanitizeFilename("../../etc/passwd"); got == "../../etc/passwd" {
		t.Error("path not sanitized")
	}
}

// SanitizeFilename: legal characters preserved, illegal ones replaced.
func TestSanitizeSessionID(t *testing.T) {
	cases := map[string]string{
		"repl":      "repl",
		"session-1": "session-1",
		"sess_2":    "sess_2",
		"a/b/../c":  "a-b----c",
		"":          "",
		"a b!c":     "a-b-c",
	}
	for in, want := range cases {
		if got := SanitizeFilename(in); got != want {
			t.Errorf("sanitize(%q) = %q, want %q", in, got, want)
		}
	}
}

// End-to-end: agent with JSONFileStore persists multi-turn history across Stream calls.
func TestAgentSessionPersistenceAcrossStream(t *testing.T) {
	dir := t.TempDir()
	store := &JSONFileStore{Dir: dir}
	mock := &mockProvider{turns: [][]ModelEvent{
		{MTextDelta{Delta: "ok"}},
	}}
	ag := NewAgent(mock, WithSessionStore(store))

	events, err := ag.Stream(context.Background(), PromptInput("repl", "hello"))
	if err != nil {
		t.Fatal(err)
	}
	for range events {
	}

	mock2 := &mockProvider{turns: [][]ModelEvent{
		{MTextDelta{Delta: "ok2"}},
	}}
	ag2 := NewAgent(mock2, WithSessionStore(store))
	hist, err := store.Load("repl")
	if err != nil {
		t.Fatal(err)
	}
	if len(hist) == 0 {
		t.Error("session not persisted after first Stream")
	}
	hasUser := false
	for _, m := range hist {
		if m.Role == RoleUser && m.Content == "hello" {
			hasUser = true
		}
	}
	if !hasUser {
		t.Error("user message not in persisted history")
	}
	_ = ag2
}

// ClearSession clears the store.
func TestAgentClearSession(t *testing.T) {
	store := NewMemoryStore()
	_ = store.Save("repl", []Message{UserMessage("x")})
	ag := NewAgent(&mockProvider{}, WithSessionStore(store)).(*agent)
	ag.ClearSession("repl")
	if h, _ := store.Load("repl"); h != nil {
		t.Error("ClearSession did not clear store")
	}
}

// TestJSONFileStoreConcurrentSave: concurrent Save/Clear/Load from multiple goroutines must not panic or corrupt files.
func TestJSONFileStoreConcurrentSave(t *testing.T) {
	dir := t.TempDir()
	s := &JSONFileStore{Dir: dir}
	const n = 50
	done := make(chan error, n*3)
	for i := 0; i < n; i++ {
		i := i
		go func() {
			msgs := []Message{UserMessage("msg"), AssistantMessage("reply")}
			done <- s.Save("sess", msgs)
		}()
		go func() {
			_, err := s.Load("sess")
			done <- err
		}()
		go func() {
			_ = i
			done <- s.Clear("other-sess")
		}()
	}
	for k := 0; k < n*3; k++ {
		if err := <-done; err != nil {
			t.Errorf("concurrent op failed: %v", err)
		}
	}
	got, err := s.Load("sess")
	if err != nil {
		t.Fatalf("final Load: %v", err)
	}
	if len(got) != 2 || got[0].Content != "msg" || got[1].Content != "reply" {
		t.Errorf("final history corrupted: %+v", got)
	}
}

// --- Multi-session: List, SaveWithMeta, backward-compatible legacy format ---

// TestMemoryStoreList verifies MemoryStore.List returns all sessions newest-first with their
// derived titles.
func TestMemoryStoreList(t *testing.T) {
	s := NewMemoryStore()
	if got, err := s.List(); err != nil || len(got) != 0 {
		t.Fatalf("empty List = %v (err %v), want empty", got, err)
	}
	// Save two sessions with explicit titles.
	if err := s.SaveWithMeta("old", "Old session", []Message{UserMessage("old")}); err != nil {
		t.Fatal(err)
	}
	// Stagger UpdatedAt so ordering is deterministic even on fast machines: re-save to bump time.
	time.Sleep(2 * time.Millisecond)
	if err := s.SaveWithMeta("new", "New session", []Message{UserMessage("new")}); err != nil {
		t.Fatal(err)
	}
	got, err := s.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("List returned %d items, want 2: %+v", len(got), got)
	}
	// Newest first.
	if got[0].ID != "new" || got[1].ID != "old" {
		t.Fatalf("List order = [%s, %s], want [new, old]", got[0].ID, got[1].ID)
	}
	if got[0].Title != "New session" || got[1].Title != "Old session" {
		t.Fatalf("List titles = %q, %q", got[0].Title, got[1].Title)
	}
}

// TestMemoryStoreSavePreservesTitle verifies plain Save does not clobber an existing title.
func TestMemoryStoreSavePreservesTitle(t *testing.T) {
	s := NewMemoryStore()
	if err := s.SaveWithMeta("s", "My Title", []Message{UserMessage("hi")}); err != nil {
		t.Fatal(err)
	}
	// Plain Save with empty title must keep "My Title".
	if err := s.Save("s", []Message{UserMessage("hi"), AssistantMessage("yo")}); err != nil {
		t.Fatal(err)
	}
	got, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	var title string
	for _, info := range got {
		if info.ID == "s" {
			title = info.Title
		}
	}
	if title != "My Title" {
		t.Fatalf("title after plain Save = %q, want My Title", title)
	}
}

// TestJSONFileStoreListRoundtrip covers SaveWithMeta + Load + List on disk, including title
// preservation across a plain Save.
func TestJSONFileStoreListRoundtrip(t *testing.T) {
	dir := t.TempDir()
	s, err := NewJSONFileStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SaveWithMeta("ses_a", "Alpha", []Message{UserMessage("a")}); err != nil {
		t.Fatal(err)
	}
	// Plain Save must preserve the title "Alpha".
	if err := s.Save("ses_a", []Message{UserMessage("a"), AssistantMessage("b")}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(2 * time.Millisecond)
	if err := s.SaveWithMeta("ses_b", "Beta", []Message{UserMessage("b")}); err != nil {
		t.Fatal(err)
	}
	// Load returns the latest history for ses_a.
	got, err := s.Load("ses_a")
	if err != nil {
		t.Fatalf("Load ses_a: %v", err)
	}
	if len(got) != 2 || got[0].Content != "a" || got[1].Content != "b" {
		t.Fatalf("Load ses_a = %+v, want [a, b]", got)
	}
	// List returns both, newest first.
	infos, err := s.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(infos) != 2 {
		t.Fatalf("List returned %d, want 2: %+v", len(infos), infos)
	}
	if infos[0].ID != "ses_b" || infos[1].ID != "ses_a" {
		t.Fatalf("List order = [%s, %s], want [ses_b, ses_a]", infos[0].ID, infos[1].ID)
	}
	if infos[1].Title != "Alpha" {
		t.Fatalf("ses_a title = %q, want Alpha (preserved across plain Save)", infos[1].Title)
	}
}

// TestJSONFileStoreLoadLegacyBareFormat writes a legacy bare []Message file (pre-multi-session
// format) and verifies Load still reads it (backward compatibility), and List includes it with a
// derived title.
func TestJSONFileStoreLoadLegacyBareFormat(t *testing.T) {
	dir := t.TempDir()
	s, err := NewJSONFileStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	// Write the legacy format directly: a bare JSON array of messages.
	legacy := []Message{UserMessage("legacy hello"), AssistantMessage("legacy reply")}
	data, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	legacyPath := filepath.Join(dir, "repl.json")
	if err := os.WriteFile(legacyPath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	// Load must transparently parse the legacy format.
	got, err := s.Load("repl")
	if err != nil {
		t.Fatalf("Load legacy: %v", err)
	}
	if len(got) != 2 || got[0].Content != "legacy hello" {
		t.Fatalf("legacy Load = %+v, want 2 msgs", got)
	}
	// List includes the legacy file with a derived title.
	infos, err := s.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(infos) != 1 || infos[0].ID != "repl" {
		t.Fatalf("List = %+v, want single repl entry", infos)
	}
	if infos[0].Title != "legacy hello" {
		t.Fatalf("legacy derived title = %q, want 'legacy hello'", infos[0].Title)
	}
	// After a plain Save, the file is rewritten in the wrapped format but the derived title
	// (preserved) must remain stable.
	if err := s.Save("repl", legacy); err != nil {
		t.Fatal(err)
	}
	again, err := s.Load("repl")
	if err != nil || len(again) != 2 {
		t.Fatalf("post-upgrade Load = %+v (err %v)", again, err)
	}
}

// TestJSONFileStoreLoadMeta verifies LoadMeta reads title/UpdatedAt without loading full history.
func TestJSONFileStoreLoadMeta(t *testing.T) {
	dir := t.TempDir()
	s, err := NewJSONFileStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SaveWithMeta("x", "Meta Title", []Message{UserMessage("x")}); err != nil {
		t.Fatal(err)
	}
	title, updatedAt, _, err := s.LoadMeta("x")
	if err != nil {
		t.Fatalf("LoadMeta: %v", err)
	}
	if title != "Meta Title" {
		t.Fatalf("LoadMeta title = %q, want Meta Title", title)
	}
	if updatedAt.IsZero() {
		t.Fatalf("LoadMeta updatedAt is zero")
	}
	// Non-existent session returns zero values, no error.
	t2, u2, _, err := s.LoadMeta("missing")
	if err != nil || t2 != "" || !u2.IsZero() {
		t.Fatalf("LoadMeta missing = %q %v (err %v), want zero values", t2, u2, err)
	}
}

// TestMemoryStoreSetPinnedRename verifies the MemoryStore metadata mutator: SetPinned toggles the
// pin flag (reflected in List + LoadMeta), and Rename changes the title without bumping UpdatedAt.
func TestMemoryStoreSetPinnedRename(t *testing.T) {
	m := NewMemoryStore()
	if err := m.SaveWithMeta("a", "Alpha", []Message{UserMessage("x")}); err != nil {
		t.Fatal(err)
	}
	if err := m.SetPinned("a", true); err != nil {
		t.Fatal(err)
	}
	infos, _ := m.List()
	if !infos[0].Pinned {
		t.Errorf("SetPinned(true) not reflected in List")
	}
	// LoadMeta must report the same pin flag — the TUI /pin toggle reads current state via LoadMeta
	// before flipping it, so without this the toggle could never un-pin on the default store.
	if _, _, pinned, err := m.LoadMeta("a"); err != nil || !pinned {
		t.Errorf("LoadMeta after SetPinned(true) = pinned=%v err=%v, want true nil", pinned, err)
	}
	// Save again (simulating a new turn): pin must survive.
	if err := m.Save("a", []Message{UserMessage("x"), AssistantMessage("y")}); err != nil {
		t.Fatal(err)
	}
	infos, _ = m.List()
	if !infos[0].Pinned {
		t.Errorf("pin lost after Save")
	}
	// Rename: title changes, UpdatedAt preserved (rename is metadata-only).
	before := infos[0].UpdatedAt
	if err := m.Rename("a", "Alpha Renamed"); err != nil {
		t.Fatal(err)
	}
	infos, _ = m.List()
	if infos[0].Title != "Alpha Renamed" {
		t.Errorf("Rename title = %q, want Alpha Renamed", infos[0].Title)
	}
	if !infos[0].UpdatedAt.Equal(before) {
		t.Errorf("Rename bumped UpdatedAt: was %v now %v", before, infos[0].UpdatedAt)
	}
	if !infos[0].Pinned {
		t.Errorf("Rename dropped pin flag")
	}
	// Rename of a non-existent session is a no-op (no error).
	if err := m.Rename("ghost", "X"); err != nil {
		t.Errorf("Rename non-existent should be a no-op, got %v", err)
	}
	// LoadMeta on a non-existent session returns zero values with no error, mirroring JSONFileStore.
	if title, _, pinned, err := m.LoadMeta("ghost"); err != nil || title != "" || pinned {
		t.Errorf("LoadMeta non-existent = title=%q pinned=%v err=%v, want empty/false/nil", title, pinned, err)
	}
}

// TestJSONFileStoreSetPinnedRename verifies the JSONFileStore metadata mutator + pin preservation
// across SaveWithMeta (auto-save must not un-pin) + persistence across a simulated restart.
func TestJSONFileStoreSetPinnedRename(t *testing.T) {
	dir := t.TempDir()
	s := &JSONFileStore{Dir: dir}
	if err := s.SaveWithMeta("a", "Alpha", []Message{UserMessage("x")}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetPinned("a", true); err != nil {
		t.Fatal(err)
	}
	// Save again (a new turn): pin must survive (SaveWithMeta preserves it).
	if err := s.SaveWithMeta("a", "", []Message{UserMessage("x"), AssistantMessage("y")}); err != nil {
		t.Fatal(err)
	}
	infos, _ := s.List()
	if len(infos) != 1 || !infos[0].Pinned {
		t.Fatalf("pin lost after SaveWithMeta: %+v", infos)
	}
	// Rename persists and survives restart.
	if err := s.Rename("a", "Alpha Renamed"); err != nil {
		t.Fatal(err)
	}
	s2 := &JSONFileStore{Dir: dir} // simulate restart
	infos2, _ := s2.List()
	if len(infos2) != 1 {
		t.Fatalf("restart List = %d entries, want 1", len(infos2))
	}
	if infos2[0].Title != "Alpha Renamed" {
		t.Errorf("restart title = %q, want Alpha Renamed", infos2[0].Title)
	}
	if !infos2[0].Pinned {
		t.Errorf("restart lost pin flag")
	}
	// LoadMeta returns the pinned flag too.
	if _, _, pinned, _ := s2.LoadMeta("a"); !pinned {
		t.Errorf("LoadMeta pinned = false, want true after restart")
	}
	// Un-pin, then verify it persists as false.
	if err := s2.SetPinned("a", false); err != nil {
		t.Fatal(err)
	}
	infos3, _ := s2.List()
	if infos3[0].Pinned {
		t.Errorf("SetPinned(false) not reflected")
	}
}
