package middlewares

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/skys-mission/creator-agent/core"
)

// newTestHooks is a constructor that injects a runner for testing.
func newTestHooks(cfg HooksConfig, runner commandRunner) *UserHooksMiddleware {
	return &UserHooksMiddleware{cfg: cfg, runner: runner}
}

// echoTool records invocation details.
type echoTool struct {
	name     string
	calls    int
	lastIn   json.RawMessage
	execFunc func(input json.RawMessage) (core.ToolResult, error)
}

func (e *echoTool) Info() core.ToolInfo {
	return core.ToolInfo{Name: e.name, InputSchema: json.RawMessage(`{"type":"object"}`)}
}
func (e *echoTool) Exec(ctx context.Context, input json.RawMessage) (core.ToolResult, error) {
	e.calls++
	e.lastIn = input
	if e.execFunc != nil {
		return e.execFunc(input)
	}
	return core.ToolResult{Content: "ran"}, nil
}

// ===== matchToolName =====

func TestMatchToolName(t *testing.T) {
	cases := []struct {
		matcher, name string
		want          bool
	}{
		{"", "anything", true},
		{"*", "anything", true},
		{"bash", "bash", true},
		{"bash", "read", false},
		{"read", "read", true},
	}
	for _, c := range cases {
		if got := matchToolName(c.matcher, c.name); got != c.want {
			t.Errorf("match(%q,%q) = %v, want %v", c.matcher, c.name, got, c.want)
		}
	}
}

// ===== PreToolUse decision branches =====

// TestPreToolUseApprove verifies that approve allows the tool to run with unchanged input.
func TestPreToolUseApprove(t *testing.T) {
	runner := func(ctx context.Context, cmd string, stdin []byte) ([]byte, error) {
		return []byte(`{"decision":"approve"}`), nil
	}
	tool := &echoTool{name: "bash"}
	mw := newTestHooks(HooksConfig{PreToolUse: []HookEntry{{Matcher: "bash", Command: "x"}}}, runner)
	wrapped := mw.WrapTool("bash", func(ctx context.Context, in json.RawMessage) (core.ToolResult, error) {
		return tool.Exec(ctx, in)
	})

	res, err := wrapped(context.Background(), json.RawMessage(`{"command":"ls"}`))
	if err != nil || res.IsError {
		t.Errorf("approve should pass: %+v %v", res, err)
	}
	if tool.calls != 1 {
		t.Errorf("tool not called: %d", tool.calls)
	}
}

// TestPreToolUseDeny verifies that deny short-circuits and the tool is not called.
func TestPreToolUseDeny(t *testing.T) {
	runner := func(ctx context.Context, cmd string, stdin []byte) ([]byte, error) {
		return []byte(`{"decision":"deny","reason":"forbidden"}`), nil
	}
	tool := &echoTool{name: "bash"}
	mw := newTestHooks(HooksConfig{PreToolUse: []HookEntry{{Matcher: "bash", Command: "x"}}}, runner)
	wrapped := mw.WrapTool("bash", func(ctx context.Context, in json.RawMessage) (core.ToolResult, error) {
		return tool.Exec(ctx, in)
	})

	res, err := wrapped(context.Background(), json.RawMessage(`{"command":"rm"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Error("deny should return IsError")
	}
	if tool.calls != 0 {
		t.Errorf("tool called despite deny: %d", tool.calls)
	}
	if res.Content != "blocked by hook: forbidden" {
		t.Errorf("deny reason wrong: %q", res.Content)
	}
}

// TestPreToolUseModify verifies that modify rewrites input before calling the tool.
func TestPreToolUseModify(t *testing.T) {
	runner := func(ctx context.Context, cmd string, stdin []byte) ([]byte, error) {
		return []byte(`{"decision":"modify","modified_input":{"command":"ls -la"}}`), nil
	}
	tool := &echoTool{name: "bash"}
	mw := newTestHooks(HooksConfig{PreToolUse: []HookEntry{{Matcher: "bash", Command: "x"}}}, runner)
	wrapped := mw.WrapTool("bash", func(ctx context.Context, in json.RawMessage) (core.ToolResult, error) {
		return tool.Exec(ctx, in)
	})

	_, err := wrapped(context.Background(), json.RawMessage(`{"command":"rm"}`))
	if err != nil {
		t.Fatal(err)
	}
	if tool.calls != 1 {
		t.Fatalf("tool not called: %d", tool.calls)
	}
	// Input should have been rewritten
	var got struct {
		Cmd string `json:"command"`
	}
	_ = json.Unmarshal(tool.lastIn, &got)
	if got.Cmd != "ls -la" {
		t.Errorf("input not modified: %s", tool.lastIn)
	}
}

// TestPreToolUseFailClosed verifies that a hook runner error is treated as deny (fail-closed).
func TestPreToolUseFailClosed(t *testing.T) {
	runner := func(ctx context.Context, cmd string, stdin []byte) ([]byte, error) {
		return nil, errors.New("hook crashed")
	}
	tool := &echoTool{name: "bash"}
	mw := newTestHooks(HooksConfig{PreToolUse: []HookEntry{{Matcher: "bash", Command: "x"}}}, runner)
	wrapped := mw.WrapTool("bash", func(ctx context.Context, in json.RawMessage) (core.ToolResult, error) {
		return tool.Exec(ctx, in)
	})

	res, _ := wrapped(context.Background(), json.RawMessage(`{}`))
	if !res.IsError {
		t.Error("hook failure should fail-closed (deny)")
	}
	if tool.calls != 0 {
		t.Errorf("tool called despite hook failure: %d", tool.calls)
	}
}

// TestPreToolUseNonJSON verifies that non-JSON hook output is treated as deny (fail-closed).
func TestPreToolUseNonJSON(t *testing.T) {
	runner := func(ctx context.Context, cmd string, stdin []byte) ([]byte, error) {
		return []byte(`not json at all`), nil
	}
	tool := &echoTool{name: "bash"}
	mw := newTestHooks(HooksConfig{PreToolUse: []HookEntry{{Matcher: "bash", Command: "x"}}}, runner)
	wrapped := mw.WrapTool("bash", func(ctx context.Context, in json.RawMessage) (core.ToolResult, error) {
		return tool.Exec(ctx, in)
	})

	res, _ := wrapped(context.Background(), json.RawMessage(`{}`))
	if !res.IsError {
		t.Error("non-JSON output should fail-closed")
	}
}

// TestPreToolUseNoMatch verifies that a non-matching hook does not run and the tool is called directly.
func TestPreToolUseNoMatch(t *testing.T) {
	hookRan := false
	runner := func(ctx context.Context, cmd string, stdin []byte) ([]byte, error) {
		hookRan = true
		return []byte(`{"decision":"deny"}`), nil
	}
	tool := &echoTool{name: "read"}
	mw := newTestHooks(HooksConfig{PreToolUse: []HookEntry{{Matcher: "bash", Command: "x"}}}, runner)
	wrapped := mw.WrapTool("read", func(ctx context.Context, in json.RawMessage) (core.ToolResult, error) {
		return tool.Exec(ctx, in)
	})

	res, _ := wrapped(context.Background(), json.RawMessage(`{}`))
	if hookRan {
		t.Error("bash-only hook should not run for read tool")
	}
	if res.IsError {
		t.Error("non-matching hook should not block")
	}
}

// ===== PostToolUse =====

// TestPostToolUseObserves verifies that PostToolUse hooks run but do not affect the result.
func TestPostToolUseObserves(t *testing.T) {
	postRan := false
	runner := func(ctx context.Context, cmd string, stdin []byte) ([]byte, error) {
		var p map[string]any
		_ = json.Unmarshal(stdin, &p)
		if p["event"] == "post_tool_use" {
			postRan = true
			if p["tool"] != "bash" {
				t.Errorf("post tool name wrong: %v", p["tool"])
			}
		}
		return []byte(`{}`), nil
	}
	tool := &echoTool{name: "bash"}
	mw := newTestHooks(HooksConfig{PostToolUse: []HookEntry{{Matcher: "*", Command: "x"}}}, runner)
	wrapped := mw.WrapTool("bash", func(ctx context.Context, in json.RawMessage) (core.ToolResult, error) {
		return tool.Exec(ctx, in)
	})

	res, err := wrapped(context.Background(), json.RawMessage(`{}`))
	if err != nil || res.IsError {
		t.Errorf("post hook should not affect result: %+v", res)
	}
	if !postRan {
		t.Error("post hook did not run")
	}
}

// ===== Stop =====

// TestStopHook verifies that Stop hooks run in AfterAgent.
func TestStopHook(t *testing.T) {
	stopRan := false
	runner := func(ctx context.Context, cmd string, stdin []byte) ([]byte, error) {
		var p map[string]any
		_ = json.Unmarshal(stdin, &p)
		if p["event"] == "stop" {
			stopRan = true
		}
		return []byte(`{}`), nil
	}
	mw := newTestHooks(HooksConfig{Stop: []HookEntry{{Command: "x"}}}, runner)
	_ = mw.AfterAgent(context.Background(), &core.RunState{})
	if !stopRan {
		t.Error("stop hook did not run")
	}
}

// ===== Payload format =====

// TestPreToolUsePayload verifies that the PreToolUse payload contains tool name and input.
func TestPreToolUsePayload(t *testing.T) {
	var got map[string]any
	runner := func(ctx context.Context, cmd string, stdin []byte) ([]byte, error) {
		_ = json.Unmarshal(stdin, &got)
		return []byte(`{"decision":"approve"}`), nil
	}
	tool := &echoTool{name: "bash"}
	mw := newTestHooks(HooksConfig{PreToolUse: []HookEntry{{Matcher: "*", Command: "x"}}}, runner)
	wrapped := mw.WrapTool("bash", func(ctx context.Context, in json.RawMessage) (core.ToolResult, error) {
		return tool.Exec(ctx, in)
	})
	_, _ = wrapped(context.Background(), json.RawMessage(`{"command":"ls"}`))
	if got["tool"] != "bash" {
		t.Errorf("payload tool = %v, want bash", got["tool"])
	}
	if got["event"] != "pre_tool_use" {
		t.Errorf("payload event = %v", got["event"])
	}
}

// TestPreToolUseUnknownDecision verifies that an unknown decision value is treated as deny (fail-closed).
func TestPreToolUseUnknownDecision(t *testing.T) {
	runner := func(ctx context.Context, cmd string, stdin []byte) ([]byte, error) {
		return []byte(`{"decision":"maybe"}`), nil
	}
	tool := &echoTool{name: "bash"}
	mw := newTestHooks(HooksConfig{PreToolUse: []HookEntry{{Matcher: "*", Command: "x"}}}, runner)
	wrapped := mw.WrapTool("bash", func(ctx context.Context, in json.RawMessage) (core.ToolResult, error) {
		return tool.Exec(ctx, in)
	})
	res, _ := wrapped(context.Background(), json.RawMessage(`{}`))
	if !res.IsError {
		t.Error("unknown decision should fail-closed")
	}
}
