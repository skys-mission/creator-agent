package tui

// session_test.go covers the TUI multi-session layer: newSession, switchSession (history reload),
// the /sessions picker (open/filter/commit) and rebuildMessagesFromCore.

import (
	"strings"
	"testing"

	"github.com/skys-mission/creator-agent/core"
)

// newAppWithSession builds a sim App wired with a MemoryStore + initial "repl" session, so the
// multi-session code paths have a real store to read/write.
func newAppWithSession(t *testing.T, w, h int) *App {
	t.Helper()
	a, _ := newAppWithSim(t, w, h)
	a.rt.store = core.NewMemoryStore()
	a.rt.sessionID = "repl"
	return a
}

// TestNewSessionClearsStateAndChangesID verifies /new starts a fresh conversation: messages are
// cleared and the active session id changes to a generated "ses_..." id.
func TestNewSessionClearsStateAndChangesID(t *testing.T) {
	a := newAppWithSession(t, 80, 24)
	// Seed some state that newSession must wipe.
	a.messages = []msgBlock{{kind: kindUser}}
	a.current = &msgBlock{kind: kindAssistant, tools: []toolCard{{name: "read"}}}
	a.totalIn = 42
	prevID := a.rt.sessionID

	if !handleSlashCommand(a, "/new") {
		t.Fatalf("/new should be handled")
	}
	if a.rt.sessionID == prevID {
		t.Fatalf("session id should change after /new; still %q", a.rt.sessionID)
	}
	if !strings.HasPrefix(a.rt.sessionID, "ses_") {
		t.Fatalf("new session id should be a generated ses_ id; got %q", a.rt.sessionID)
	}
	if len(a.messages) != 1 {
		t.Fatalf("messages should be cleared except the system note; got %d blocks", len(a.messages))
	}
	// The single remaining block is the "New session..." system note.
	if !strings.Contains(a.messages[0].content.String(), "New session") {
		t.Fatalf("expected a new-session system note; got %q", a.messages[0].content.String())
	}
	if (a.current != nil && len(a.current.tools) != 0) || a.totalIn != 0 {
		toolCount := 0
		if a.current != nil {
			toolCount = len(a.current.tools)
		}
		t.Fatalf("tools/tokens not cleared: tools=%d totalIn=%d", toolCount, a.totalIn)
	}
}

// TestSwitchSessionLoadsHistory verifies switching to a session restores its text history as
// msgBlocks, and that switching back to the original empties/loads accordingly.
func TestSwitchSessionLoadsHistory(t *testing.T) {
	a := newAppWithSession(t, 80, 24)
	// Pre-populate another session in the store with a 2-message history.
	other := "ses_other"
	if err := a.rt.store.SaveWithMeta(other, "Other", []core.Message{
		core.UserMessage("hello other"),
		core.AssistantMessage("hi back"),
	}); err != nil {
		t.Fatal(err)
	}

	switchSession(a, other)
	if a.rt.sessionID != other {
		t.Fatalf("after switch, sessionID = %q, want %q", a.rt.sessionID, other)
	}
	// History is rebuilt: 2 conversation blocks + 1 "Resumed..." system note.
	// (Order: [user, assistant, system] since system is appended after rebuild.)
	var userText, asstText string
	for _, mb := range a.messages {
		switch mb.kind {
		case kindUser:
			userText = mb.content.String()
		case kindAssistant:
			asstText = mb.content.String()
		}
	}
	if userText != "hello other" {
		t.Fatalf("rebuilt user block = %q, want 'hello other'", userText)
	}
	if asstText != "hi back" {
		t.Fatalf("rebuilt assistant block = %q, want 'hi back'", asstText)
	}
}

// TestSwitchSessionRefusedWhileBusy verifies switching is blocked during an active stream so the
// message array isn't swapped mid-flight.
func TestSwitchSessionRefusedWhileBusy(t *testing.T) {
	a := newAppWithSession(t, 80, 24)
	a.status = statusThinking
	prevID := a.rt.sessionID
	switchSession(a, "ses_target")
	if a.rt.sessionID != prevID {
		t.Fatalf("switch during busy should be refused; id changed to %q", a.rt.sessionID)
	}
}

// TestSwitchSessionNoopForSameID verifies switching to the current session is a no-op.
func TestSwitchSessionNoopForSameID(t *testing.T) {
	a := newAppWithSession(t, 80, 24)
	a.messages = []msgBlock{{kind: kindUser}}
	switchSession(a, a.rt.sessionID) // same id
	// messages untouched (no reload happened).
	if len(a.messages) != 1 {
		t.Fatalf("same-id switch should be a no-op; messages changed to %d blocks", len(a.messages))
	}
}

// TestLoadCurrentSessionHistory verifies startup history loading (-c/--continue path): when the
// current session has stored history it is rebuilt into the message area; an empty/missing session
// leaves messages empty (so the home screen shows).
func TestLoadCurrentSessionHistory(t *testing.T) {
	t.Run("loads stored history", func(t *testing.T) {
		a := newAppWithSession(t, 80, 24)
		// Seed the current session with a user+assistant exchange.
		seed := []core.Message{core.UserMessage("hi"), core.AssistantMessage("hello")}
		if err := a.rt.store.SaveWithMeta(a.rt.sessionID, "t", seed); err != nil {
			t.Fatalf("seed: %v", err)
		}
		loadCurrentSessionHistory(a)
		if len(a.messages) != 2 {
			t.Fatalf("expected 2 blocks loaded, got %d", len(a.messages))
		}
		if !a.atBottom {
			t.Errorf("expected atBottom=true after load")
		}
	})

	t.Run("no history leaves messages empty", func(t *testing.T) {
		a := newAppWithSession(t, 80, 24)
		// Current session has nothing stored.
		loadCurrentSessionHistory(a)
		if len(a.messages) != 0 {
			t.Fatalf("expected 0 blocks for empty session, got %d", len(a.messages))
		}
	})

	t.Run("nil store is safe", func(t *testing.T) {
		a := newAppWithSession(t, 80, 24)
		a.rt.store = nil
		loadCurrentSessionHistory(a) // must not panic
		if len(a.messages) != 0 {
			t.Fatalf("expected 0 blocks with nil store, got %d", len(a.messages))
		}
	})
}

// TestRebuildMessagesFromCore covers the role mapping and the skip of system/tool roles.
func TestRebuildMessagesFromCore(t *testing.T) {
	a := newAppWithSession(t, 80, 24)
	msgs := []core.Message{
		core.SystemMessage("sys prompt"),                // skipped
		core.UserMessage("user text"),                   // -> kindUser
		core.AssistantMessage("assistant text"),         // -> kindAssistant
		core.Message{Role: core.RoleTool, Content: "x"}, // skipped
		core.AssistantMessage(""),                       // skipped (empty)
	}
	got := rebuildMessagesFromCore(a, msgs)
	if len(got) != 2 {
		t.Fatalf("rebuilt %d blocks, want 2 (user + assistant)", len(got))
	}
	if got[0].kind != kindUser || got[0].content.String() != "user text" {
		t.Fatalf("block 0 = %+v, want kindUser/user text", got[0])
	}
	if got[1].kind != kindAssistant || got[1].content.String() != "assistant text" {
		t.Fatalf("block 1 = %+v, want kindAssistant/assistant text", got[1])
	}
}

// TestSessionPickerOpenFilterCommit covers the /sessions overlay lifecycle.
func TestSessionPickerOpenFilterCommit(t *testing.T) {
	a := newAppWithSession(t, 80, 24)
	// Seed two sessions; the current one (repl) + a target.
	if err := a.rt.store.SaveWithMeta("ses_alpha", "Alpha chat", []core.Message{core.UserMessage("a")}); err != nil {
		t.Fatal(err)
	}
	if err := a.rt.store.SaveWithMeta("ses_beta", "Beta chat", []core.Message{core.UserMessage("b")}); err != nil {
		t.Fatal(err)
	}

	// Open via /sessions.
	if !handleSlashCommand(a, "/sessions") {
		t.Fatalf("/sessions should be handled")
	}
	if !a.sessionPicker.open {
		t.Fatalf("session picker should be open after /sessions")
	}
	// Entries include both seeded sessions (current "repl" has no history yet, so it may not appear
	// in List unless it was saved — it was not, so expect 2 entries).
	if len(a.sessionPicker.entries) != 2 {
		t.Fatalf("picker entries = %d, want 2: %+v", len(a.sessionPicker.entries), a.sessionPicker.entries)
	}

	// Type "alp" to filter down to the Alpha session.
	for _, r := range "alp" {
		injectRune(a, r)
	}
	var got []string
	for _, e := range a.sessionPicker.entries {
		got = append(got, e.id)
	}
	matchedAlpha := false
	for _, id := range got {
		if id == "ses_alpha" {
			matchedAlpha = true
		}
	}
	if !matchedAlpha {
		t.Fatalf("filter 'alp' should keep ses_alpha; got %v", got)
	}
	// Ensure no beta survived the filter.
	for _, id := range got {
		if id == "ses_beta" {
			t.Fatalf("filter 'alp' should drop ses_beta; got %v", got)
		}
	}

	// Enter commits the selected (first, since alpha is the only match) session.
	injectKey(a, KeyEnter)
	if a.sessionPicker.open {
		t.Fatalf("picker should close on Enter")
	}
	if a.rt.sessionID != "ses_alpha" {
		t.Fatalf("after commit, sessionID = %q, want ses_alpha", a.rt.sessionID)
	}
}

// TestSessionPickerEscClose verifies Esc dismisses without switching.
func TestSessionPickerEscClose(t *testing.T) {
	a := newAppWithSession(t, 80, 24)
	if err := a.rt.store.SaveWithMeta("ses_x", "X", []core.Message{core.UserMessage("x")}); err != nil {
		t.Fatal(err)
	}
	openSessionPicker(a)
	prev := a.rt.sessionID
	injectKey(a, KeyEsc)
	if a.sessionPicker.open {
		t.Fatalf("Esc should close the picker")
	}
	if a.rt.sessionID != prev {
		t.Fatalf("Esc should not switch sessions; id = %q, want %q", a.rt.sessionID, prev)
	}
}

// TestSlashClearUsesCurrentSession verifies /clear clears the active session (not a hardcoded
// "repl"), so clearing works after switching to a generated ses_ id.
func TestSlashClearUsesCurrentSession(t *testing.T) {
	a := newAppWithSession(t, 80, 24)
	// Switch to a fresh generated session, then /clear it.
	newSession(a)
	curID := a.rt.sessionID
	a.messages = []msgBlock{{kind: kindUser}, {kind: kindAssistant}}
	if !handleSlashCommand(a, "/clear") {
		t.Fatalf("/clear should be handled")
	}
	// sessionID unchanged by /clear (it clears history, doesn't switch).
	if a.rt.sessionID != curID {
		t.Fatalf("/clear changed session id: %q -> %q", curID, a.rt.sessionID)
	}
	if len(a.messages) != 0 {
		t.Fatalf("/clear should wipe messages; got %d blocks", len(a.messages))
	}
}

// TestShortID covers the truncation helper.
func TestShortID(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"repl", "repl"},                      // short, unchanged
		{"ab", "ab"},                          // very short
		{"ses_0123456789abcdef", "ses_…cdef"}, // long, truncated
	}
	for _, tc := range cases {
		if got := shortID(tc.in); got != tc.want {
			t.Errorf("shortID(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
