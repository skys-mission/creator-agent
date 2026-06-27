package core

// Tests for debug.go env switch. debugOn caches via sync.Once, so each case needs fresh-process semantics.
// We test the underlying env parsing logic directly (resetting once before checking).

import (
	"sync"
	"testing"
)

// resetDebugOnce resets the debugOnce cache so debugOn re-reads env.
// Reset again at test cleanup to prevent debug state leaking to later tests in the same process.
func resetDebugOnce(t *testing.T) {
	t.Helper()
	debugOnce = sync.Once{}
	debugEnabled = false
	t.Cleanup(func() {
		debugOnce = sync.Once{}
		debugEnabled = false
	})
}

func TestDebugOnEnvTrue(t *testing.T) {
	resetDebugOnce(t)
	t.Setenv("CREATOR_AGENT_DEBUG", "1")
	if !debugOn() {
		t.Error("DEBUG=1 should enable debug")
	}
}

func TestDebugOnEnvTrueWord(t *testing.T) {
	resetDebugOnce(t)
	t.Setenv("CREATOR_AGENT_DEBUG", "true")
	if !debugOn() {
		t.Error("DEBUG=true should enable debug")
	}
}

func TestDebugOnEnvFalse(t *testing.T) {
	resetDebugOnce(t)
	t.Setenv("CREATOR_AGENT_DEBUG", "0")
	if debugOn() {
		t.Error("DEBUG=0 should disable debug")
	}
}

func TestDebugOnEnvUnset(t *testing.T) {
	resetDebugOnce(t)
	t.Setenv("CREATOR_AGENT_DEBUG", "")
	if debugOn() {
		t.Error("DEBUG unset should disable debug")
	}
}

func TestDebugOnEnvCaseInsensitive(t *testing.T) {
	resetDebugOnce(t)
	t.Setenv("CREATOR_AGENT_DEBUG", "  TRUE  ")
	if !debugOn() {
		t.Error("DEBUG='  TRUE  ' should enable (trim + case-insensitive)")
	}
}

func TestTruncateRunes(t *testing.T) {
	if truncateRunes("short", 10) != "short" {
		t.Error("short string should pass through")
	}
	if got := truncateRunes("abcdefghij", 5); got != "abcde..." {
		t.Errorf("truncateRunes = %q, want abcde...", got)
	}
	// rune-safe: the cut lands on a rune boundary, never splitting a multibyte char.
	if got := truncateRunes("你好世界", 2); got != "你好..." {
		t.Errorf("truncateRunes multibyte = %q, want 你好...", got)
	}
}
