package core

// session_meta_test.go covers the session ID generator (descending lexicographic order) and the
// title helpers.

import (
	"strings"
	"testing"
	"time"
)

// TestGenerateSessionIDFormat asserts the prefix and length are stable so List ordering and
// sanitization (filename-safe) keep working.
func TestGenerateSessionIDFormat(t *testing.T) {
	id := GenerateSessionID()
	if !strings.HasPrefix(id, "ses_") {
		t.Fatalf("id %q missing ses_ prefix", id)
	}
	body := strings.TrimPrefix(id, "ses_")
	const wantLen = 26 // 10 timestamp + 16 random
	if len(body) != wantLen {
		t.Fatalf("id body length = %d, want %d (id=%q)", len(body), wantLen, id)
	}
	// Must be filename-safe (SanitizeFilename keeps it unchanged).
	if got := SanitizeFilename(id); got != id {
		t.Fatalf("SanitizeFilename changed the id: %q -> %q", id, got)
	}
}

// TestGenerateSessionIDOrdering asserts two IDs generated in chronological order sort so the
// NEWER one comes first lexicographically (descending), which makes List fall out of string sort.
func TestGenerateSessionIDOrdering(t *testing.T) {
	t0 := time.Date(2026, 6, 21, 12, 0, 0, 0, time.UTC)
	t1 := t0.Add(time.Hour) // one hour later
	older := generateSessionIDAt(t0)
	newer := generateSessionIDAt(t1)
	if !(older > newer) {
		t.Fatalf("expected older(%q) > newer(%q) for descending order", older, newer)
	}
}

// TestGenerateSessionIDRandomSuffixUniqueness ensures same-millisecond IDs still differ via the
// random suffix (collision probability over 2 samples is astronomically low).
func TestGenerateSessionIDRandomSuffixUniqueness(t *testing.T) {
	now := time.Now()
	a := generateSessionIDAt(now)
	b := generateSessionIDAt(now)
	if a == b {
		t.Fatalf("two same-ms IDs collided: %q", a)
	}
	// The timestamp prefix (first ses_+10 chars) should be identical for same ms.
	if a[:14] != b[:14] {
		t.Fatalf("same-ms timestamp prefix differs: %q vs %q", a[:14], b[:14])
	}
}

// TestDefaultSessionTitle checks the placeholder format the UI detects.
func TestDefaultSessionTitle(t *testing.T) {
	now := time.Date(2026, 6, 21, 12, 30, 0, 0, time.UTC)
	got := DefaultSessionTitle(now)
	if !strings.HasPrefix(got, "New session - ") {
		t.Fatalf("default title %q missing prefix", got)
	}
	if !IsDefaultSessionTitle(got) {
		t.Fatalf("IsDefaultSessionTitle(%q) = false, want true", got)
	}
	if IsDefaultSessionTitle("My custom title") {
		t.Fatalf("IsDefaultSessionTitle should be false for custom titles")
	}
}

// TestDeriveTitle covers first-user-message derivation, multiline truncation, fallback, and the
// rune-based truncation length.
func TestDeriveTitle(t *testing.T) {
	now := time.Now()
	t.Run("first_user_message", func(t *testing.T) {
		msgs := []Message{
			AssistantMessage("hi"),
			UserMessage("Fix the bug in parser.go"),
		}
		if got := DeriveTitle(msgs, now); got != "Fix the bug in parser.go" {
			t.Fatalf("DeriveTitle = %q, want the user text", got)
		}
	})
	t.Run("multiline_uses_first_line", func(t *testing.T) {
		msgs := []Message{UserMessage("First line\nsecond line")}
		if got := DeriveTitle(msgs, now); got != "First line" {
			t.Fatalf("DeriveTitle = %q, want first line only", got)
		}
	})
	t.Run("truncates_long", func(t *testing.T) {
		long := strings.Repeat("a", 200)
		msgs := []Message{UserMessage(long)}
		got := DeriveTitle(msgs, now)
		if !strings.HasSuffix(got, "...") {
			t.Fatalf("expected truncated title to end with ..., got %q", got)
		}
		// "..." + 60 runes = 63 runes total.
		if rc := len([]rune(got)); rc != 63 {
			t.Fatalf("truncated title rune count = %d, want 63", rc)
		}
	})
	t.Run("fallback_when_no_user", func(t *testing.T) {
		msgs := []Message{AssistantMessage("hello")}
		got := DeriveTitle(msgs, now)
		if !IsDefaultSessionTitle(got) {
			t.Fatalf("expected default title fallback, got %q", got)
		}
	})
	t.Run("fallback_when_empty", func(t *testing.T) {
		got := DeriveTitle(nil, now)
		if !IsDefaultSessionTitle(got) {
			t.Fatalf("expected default title for nil msgs, got %q", got)
		}
	})
}
