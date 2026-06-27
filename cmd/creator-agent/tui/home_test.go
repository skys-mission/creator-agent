package tui

// home_test.go covers the home (empty-state) screen: rendering (brand + tip + recent sessions +
// examples), the digit-key 1-9 session quick-switch, the empty-store guard, and tip selection.

import (
	"strings"
	"testing"
	"time"

	"github.com/skys-mission/creator-agent/core"
)

// newAppWithStore builds a sim App wired with a MemoryStore seeded with the given sessions (in
// insertion order; each saved with a small delay so List() returns them newest-first in that order).
func newAppWithStore(t *testing.T, sessionSeeds []struct{ id, title string }) *App {
	t.Helper()
	a, _ := newAppWithSim(t, 80, 24)
	a.rt.store = core.NewMemoryStore()
	a.rt.currentAgent = "build"
	// Seed sessions oldest-first with a small delay so the stable newest-first sort preserves order.
	for i, s := range sessionSeeds {
		_ = i
		_ = a.rt.store.SaveWithMeta(s.id, s.title, []core.Message{core.UserMessage("seed")})
		time.Sleep(2 * time.Millisecond)
	}
	return a
}

// joinedHomeText flattens the home screen lines into a single lowercase string for substring checks.
func joinedHomeText(a *App) string {
	lines := renderHomeLines(a, 78)
	var sb strings.Builder
	for _, ln := range lines {
		for _, r := range ln.runs {
			sb.WriteString(r.text)
		}
		sb.WriteByte('\n')
	}
	return strings.ToLower(sb.String())
}

// TestHomeRendersTip verifies the home screen shows a tip from the pool. (The brand wordmark +
// tagline were removed from the home view by design; the context line is now the sole anchor.)
func TestHomeRendersTip(t *testing.T) {
	// Deterministic tip selection.
	prev := homeTipPick
	homeTipPick = func(n int) int { return 0 }
	defer func() { homeTipPick = prev }()

	a, _ := newAppWithSim(t, 80, 24)
	a.rt.currentAgent = "build"
	a.rt.store = nil // no sessions -> no recent-sessions block, must not crash
	txt := joinedHomeText(a)

	if strings.Contains(txt, "creator-agent") {
		t.Errorf("home should NOT show the brand wordmark anymore; got:\n%s", txt)
	}
	if strings.Contains(txt, "open-source coding agent") {
		t.Errorf("home should NOT show the tagline anymore; got:\n%s", txt)
	}
	if !strings.Contains(txt, "● tip") {
		t.Errorf("home should show a tip line; got:\n%s", txt)
	}
	// The chosen tip text should be present.
	pool := homeTips()
	if !strings.Contains(txt, strings.ToLower(pool[0])) {
		t.Errorf("home tip text mismatch; got:\n%s", txt)
	}
}

// TestHomeShowsAgentContext verifies the agent/model context line reflects current agent + model.
func TestHomeShowsAgentContext(t *testing.T) {
	prev := homeTipPick
	homeTipPick = func(n int) int { return 0 }
	defer func() { homeTipPick = prev }()

	a, _ := newAppWithSim(t, 80, 24)
	a.rt.currentAgent = "plan"
	a.rt.prof = profile{Name: "openai", Model: "gpt-4o", BaseURL: "https://api.openai.com/v1"}
	a.rt.store = nil
	txt := joinedHomeText(a)

	if !strings.Contains(txt, "plan") {
		t.Errorf("home should show the current agent; got:\n%s", txt)
	}
	if !strings.Contains(txt, "gpt-4o") {
		t.Errorf("home should show the current model; got:\n%s", txt)
	}
}

// TestHomeRecentSessionsListed verifies seeded sessions appear in the recent-sessions block.
func TestHomeRecentSessionsListed(t *testing.T) {
	prev := homeTipPick
	homeTipPick = func(n int) int { return 0 }
	defer func() { homeTipPick = prev }()

	seeds := []struct{ id, title string }{
		{"ses_alpha", "Refactor config loader"},
		{"ses_beta", "Fix grep tool"},
	}
	a := newAppWithStore(t, seeds)
	// The "current" session (none here) is excluded by ID; with no current sessionID set, all show.
	txt := joinedHomeText(a)

	if !strings.Contains(txt, "recent") {
		t.Errorf("home should show a recent-sessions heading; got:\n%s", txt)
	}
	if !strings.Contains(txt, "refactor config loader") || !strings.Contains(txt, "fix grep tool") {
		t.Errorf("home should list seeded session titles; got:\n%s", txt)
	}
	// homeSessions cache should hold the seeded ids.
	if len(a.homeSessions) != 2 {
		t.Errorf("homeSessions cache = %d, want 2: %+v", len(a.homeSessions), a.homeSessions)
	}
}

// TestHomeExcludesCurrentSession verifies the current session is not listed on home.
func TestHomeExcludesCurrentSession(t *testing.T) {
	prev := homeTipPick
	homeTipPick = func(n int) int { return 0 }
	defer func() { homeTipPick = prev }()

	seeds := []struct{ id, title string }{
		{"ses_current", "The active one"},
		{"ses_other", "A different one"},
	}
	a := newAppWithStore(t, seeds)
	a.rt.sessionID = "ses_current"
	renderHomeLines(a, 78) // populate a.homeSessions
	for _, e := range a.homeSessions {
		if e.ID == "ses_current" {
			t.Errorf("current session should be excluded from home list; found %q", e.ID)
		}
	}
	if len(a.homeSessions) != 1 || a.homeSessions[0].ID != "ses_other" {
		t.Errorf("homeSessions = %+v, want only ses_other", a.homeSessions)
	}
}

// TestHomeDigitQuickSwitch verifies pressing 1 on the home screen switches to the most-recent
// (first-listed) session.
func TestHomeDigitQuickSwitch(t *testing.T) {
	prev := homeTipPick
	homeTipPick = func(n int) int { return 0 }
	defer func() { homeTipPick = prev }()

	seeds := []struct{ id, title string }{
		{"ses_recent", "Most recent"},
		{"ses_older", "Older one"},
	}
	a := newAppWithStore(t, seeds)
	// Render once so homeSessions is populated (the render path does this).
	renderHomeLines(a, 78)
	if len(a.homeSessions) == 0 {
		t.Fatalf("precondition: homeSessions should be populated")
	}
	// On the home screen (idle, empty input, no messages, no completions), press '1'.
	injectRune(a, '1')
	if a.rt.sessionID != a.homeSessions[0].ID {
		t.Errorf("after pressing 1, sessionID = %q, want %q (first recent session)", a.rt.sessionID, a.homeSessions[0].ID)
	}
}

// TestHomeDigitNotActiveWhenInputNonEmpty verifies a digit is inserted as text (not a switch) when
// the input is non-empty.
func TestHomeDigitNotActiveWhenInputNonEmpty(t *testing.T) {
	prev := homeTipPick
	homeTipPick = func(n int) int { return 0 }
	defer func() { homeTipPick = prev }()

	seeds := []struct{ id, title string }{{"ses_x", "x"}}
	a := newAppWithStore(t, seeds)
	renderHomeLines(a, 78)
	a.input.SetValue("hello") // now off-home-quick-switch (input non-empty)
	before := a.rt.sessionID
	injectRune(a, '1')
	if a.rt.sessionID != before {
		t.Errorf("digit should not switch when input non-empty; sessionID changed %q -> %q", before, a.rt.sessionID)
	}
	if a.input.Value() != "hello1" {
		t.Errorf("digit should be appended to input; got %q", a.input.Value())
	}
}

// TestHomeDigitNotActiveWhenMessagesExist verifies a digit does not switch once there are messages
// (i.e. no longer on the home screen).
func TestHomeDigitNotActiveWhenMessagesExist(t *testing.T) {
	prev := homeTipPick
	homeTipPick = func(n int) int { return 0 }
	defer func() { homeTipPick = prev }()

	seeds := []struct{ id, title string }{{"ses_x", "x"}}
	a := newAppWithStore(t, seeds)
	renderHomeLines(a, 78)
	a.messages = []msgBlock{{kind: kindUser}} // simulate having sent a message
	before := a.rt.sessionID
	injectRune(a, '1')
	if a.rt.sessionID != before {
		t.Errorf("digit should not switch when messages exist; sessionID changed to %q", a.rt.sessionID)
	}
}

// TestHomeDigitOutOfRangeIsNoop verifies pressing a digit beyond the list length does nothing.
func TestHomeDigitOutOfRangeIsNoop(t *testing.T) {
	prev := homeTipPick
	homeTipPick = func(n int) int { return 0 }
	defer func() { homeTipPick = prev }()

	seeds := []struct{ id, title string }{{"ses_only", "only one"}}
	a := newAppWithStore(t, seeds)
	renderHomeLines(a, 78)
	before := a.rt.sessionID
	injectRune(a, '9') // only 1 session; '9' is out of range
	if a.rt.sessionID != before {
		t.Errorf("out-of-range digit should be a no-op; sessionID changed to %q", a.rt.sessionID)
	}
}

// TestHomeEmptyStoreNoCrash verifies an empty store does not produce a sessions block or crash.
func TestHomeEmptyStoreNoCrash(t *testing.T) {
	prev := homeTipPick
	homeTipPick = func(n int) int { return 0 }
	defer func() { homeTipPick = prev }()

	a, _ := newAppWithSim(t, 80, 24)
	a.rt.store = core.NewMemoryStore() // empty store
	a.rt.currentAgent = "build"
	txt := joinedHomeText(a)
	if strings.Contains(txt, "recent sessions") {
		t.Errorf("empty store should not show a recent-sessions block; got:\n%s", txt)
	}
	if len(a.homeSessions) != 0 {
		t.Errorf("homeSessions should be empty for empty store; got %+v", a.homeSessions)
	}
}

// TestPickHomeTipCachesAndClears verifies the tip is cached until cleared, then re-rolled.
func TestPickHomeTipCachesAndClears(t *testing.T) {
	calls := 0
	prev := homeTipPick
	homeTipPick = func(n int) int { calls++; return 0 }
	defer func() { homeTipPick = prev }()

	a, _ := newAppWithSim(t, 80, 24)
	_ = pickHomeTip(a) // first call rolls
	_ = pickHomeTip(a) // second call reuses the cache (no new roll)
	if calls != 1 {
		t.Errorf("pickHomeTip should cache; got %d rolls, want 1", calls)
	}
	clearHomeTip(a)
	_ = pickHomeTip(a) // cleared -> re-roll
	if calls != 2 {
		t.Errorf("pickHomeTip should re-roll after clear; got %d rolls, want 2", calls)
	}
}
