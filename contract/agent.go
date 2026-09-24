package contract

import "context"

// Agent is the core executor.
//
// Stream runs with session-aware multi-turn memory; ClearSession wipes history (used by /clear).
// The TUI holds an Agent behind this interface and never sees the implementation.
type Agent interface {
	Stream(ctx context.Context, in StreamInput) (<-chan Event, error)
	ClearSession(sessionID string)
}

// StreamInput is the input for a single execution.
type StreamInput struct {
	Messages  []Message
	SessionID string
}
