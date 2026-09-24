package contract

import "encoding/json"

// ToolInfo describes tool metadata and capabilities.
//
// Capability declarations use fail-closed defaults (zero value = most conservative):
// not declaring ReadOnly means write; not declaring ConcurrencySafe means non-parallel.
// The TUI renders these and the approval UI keys off them.
type ToolInfo struct {
	Name        string
	Description string
	InputSchema json.RawMessage // JSON Schema for the model

	// ===== Capability declarations (fail-closed) =====
	ReadOnly          bool              // default false (treated as write)
	ConcurrencySafe   bool              // default false (treated as non-parallel)
	Destructive       bool              // default false
	InterruptBehavior InterruptBehavior // default Cancel
	MaxResultChars    int               // >0 spills to disk, returning only a preview + file path
}

// InterruptBehavior defines how a tool responds to user interruption.
type InterruptBehavior int

const (
	// InterruptCancel (default): the tool is canceled on user interrupt.
	InterruptCancel InterruptBehavior = iota
	// InterruptBlock: block until completion, ignoring interrupt (for critical operations that must
	// not be interrupted).
	InterruptBlock
)

// ToolResult is the outcome of a tool execution.
//
// Error handling (dual track). Both tracks feed the failure back to the model so it can adapt
// (retry, pick another file, try a different command) rather than aborting the whole run — an
// agent's core value is recovering from errors, so a single failing tool must not kill the session:
//   - IsError=true (with a nil error return): expected business error (file not found, command
//     failed, permission denied). Preferred form; all built-in tools use this.
//   - Exec returns error != nil: unexpected system/execution error (e.g. a transport failure or a
//     tool panic downgraded by the loop). Surfaced to the model as an "Error: ..." tool result and
//     marked ToolIsError; the loop continues.
//
// The one exception is cancellation: context.Canceled / context.DeadlineExceeded propagate through
// ctx and finish the run as FinishCanceled (handled by the loop), not as a recoverable tool result.
type ToolResult struct {
	Content string // text for the model
	Parts   []Part // optional images/files
	IsError bool   // business error (distinct from system error)
}
