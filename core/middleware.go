package core

import (
	"context"
	"encoding/json"
)

// Middleware intercepts and rewrites the agent loop. It backs compaction, permission, and memory.
// All parameter/return types are core types with no dependency on any underlying SDK.
//
// Lifecycle hooks (invoked in this order per run; the loop wraps the chain so the first middleware
// is the outermost):
//
//   - BeforeAgent: once, before the loop starts. Seed system context or reject the run early.
//   - BeforeModel: before every model call. Rewrite Messages (compaction) or Tools (pruning).
//   - AfterModel:  after every model call. Observe/annotate the produced messages.
//   - WrapTool:    wraps each tool invocation (permission gating, hooks). Returns the next in the chain.
//   - AfterAgent:  once, after the loop ends. Post-run bookkeeping (e.g. memory extraction).
//   - OnError:     on a model-call error. Returning nil means "handled" and the loop retries the
//     current turn; returning non-nil preserves the abort path and MUST be the original err or wrap
//     it with %w so upstream retry/classification via errors.As keeps working.
//
// Contract: every hook must respect ctx cancellation and must not panic (the loop recovers panics
// into an ErrorEvent, but middlewares should fail via returned errors, not panics).
type Middleware interface {
	BeforeAgent(ctx context.Context, s *RunState) error
	AfterAgent(ctx context.Context, s *RunState) error
	BeforeModel(ctx context.Context, s *RunState) error
	AfterModel(ctx context.Context, s *RunState) error
	WrapTool(name string, next ToolFunc) ToolFunc
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
