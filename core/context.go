package core

import "context"

// sessionIDContextKey is the private key under which the active session ID is stored in the
// execution context. A dedicated unexported type prevents collisions with keys from other packages.
type sessionIDContextKey struct{}

// WithSessionID returns a copy of ctx carrying the session ID. The agent sets this before running a
// turn so session-scoped tools (e.g. todo_write) can isolate their state per session instead of
// sharing one global bucket. An empty id leaves ctx unchanged.
func WithSessionID(ctx context.Context, id string) context.Context {
	if id == "" {
		return ctx
	}
	return context.WithValue(ctx, sessionIDContextKey{}, id)
}

// SessionIDFromContext returns the session ID stored in ctx, or "" when none is set (e.g. stateless
// headless runs). Tools that need a fallback bucket should treat "" as their own default.
func SessionIDFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	id, _ := ctx.Value(sessionIDContextKey{}).(string)
	return id
}
