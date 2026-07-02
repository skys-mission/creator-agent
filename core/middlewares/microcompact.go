package middlewares

import (
	"context"
	"fmt"

	"github.com/skys-mission/creator-agent/core"
)

// MicroCompact performs structural rewrite in BeforeModel (no model call):
// replaces older tool results (RoleTool messages) with short stubs, keeping recent results intact.
//
// Differences from Summarization (autoCompact):
//   - triggers early (low threshold), no model call (zero cost), only clears tool_result content (preserves user/assistant messages);
//   - Summarization triggers late (high threshold), calls a model, rewrites the entire conversation history.
//
// Algorithm: keep the last KeepRecent messages unchanged; for messages before that,
// if a RoleTool message's Content exceeds MaxToolResultChars, replace it with a stub
// ("[tool result truncated: <name>]").
type MicroCompact struct {
	core.BaseMiddleware

	// Threshold is the total character count that triggers cleanup. Default 8000.
	Threshold int
	// KeepRecent is the number of recent messages to preserve unchanged. Default 8.
	KeepRecent int
	// MaxToolResultChars is the per-tool-result length threshold for cleanup. Default 500.
	MaxToolResultChars int
}

// NewMicroCompact creates a MicroCompact with defaults.
func NewMicroCompact() *MicroCompact {
	return &MicroCompact{
		Threshold:          8000,
		KeepRecent:         8,
		MaxToolResultChars: 500,
	}
}

// BeforeModel cleans up old tool results that are too long before the model call.
func (m *MicroCompact) BeforeModel(_ context.Context, st *core.RunState) error {
	if len(st.Messages) <= m.KeepRecent {
		return nil
	}
	total := core.EstimateChars(st.Messages)
	if total < m.Threshold {
		return nil
	}

	// Compute the "recent" window with the same pair-aware boundary that Summarization uses
	// (core.PartitionForCompact), so both layers agree on what counts as recent and never stub a
	// tool result whose sibling in the same tool-call batch stays intact. The leading system message
	// is excluded from partitioning (it is never compacted) and its offset re-added afterward.
	offset := 0
	rest := st.Messages
	if len(rest) > 0 && rest[0].Role == core.RoleSystem {
		offset = 1
		rest = rest[1:]
	}
	toCompact, _ := core.PartitionForCompact(rest, m.KeepRecent)
	cutoff := offset + len(toCompact)
	for i := 0; i < cutoff; i++ {
		msg := st.Messages[i]
		if msg.Role != core.RoleTool {
			continue
		}
		if len(msg.Content) <= m.MaxToolResultChars {
			continue
		}
		// Replace with stub (keep ToolCallID/ToolName for model correlation)
		name := msg.ToolName
		if name == "" {
			name = "tool"
		}
		st.Messages[i].Content = fmt.Sprintf("[tool result truncated: %s, was %d chars]", name, len(msg.Content))
	}
	// Idempotent: multiple BeforeModel calls are safe (already-truncated stubs fall under MaxToolResultChars).
	return nil
}

// Compile-time check that MicroCompact implements Middleware.
var _ core.Middleware = (*MicroCompact)(nil)
