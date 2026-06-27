package builtins

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/skys-mission/creator-agent/core"
)

// ===== TodoStore =====

func TestTodoStoreGetSetClear(t *testing.T) {
	s := NewTodoStore()
	// Empty session -> nil
	if got := s.Get("repl"); got != nil {
		t.Errorf("empty Get = %v, want nil", got)
	}
	// Set
	items := []TodoItem{{Content: "a", Status: TodoPending}, {Content: "b", Status: TodoCompleted}}
	s.Set("repl", items)
	got := s.Get("repl")
	if len(got) != 2 || got[0].Content != "a" || got[1].Status != TodoCompleted {
		t.Errorf("Get after Set = %+v", got)
	}
	// Replacement Set (not append)
	s.Set("repl", []TodoItem{{Content: "x", Status: TodoInProgress}})
	if len(s.Get("repl")) != 1 {
		t.Errorf("Set should replace, got %d", len(s.Get("repl")))
	}
	// Clear
	s.Clear("repl")
	if got := s.Get("repl"); got != nil {
		t.Errorf("after Clear Get = %v, want nil", got)
	}
}

func TestTodoStoreSessionIsolation(t *testing.T) {
	s := NewTodoStore()
	s.Set("repl", []TodoItem{{Content: "a"}})
	s.Set("other", []TodoItem{{Content: "b"}})
	if len(s.Get("repl")) != 1 || s.Get("repl")[0].Content != "a" {
		t.Error("repl should be isolated from other")
	}
	if len(s.Get("other")) != 1 || s.Get("other")[0].Content != "b" {
		t.Error("other should be isolated from repl")
	}
}

func TestTodoStoreConcurrent(t *testing.T) {
	s := NewTodoStore()
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			s.Set("repl", []TodoItem{{Content: "x"}})
			_ = s.Get("repl")
		}(i)
	}
	wg.Wait() // passes if no race and no panic
}

// ===== TodoTool =====

func TestTodoToolExecReplaceAll(t *testing.T) {
	store := NewTodoStore()
	tool := NewTodoTool(store)
	input, _ := json.Marshal(map[string]any{
		"todos": []map[string]string{
			{"content": "step 1", "status": "completed"},
			{"content": "step 2", "status": "in_progress"},
			{"content": "step 3", "status": "pending"},
		},
	})
	res, err := tool.Exec(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Errorf("should not be error: %s", res.Content)
	}
	// store should have 3 items
	got := store.Get("repl")
	if len(got) != 3 {
		t.Fatalf("store should have 3 items, got %d", len(got))
	}
	if got[0].Status != TodoCompleted || got[1].Status != TodoInProgress || got[2].Status != TodoPending {
		t.Errorf("statuses wrong: %+v", got)
	}
	// returned summary should contain progress
	if !strings.Contains(res.Content, "1/3") {
		t.Errorf("result should show progress 1/3: %s", res.Content)
	}
}

func TestTodoToolExecEmpty(t *testing.T) {
	store := NewTodoStore()
	store.Set("repl", []TodoItem{{Content: "old"}}) // pre-populate
	tool := NewTodoTool(store)
	input, _ := json.Marshal(map[string]any{"todos": []map[string]string{}})
	res, err := tool.Exec(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	// empty list should clear the store
	if got := store.Get("repl"); len(got) != 0 {
		t.Errorf("empty todos should clear store, got %d", len(got))
	}
	if !strings.Contains(res.Content, "0/0") {
		t.Errorf("should show 0/0: %s", res.Content)
	}
}

func TestTodoToolExecInvalidStatus(t *testing.T) {
	store := NewTodoStore()
	tool := NewTodoTool(store)
	// invalid status -> falls back to pending (fault-tolerant)
	input := []byte(`{"todos":[{"content":"x","status":"bogus"}]}`)
	res, err := tool.Exec(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Errorf("invalid status should be tolerated, not error: %s", res.Content)
	}
	got := store.Get("repl")
	if len(got) != 1 || got[0].Status != TodoPending {
		t.Errorf("bogus status should become pending, got %+v", got)
	}
}

// planned is a valid status and should not fall back to pending; the summary should count planned separately.
func TestTodoToolExecPlannedStatus(t *testing.T) {
	store := NewTodoStore()
	tool := NewTodoTool(store)
	input, _ := json.Marshal(map[string]any{
		"todos": []map[string]string{
			{"content": "idea A", "status": "planned"},
			{"content": "idea B", "status": "planned"},
			{"content": "queued", "status": "pending"},
			{"content": "done one", "status": "completed"},
		},
	})
	res, err := tool.Exec(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Errorf("planned should be valid, not error: %s", res.Content)
	}
	got := store.Get("repl")
	if len(got) != 4 {
		t.Fatalf("store should have 4 items, got %d", len(got))
	}
	// planned items should remain planned, not fall back to pending
	if got[0].Status != TodoPlanned || got[1].Status != TodoPlanned {
		t.Errorf("planned items should stay planned, got %+v %+v", got[0], got[1])
	}
	// summary: 1 completed out of 4, planned counted separately as 2
	if !strings.Contains(res.Content, "1/4") {
		t.Errorf("summary should show 1/4 completed: %s", res.Content)
	}
	if !strings.Contains(res.Content, "2 planned") {
		t.Errorf("summary should count planned separately: %s", res.Content)
	}
	// progress bar only counts completed: planned/pending do not count as done
	if strings.Contains(res.Content, "4 planned") || strings.Contains(res.Content, "3 planned") {
		t.Errorf("planned count wrong: %s", res.Content)
	}
}

func TestTodoToolExecEmptyContent(t *testing.T) {
	store := NewTodoStore()
	tool := NewTodoTool(store)
	input := []byte(`{"todos":[{"content":"","status":"pending"},{"content":"real","status":"pending"}]}`)
	_, err := tool.Exec(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	got := store.Get("repl")
	if len(got) != 1 {
		t.Errorf("empty content should be skipped, got %d items", len(got))
	}
	if got[0].Content != "real" {
		t.Errorf("should keep 'real', got %q", got[0].Content)
	}
}

func TestTodoToolExecInvalidJSON(t *testing.T) {
	store := NewTodoStore()
	tool := NewTodoTool(store)
	res, err := tool.Exec(context.Background(), json.RawMessage(`{bad json`))
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Error("invalid JSON should be IsError")
	}
}

// TestTodoToolExecCaps verifies a runaway payload is bounded: item count and per-item content
// length are capped, and the result surfaces that it was capped.
func TestTodoToolExecCaps(t *testing.T) {
	store := NewTodoStore()
	tool := NewTodoTool(store)
	todos := make([]map[string]string, maxTodoItems+50)
	longContent := strings.Repeat("x", maxTodoContentChars+100)
	for i := range todos {
		todos[i] = map[string]string{"content": longContent, "status": "pending"}
	}
	input, _ := json.Marshal(map[string]any{"todos": todos})
	res, err := tool.Exec(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	got := store.Get("repl")
	if len(got) != maxTodoItems {
		t.Errorf("item count = %d, want capped to %d", len(got), maxTodoItems)
	}
	if n := len([]rune(got[0].Content)); n != maxTodoContentChars {
		t.Errorf("content runes = %d, want capped to %d", n, maxTodoContentChars)
	}
	if !strings.Contains(res.Content, "capped") {
		t.Errorf("result should note capping; got %q", res.Content)
	}
}

// TestTodoToolExecCapsAtLimitNotCapped verifies that exactly maxTodoItems valid items (each under the
// char limit) is NOT reported as capped — reaching the exact limit with nothing overflowed is not a
// cap. Regression guard for the off-by-one in the cap check.
func TestTodoToolExecCapsAtLimitNotCapped(t *testing.T) {
	store := NewTodoStore()
	tool := NewTodoTool(store)
	todos := make([]map[string]string, maxTodoItems)
	for i := range todos {
		todos[i] = map[string]string{"content": "ok", "status": "pending"}
	}
	input, _ := json.Marshal(map[string]any{"todos": todos})
	res, err := tool.Exec(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(store.Get("repl")); got != maxTodoItems {
		t.Errorf("item count = %d, want exactly %d (no overflow)", got, maxTodoItems)
	}
	if strings.Contains(res.Content, "capped") {
		t.Errorf("result at the exact limit should not be flagged capped; got %q", res.Content)
	}
}

func TestTodoToolInfo(t *testing.T) {
	tool := NewTodoTool(NewTodoStore())
	info := tool.Info()
	if info.Name != "todo_write" {
		t.Errorf("Name = %q", info.Name)
	}
	if info.ReadOnly {
		t.Error("todo_write should not be ReadOnly (has side effects)")
	}
	if info.ConcurrencySafe {
		t.Error("todo_write should not be ConcurrencySafe (serialized)")
	}
	// schema should be valid JSON and the status enum should contain all four states
	var schema map[string]any
	if err := json.Unmarshal(info.InputSchema, &schema); err != nil {
		t.Errorf("InputSchema invalid JSON: %v", err)
	}
	props := schema["properties"].(map[string]any)
	todos := props["todos"].(map[string]any)
	items := todos["items"].(map[string]any)
	itemProps := items["properties"].(map[string]any)
	statusField := itemProps["status"].(map[string]any)
	enum := statusField["enum"].([]any)
	got := map[string]bool{}
	for _, v := range enum {
		got[v.(string)] = true
	}
	for _, s := range []string{"planned", "pending", "in_progress", "completed"} {
		if !got[s] {
			t.Errorf("status enum missing %q: %v", s, enum)
		}
	}
}

func TestTodoStatusValid(t *testing.T) {
	if !TodoPlanned.isValid() || !TodoPending.isValid() || !TodoInProgress.isValid() || !TodoCompleted.isValid() {
		t.Error("all four statuses should be valid")
	}
	if TodoStatus("bogus").isValid() {
		t.Error("bogus status should be invalid")
	}
}

// Ensure core.Tool implementation.
var _ core.Tool = (*TodoTool)(nil)
