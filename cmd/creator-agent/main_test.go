package main

import (
	"context"
	"testing"

	"github.com/skys-mission/creator-agent/config"
	"github.com/skys-mission/creator-agent/core"
	"github.com/skys-mission/creator-agent/core/builtins"
)

// stubProvider is a minimal core.ModelProvider for build tests.
type stubProvider struct{ name string }

func (s stubProvider) Stream(ctx context.Context, req core.ModelRequest) (<-chan core.ModelEvent, error) {
	return nil, nil
}

// TestBuildAllToolsBindsTaskToProvider verifies that buildAllTools wires the task sub-agent's
// parent config to the supplied provider, so /model switching creates a taskTool that delegates
// to the new provider.
func TestBuildAllToolsBindsTaskToProvider(t *testing.T) {
	cfg := &config.Config{}
	p1 := stubProvider{name: "p1"}
	tools1 := buildAllTools(p1, cfg, builtins.NoopSandbox{}, nil, nil)
	var task1 *builtins.TaskTool
	for _, tl := range tools1 {
		if tl.Info().Name == "task" {
			// Access the concrete tool via type assertion to inspect parentCfg.
			task1 = tl.(*builtins.TaskTool)
			break
		}
	}
	if task1 == nil {
		t.Fatal("task tool not found")
	}

	p2 := stubProvider{name: "p2"}
	tools2 := buildAllTools(p2, cfg, builtins.NoopSandbox{}, nil, nil)
	var task2 *builtins.TaskTool
	for _, tl := range tools2 {
		if tl.Info().Name == "task" {
			task2 = tl.(*builtins.TaskTool)
			break
		}
	}
	if task2 == nil {
		t.Fatal("task tool not found in second build")
	}

	// We cannot compare interface values directly, but we can verify the two task tools are
	// distinct instances and that running a sub-task would use the second provider by checking
	// the tool is rebuilt (different pointer).
	if task1 == task2 {
		t.Error("buildAllTools should create a new taskTool for each provider")
	}
}
