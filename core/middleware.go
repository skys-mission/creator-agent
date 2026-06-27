package core

import (
	"context"
	"encoding/json"
)

// Middleware intercepts and rewrites the agent loop. Implements compaction, permission, and memory.
// Hook model: BeforeAgent / BeforeModel / AfterModel / WrapTool / AfterAgent / OnError.
// All types are Core types with no dependency on any underlying SDK.
type Middleware interface {
	BeforeAgent(ctx context.Context, s *RunState) error
	AfterAgent(ctx context.Context, s *RunState) error
	BeforeModel(ctx context.Context, s *RunState) error
	AfterModel(ctx context.Context, s *RunState) error
	WrapTool(name string, next ToolFunc) ToolFunc
	// (meaning "handled"), so the loop retries the current turn. Returning a non-nil error preserves the abort path.
	// Contract: when returning a non-nil error, it must either be the original err or wrap it with %w.
	// retry/classification logic that relies on errors.As.
	OnError(ctx context.Context, s *RunState, err error) error
}

// ToolFunc is the tool execution function wrapped by middleware.
type ToolFunc func(ctx context.Context, input json.RawMessage) (ToolResult, error)

// RunState is the mutable state of the agent loop; middleware can read and write it.
// Modify Messages for compaction / context epoch; modify Tools for dynamic pruning.
type RunState struct {
	Messages []Message
	Tools    []ToolInfo
	Step     int
}

// BaseMiddleware is a no-op Middleware implementation.
// Custom middlewares embed it and override only the methods they need.
type BaseMiddleware struct{}

func (BaseMiddleware) BeforeAgent(context.Context, *RunState) error { return nil }
func (BaseMiddleware) AfterAgent(context.Context, *RunState) error  { return nil }
func (BaseMiddleware) BeforeModel(context.Context, *RunState) error { return nil }
func (BaseMiddleware) AfterModel(context.Context, *RunState) error  { return nil }
func (BaseMiddleware) WrapTool(_ string, next ToolFunc) ToolFunc    { return next }

// OnError default no-op: pass the error through unchanged so the next middleware or the loop decides.
func (BaseMiddleware) OnError(_ context.Context, _ *RunState, err error) error { return err }
