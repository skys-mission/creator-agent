package builtins

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/skys-mission/creator-agent/core"
)

// todo.go provides a todo list feature: lets the agent track progress across multi-step tasks, giving users visibility.
//
// Design trade-offs (kept simple so capability-limited models are less likely to make mistakes):
//   - Single tool todo_write with replacement-style full overwrite: the model passes the complete list each time,
//     no need to remember "change item N".
//   - Minimal schema: todos[].{content, status}, with a four-state enum (planned/pending/in_progress/completed).
//   - Fewer fields + fewer enum values reduce the model's error rate in state management.
//
// Persistence: TodoStore is in-memory (per-session), lives for the session duration, /clear resets it.

// TodoStatus is the status of a todo item.
type TodoStatus string

const (
	// TodoPlanned means listed in the plan but not yet committed — used for "planning / exploring / feasibility assessment" scenarios.
	// When the model confirms it will do the work, it transitions to pending. Distinct from pending (already queued).
	TodoPlanned    TodoStatus = "planned"
	TodoPending    TodoStatus = "pending"
	TodoInProgress TodoStatus = "in_progress"
	TodoCompleted  TodoStatus = "completed"
)

// Defensive caps on a single todo_write payload. Generous enough that no realistic agent plan is
// affected, but they bound memory and keep the returned summary readable if a model emits a runaway
// list. Exceeding either limit truncates and is surfaced in the result.
const (
	maxTodoItems        = 200
	maxTodoContentChars = 1000
)

// TodoItem is a single entry in the todo list.
type TodoItem struct {
	Content string     `json:"content"`
	Status  TodoStatus `json:"status"`
}

// isValid reports whether the status is a recognized enum value (invalid values fall back to pending, fault-tolerant).
func (s TodoStatus) isValid() bool {
	return s == TodoPlanned || s == TodoPending || s == TodoInProgress || s == TodoCompleted
}

// TodoStore is a session-level in-memory todo list (thread-safe).
// Isolated by sessionID — TUI uses "repl", headless uses empty (no storage).
type TodoStore struct {
	mu   sync.Mutex
	list map[string][]TodoItem
}

// NewTodoStore creates an empty TodoStore.
func NewTodoStore() *TodoStore {
	return &TodoStore{list: make(map[string][]TodoItem)}
}

// Get returns the todo list for the given session (nil if not present).
func (s *TodoStore) Get(sessionID string) []TodoItem {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.list[sessionID]
}

// Set replaces the todo list for the given session (full overwrite).
func (s *TodoStore) Set(sessionID string, items []TodoItem) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.list[sessionID] = items
}

// Clear removes the todo list for the given session (used by /clear).
func (s *TodoStore) Clear(sessionID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.list, sessionID)
}

// TodoTool is the todo list management tool (todo_write): replaces the current session's todo list with a full overwrite.
//
// The model passes the complete list on each call (not incremental updates), reducing the error rate from
// misremembering item indices. Data is stored in TodoStore (session-level), not in session messages.
type TodoTool struct {
	store *TodoStore
}

// NewTodoTool creates a TodoTool. store is the session-level storage (main.go constructs one instance shared by all sessions).
func NewTodoTool(store *TodoStore) *TodoTool {
	return &TodoTool{store: store}
}

// todoSessionKey extracts sessionID from ctx (falls back to default "repl").
// core.ctx currently does not carry sessionID (StreamInput.SessionID is consumed at the agent layer, not in ctx),
// so the first version uses a fixed "repl" (consistent with TUI). Child session (task) isolation is handled
// by excluding todo_write from the child toolset.
const todoDefaultSession = "repl"

func (t *TodoTool) Info() core.ToolInfo {
	return core.ToolInfo{
		Name:        "todo_write",
		Description: "Manage the task progress list for the current session. Input the FULL list each time (replacement, not incremental). Use this for multi-step tasks (>=3 steps) to track progress. Each item has a status: planned / pending / in_progress / completed. Only ONE item should be in_progress at a time. Update the list whenever a step completes. Do NOT use this for simple tasks (<3 steps).",
		InputSchema: json.RawMessage(`{
  "type": "object",
  "properties": {
    "todos": {
      "type": "array",
      "description": "The full todo list (replaces the existing list).",
      "items": {
        "type": "object",
        "properties": {
          "content": {"type": "string", "description": "the task description"},
          "status": {"type": "string", "enum": ["planned", "pending", "in_progress", "completed"], "description": "planned=proposed but not committed to do (just a plan/suggestion); pending=confirmed to do, queued; in_progress=working on it; completed=done"}
        },
        "required": ["content", "status"]
      }
    }
  },
  "required": ["todos"]
}`),
		// Has side effects (modifies store), but safe (only memory, no files/commands).
		// Not declared ReadOnly — config permissions allow list will add "todo_write" for auto-approval.
		ReadOnly:          false,
		ConcurrencySafe:   false, // serialized to avoid concurrent replacement races
		InterruptBehavior: core.InterruptCancel,
	}
}

// Exec parses the full list, replaces it in the store, and returns a confirmation + current list summary.
func (t *TodoTool) Exec(ctx context.Context, input json.RawMessage) (core.ToolResult, error) {
	var args struct {
		Todos []struct {
			Content string `json:"content"`
			Status  string `json:"status"`
		} `json:"todos"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return invalidInputResult(err), nil
	}

	// Convert to TodoItem (fault-tolerant: invalid status falls back to pending; empty content is skipped).
	// Defensive caps bound a runaway payload: over-long content is truncated, excess items are dropped.
	capped := false
	items := make([]TodoItem, 0, len(args.Todos))
	for i, td := range args.Todos {
		content := strings.TrimSpace(td.Content)
		if content == "" {
			continue
		}
		if r := []rune(content); len(r) > maxTodoContentChars {
			content = string(r[:maxTodoContentChars])
			capped = true
		}
		status := TodoStatus(td.Status)
		if !status.isValid() {
			status = TodoPending // fault-tolerant: invalid status falls back to pending
		}
		items = append(items, TodoItem{Content: content, Status: status})
		if len(items) >= maxTodoItems {
			// Flag capped only when a later non-empty item would actually be dropped —
			// reaching the exact limit with nothing overflowed is not a cap.
			for _, rem := range args.Todos[i+1:] {
				if strings.TrimSpace(rem.Content) != "" {
					capped = true
					break
				}
			}
			break
		}
	}

	t.store.Set(todoDefaultSession, items)

	// Return a summary to help the model confirm the write succeeded and see the list state.
	// planned is counted separately to remind the model "some items are still just plans", prompting it to decide whether to proceed.
	done, planned, total := 0, 0, len(items)
	for _, it := range items {
		switch it.Status {
		case TodoCompleted:
			done++
		case TodoPlanned:
			planned++
		}
	}
	summary := fmt.Sprintf("todo list updated (%d/%d completed", done, total)
	if planned > 0 {
		summary += fmt.Sprintf(", %d planned", planned)
	}
	summary += "):\n" + formatTodoSummary(items)
	if capped {
		summary += fmt.Sprintf("\n(note: list capped to %d items, %d chars/item)", maxTodoItems, maxTodoContentChars)
	}
	return core.ToolResult{Content: summary}, nil
}

// formatTodoSummary formats the todo list into compact text (tool return + TUI fallback).
func formatTodoSummary(items []TodoItem) string {
	if len(items) == 0 {
		return "(empty)"
	}
	var sb strings.Builder
	for _, it := range items {
		sym := "□"
		switch it.Status {
		case TodoPlanned:
			sym = "○"
		case TodoInProgress:
			sym = "◐"
		case TodoCompleted:
			sym = "◒"
		}
		fmt.Fprintf(&sb, "%s %s\n", sym, it.Content)
	}
	return strings.TrimRight(sb.String(), "\n")
}

// Ensure core.Tool implementation.
var _ core.Tool = (*TodoTool)(nil)
