// Package middlewares provides built-in middleware implementations for creator-agent.
package middlewares

import (
	"context"
	"fmt"

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
func (s *Summarization) BeforeModel(ctx context.Context, st *core.RunState) error {
	if s.Model == nil {
		return nil
	}
	if core.EstimateTokens(st.Messages) < s.Threshold {
		return nil
	}
	out, compressed, err := compressHistory(ctx, s.Model, st.Messages, s.KeepRecent, summarizePrompt)
	if err != nil {
		return fmt.Errorf("summarize history: %w", err)
	}
	if !compressed {
		return nil // nothing compressible after pair expansion
	}
	st.Messages = out
	return nil
}

const summarizePrompt = `Concisely summarize the conversation above. Preserve:
- The user's goal(s) and any constraints
- Key decisions and their reasons
- File names/paths that were read or modified
- Errors encountered and how they were resolved
- Current task state and any pending next step
Be brief (a few short bullets). Do not include full file contents.`
