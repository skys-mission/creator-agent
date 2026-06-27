package core

// userhint_test.go covers core.UserHint classification logic -- it is the single source of truth
// for TUI error bars and dumb REPL error output, so the hint text for each category must be pinned.

import (
	"errors"
	"strings"
	"testing"
)

func TestUserHintRateLimited(t *testing.T) {
	got := UserHint(&RateLimitedError{StatusCode: 429})
	if !strings.Contains(got, "Rate limit") || !strings.Contains(got, "retry") {
		t.Errorf("429 hint should mention rate limit/retry, got %q", got)
	}
}

func TestUserHintServerError(t *testing.T) {
	got := UserHint(&ServerError{StatusCode: 503})
	if !strings.Contains(got, "Server error") {
		t.Errorf("5xx hint should mention server error, got %q", got)
	}
}

func TestUserHintAuth(t *testing.T) {
	got := UserHint(&ClientError{StatusCode: 401})
	if !strings.Contains(got, "Auth failed") || !strings.Contains(got, "key") {
		t.Errorf("401 hint should mention auth failed/key, got %q", got)
	}
}

func TestUserHintForbidden(t *testing.T) {
	got := UserHint(&ClientError{StatusCode: 403})
	if !strings.Contains(got, "Forbidden") {
		t.Errorf("403 hint should mention forbidden, got %q", got)
	}
}

func TestUserHintOtherClient(t *testing.T) {
	got := UserHint(&ClientError{StatusCode: 400})
	if !strings.Contains(got, "Request error") || !strings.Contains(got, "400") {
		t.Errorf("4xx(other) hint should mention request error + code, got %q", got)
	}
}

func TestUserHintGeneric(t *testing.T) {
	got := UserHint(errors.New("something broke"))
	if !strings.Contains(got, "Error") {
		t.Errorf("generic hint should be neutral, got %q", got)
	}
}

func TestUserHintNil(t *testing.T) {
	if got := UserHint(nil); got != "" {
		t.Errorf("nil error should give empty hint, got %q", got)
	}
}

// TestUserHintEndsWithSeparator verifies: all hints end with a separator so callers can concatenate the raw error directly.
func TestUserHintEndsWithSeparator(t *testing.T) {
	errs := []error{
		&RateLimitedError{StatusCode: 429},
		&ServerError{StatusCode: 500},
		&ClientError{StatusCode: 401},
		&ClientError{StatusCode: 404},
		errors.New("x"),
	}
	for _, e := range errs {
		got := UserHint(e)
		if !strings.HasSuffix(got, ": ") && !strings.HasSuffix(got, "] ") {
			t.Errorf("hint for %v should end with separator for concatenation, got %q", e, got)
		}
	}
}
