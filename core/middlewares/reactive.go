package middlewares

import (
	"context"
	"errors"
	"strings"
	"sync"

	"github.com/skys-mission/creator-agent/core"
)

// errNothingToCompact means the history is too short to compact. OnError returns the original error (counting toward the circuit breaker).
var errNothingToCompact = errors.New("nothing to compact")

// Reactive performs "reactive" compaction when a model call fails (OnError hook):
// detects context-length errors (request too long) → uses a model to aggressively summarize and rewrite messages → returns nil (handled), loop retries.
//
// Differences from autoCompact (Summarization, BeforeModel proactive trigger):
//   - autoCompact is "predictive" — compresses before the context is likely to overflow;
//   - reactive is "reactive" — only compacts after an error has occurred, more aggressive (KeepRecent reduced to the minimum).
//
// Circuit breaker: stops intervening after maxConsecutive consecutive failures to avoid infinite retry loops.
// Use case: some errors are not caused by context length (e.g., auth failures), so intervention is pointless and masks the real error.
type Reactive struct {
	core.BaseMiddleware

	Model          core.ModelProvider // model used for summarization rewrite
	KeepRecent     int                // aggressive compaction: keep only the last N messages (default 2)
	maxConsecutive int                // circuit breaker threshold (default 2)

	mu          sync.Mutex
	consecutive int // consecutive intervention failure counter
}

// ReactiveOption configures Reactive.
type ReactiveOption func(*Reactive)

// WithMaxConsecutive sets the circuit breaker threshold (default 2).
func WithMaxConsecutive(n int) ReactiveOption {
	return func(r *Reactive) {
		if n > 0 {
			r.maxConsecutive = n
		}
	}
}

// NewReactive creates a Reactive middleware.
func NewReactive(model core.ModelProvider, opts ...ReactiveOption) *Reactive {
	r := &Reactive{
		Model:          model,
		KeepRecent:     2,
		maxConsecutive: 2,
	}
	for _, o := range opts {
		o(r)
	}
	return r
}

// OnError intercepts model errors.
func (r *Reactive) OnError(ctx context.Context, st *core.RunState, err error) error {
	if err == nil {
		return nil
	}
	// Only intervene on context-length errors
	if !isContextLimitError(err) {
		return err
	}
	// Circuit breaker: give up if the threshold is exceeded
	r.mu.Lock()
	if r.consecutive >= r.maxConsecutive {
		r.mu.Unlock()
		return err
	}
	r.mu.Unlock()

	// Aggressive compaction: summarize older history with a model, keeping system + KeepRecent + summary
	newMsgs, compErr := r.aggressiveCompact(ctx, st.Messages)
	if compErr != nil {
		// Compaction failed: count toward the circuit breaker, return the original error
		r.mu.Lock()
		r.consecutive++
		r.mu.Unlock()
		return err
	}
	st.Messages = newMsgs
	// Success: reset counter, return nil (handled, loop will retry)
	r.mu.Lock()
	r.consecutive = 0
	r.mu.Unlock()
	return nil
}

// aggressiveCompact summarizes older history into a single message, keeping system + summary + recent.
// Nothing to compress is reported as errNothingToCompact so OnError counts it toward the circuit breaker.
func (r *Reactive) aggressiveCompact(ctx context.Context, msgs []core.Message) ([]core.Message, error) {
	out, compressed, err := compressHistory(ctx, r.Model, msgs, r.KeepRecent, reactiveSummarizePrompt)
	if err != nil {
		return nil, err
	}
	if !compressed {
		return nil, errNothingToCompact
	}
	return out, nil
}

// isContextLimitError checks whether an error is a context-length error (heuristic keyword matching).
//
// Provider error strings vary widely. Covered keywords:
//   - "context length" / "context window" / "maximum context"
//   - "too long" / "too large"
//   - "token limit" / "tokens exceeded"
//   - HTTP 413 (Payload Too Large)
func isContextLimitError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	keywords := []string{
		"context length", "context window", "maximum context",
		"too long", "too large",
		"token limit", "tokens exceeded", "token count",
		"413", "payload too large",
		"request too large",
	}
	for _, kw := range keywords {
		if strings.Contains(msg, kw) {
			return true
		}
	}
	return false
}

// reactiveSummarizePrompt is the prompt used for reactive compaction (more aggressive, emphasizes keeping key information).
const reactiveSummarizePrompt = `Summarize the conversation above extremely concisely.
Preserve: goals, key decisions, file paths mentioned, errors encountered, the current task.
Drop: tool output details, exploratory chatter, redundant context.
Output only the summary, no preamble. This is an emergency compaction to fit the context window.`

// Compile-time check that Reactive implements Middleware.
var _ core.Middleware = (*Reactive)(nil)
