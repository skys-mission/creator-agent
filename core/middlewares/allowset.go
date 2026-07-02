package middlewares

import "sync"

// AllowSet is a thread-safe, session-level set of approved tool invocations. It is shared by the
// REPL and TUI approvers so both apply identical "remember this approval" semantics instead of each
// re-implementing the map + write/edit grouping (which drifted apart historically).
//
// Keys are ApproveKey(tool, input). The two file-writing tools write and edit are treated as one
// group: approving "all edits" for either allows both, matching users' mental model that "let it
// edit files" is a single decision.
type AllowSet struct {
	mu sync.Mutex
	m  map[string]bool
}

// NewAllowSet returns an empty AllowSet.
func NewAllowSet() *AllowSet {
	return &AllowSet{m: make(map[string]bool)}
}

// Allowed reports whether this exact invocation (or the write/edit group, for write/edit tools) has
// been remembered.
func (a *AllowSet) Allowed(toolName, input string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.m[ApproveKey(toolName, input)] {
		return true
	}
	if isWriteEditTool(toolName) && (a.m["write"] || a.m["edit"]) {
		return true
	}
	return false
}

// RememberExact remembers this specific invocation by its ApproveKey (no grouping).
func (a *AllowSet) RememberExact(toolName, input string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.m[ApproveKey(toolName, input)] = true
}

// RememberWriteEdit remembers the write/edit group so all subsequent write and edit calls are allowed.
func (a *AllowSet) RememberWriteEdit() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.m["write"] = true
	a.m["edit"] = true
}

// isWriteEditTool reports whether the tool is one of the grouped file-writing tools.
func isWriteEditTool(toolName string) bool {
	return toolName == "write" || toolName == "edit"
}
