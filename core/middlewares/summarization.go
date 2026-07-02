// Package middlewares provides built-in middleware implementations for creator-agent.
package middlewares

import (
	"context"

	"github.com/skys-mission/creator-agent/core"
)

// Summarization compresses old messages when the conversation history grows too long, preventing context overflow.
//
// Trigger: when the estimated token count exceeds Threshold, older messages (keeping system + recent KeepRecent)
// are fed to a model to generate a structured summary, which replaces the old messages.
type Summarization struct {
	core.BaseMiddleware // other hooks are no-ops

	Model      core.ModelProvider // model used to generate the summary (usually the same as the agent's)
	Threshold  int                // token threshold that triggers compression (rough estimate, default 24000)
	KeepRecent int                // number of recent messages to preserve during compression (default 6)
}

var _ core.Middleware = (*Summarization)(nil)

// NewSummarization creates a Summarization middleware with default settings.
func NewSummarization(model core.ModelProvider) *Summarization {
	return &Summarization{
		Model:      model,
		Threshold:  24000,
		KeepRecent: 6,
	}
}

// BeforeModel checks the token count before each model call and compresses history if it exceeds the threshold.
//
// Failure policy: predictive summarization is best-effort. If the summary model call fails (transient
// outage, rate limit), this degrades to a no-op and lets the turn proceed on the uncompressed history
// rather than aborting the whole agent run. The reactive OnError path (Reactive middleware) remains the
// hard backstop that must compress when the provider actually rejects an over-long context.
func (s *Summarization) BeforeModel(ctx context.Context, st *core.RunState) error {
	if s.Model == nil {
		return nil
	}
	if core.EstimateTokens(st.Messages) < s.Threshold {
		return nil
	}
	// Respect interruption: if the user canceled, surface it so the loop finishes cleanly.
	if ctx.Err() != nil {
		return nil
	}
	out, compressed, err := compressHistory(ctx, s.Model, st.Messages, s.KeepRecent, core.DefaultSummarizePrompt)
	if err != nil {
		core.Warnf("predictive summarization failed, proceeding without compaction (reactive path will retry if the provider rejects the context): %v", err)
		return nil
	}
	if !compressed {
		return nil // nothing compressible after pair expansion
	}
	st.Messages = out
	return nil
}
