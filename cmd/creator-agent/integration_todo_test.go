//go:build integration

package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/skys-mission/creator-agent/core"
	"github.com/skys-mission/creator-agent/core/adapters/openai"
	"github.com/skys-mission/creator-agent/core/builtins"
)

// TestRealTodoWrite: real API verification that the model proactively uses todo_write for multi-step tasks.
// Give a clear multi-step task and verify the model calls todo_write to create a list (system prompt guidance takes effect).
func TestRealTodoWrite(t *testing.T) {
	env, ok := loadRealEnv()
	if !ok {
		t.Skip("CREATOR_AGENT_TEST_API_KEY not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	t.Cleanup(cancel)
	provider, err := openai.NewProvider(ctx, openai.Config{
		BaseURL: env.baseURL, APIKey: env.apiKey, Model: env.model, RequestTimeout: 80 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	todoStore := builtins.NewTodoStore()
	ag := core.NewAgent(provider,
		core.WithTools(builtins.NewTodoTool(todoStore), builtins.NewReadTool()),
		core.WithSystemPrompt(realTestSystemPrompt+`
Using todo_write: For multi-step tasks (3+ steps), FIRST call todo_write with the full plan. Update it as you progress.`),
	)
	r := runAgent(ctx, ag, "test-todo", "Please complete in three steps: 1. Explain what water is 2. Explain what fire is 3. Summarize. Use todo_write to track these three steps.")
	if r.err != nil {
		t.Fatalf("agent error: %v", r.err)
	}
	t.Logf("tools called=%d, text=%q", r.toolCalls(), r.text)
	// Verify the model actually called todo_write (store non-empty + each item content non-empty)
	items := todoStore.Get("repl")
	if len(items) == 0 {
		t.Error("model should have called todo_write to create the list; store is empty")
	} else {
		t.Logf("todo list (%d items):", len(items))
		for _, it := range items {
			t.Logf("  [%s] %s", it.Status, it.Content)
		}
		// Each item should have non-empty content (todo_write fault tolerance skips empty content, here we verify not all empty)
		anyNonEmpty := false
		for _, it := range items {
			if strings.TrimSpace(it.Content) != "" {
				anyNonEmpty = true
				break
			}
		}
		if !anyNonEmpty {
			t.Error("todo items should have non-empty content")
		}
	}
}
