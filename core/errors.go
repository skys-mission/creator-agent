package core

import (
	"errors"
	"fmt"
	"os"
)

// This file defines classified LLM errors for targeted handling by upper layers (reactive middleware / retry logic).
// Adapters (e.g., adapters/openai) map underlying SDK errors to these types; core and upper layers only recognize these types,
// keeping the core free of any SDK dependency.

// RateLimitedError indicates rate limiting (HTTP 429). Upper layers may back off and retry.
type RateLimitedError struct {
	Err        error
	StatusCode int
}

func (e *RateLimitedError) Error() string {
	return fmt.Sprintf("rate limited (%d): %v", e.StatusCode, e.Err)
}
func (e *RateLimitedError) Unwrap() error { return e.Err }

// ServerError indicates a server-side error (HTTP 5xx). Usually transient and retryable.
type ServerError struct {
	Err        error
	StatusCode int
}

func (e *ServerError) Error() string {
	return fmt.Sprintf("server error (%d): %v", e.StatusCode, e.Err)
}
func (e *ServerError) Unwrap() error { return e.Err }

// ClientError indicates a client-side error (HTTP 4xx excluding 429, e.g., auth failure or bad request). Usually not retryable.
type ClientError struct {
	Err        error
	StatusCode int
}

func (e *ClientError) Error() string {
	return fmt.Sprintf("client error (%d): %v", e.StatusCode, e.Err)
}
func (e *ClientError) Unwrap() error { return e.Err }

// UserHint converts a classified error into a user-readable, actionable hint string (trailing space for direct concatenation with the raw error).
// Unknown errors return a neutral "[Error] ". Used by upper layers (TUI error bar / dumb REPL output) to keep error presentation consistent
// so users know what to do next instead of seeing only technical errors.
func UserHint(err error) string {
	if err == nil {
		return ""
	}
	var rl *RateLimitedError
	var se *ServerError
	var ce *ClientError
	switch {
	case errors.As(err, &rl):
		return "[Rate limit] Service is busy, wait a few seconds and retry (auto-retried already): "
	case errors.As(err, &se):
		return "[Server error] Upstream temporarily unavailable, retry or switch profile (/model): "
	case errors.As(err, &ce):
		switch ce.StatusCode {
		case 401:
			return "[Auth failed] API key is invalid, check configuration (most likely missing or expired key): "
		case 403:
			return "[Forbidden] This key has no access to the model/endpoint, check profile model/base_url: "
		default:
			return fmt.Sprintf("[Request error] Request rejected (%d), check configuration: ", ce.StatusCode)
		}
	}
	return "[Error] "
}

// Warnf writes a best-effort warning to stderr (non-critical paths: persistence/memory failures).
func Warnf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "warn: "+format+"\n", args...)
}
