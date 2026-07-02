package shared

import (
	"errors"
	"testing"

	"github.com/skys-mission/creator-agent/core"
)

// TestClassifyStatus is the cross-adapter parity contract for error classification: all three
// provider adapters (openai-chat / openai-responses / anthropic) funnel their SDK errors through
// ClassifyStatus, so the retry/reactive layers see identical core error types for the same HTTP
// status regardless of vendor.
func TestClassifyStatus(t *testing.T) {
	base := errors.New("boom")

	if got := ClassifyStatus(nil, 500); got != nil {
		t.Errorf("nil error must stay nil, got %v", got)
	}

	// 429 -> RateLimitedError (retryable).
	var rl *core.RateLimitedError
	if got := ClassifyStatus(base, 429); !errors.As(got, &rl) || rl.StatusCode != 429 {
		t.Errorf("429 should map to RateLimitedError, got %T", ClassifyStatus(base, 429))
	}

	// 5xx -> ServerError (retryable).
	var se *core.ServerError
	for _, s := range []int{500, 502, 503} {
		if got := ClassifyStatus(base, s); !errors.As(got, &se) {
			t.Errorf("%d should map to ServerError, got %T", s, got)
		}
	}

	// 4xx (excluding 429) -> ClientError (not retryable).
	var ce *core.ClientError
	for _, s := range []int{400, 401, 403, 404, 422} {
		if got := ClassifyStatus(base, s); !errors.As(got, &ce) {
			t.Errorf("%d should map to ClientError, got %T", s, got)
		}
	}

	// Unknown/zero status: pass through unchanged.
	if got := ClassifyStatus(base, 0); got != base {
		t.Errorf("unknown status should return the original error, got %v", got)
	}
	if got := ClassifyStatus(base, 200); got != base {
		t.Errorf("2xx should return the original error, got %v", got)
	}
}
