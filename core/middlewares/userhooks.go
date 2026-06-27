package middlewares

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/skys-mission/creator-agent/core"
)

// HookEntry is a single user hook configuration. It is a plain value type with no serialization
// tags: the config package owns parsing and converts into this at the boundary.
type HookEntry struct {
	Matcher string // tool name glob ("bash" / "read" / "*"); empty = match all
	Command string // shell command; stdin receives JSON payload, stdout returns JSON decision
}

// HooksConfig groups user hook configurations by event.
type HooksConfig struct {
	PreToolUse  []HookEntry // before tool execution (can deny/modify)
	PostToolUse []HookEntry // after tool execution (observation only, does not modify result)
	Stop        []HookEntry // when the agent finishes
}

// hookDecision is the return protocol for PreToolUse hooks.
type hookDecision struct {
	Decision      string          `json:"decision"` // "approve" / "deny" / "modify" / "" (empty = allow)
	Reason        string          `json:"reason,omitempty"`
	ModifiedInput json.RawMessage `json:"modified_input,omitempty"` // new input when decision=modify
}

// commandRunner executes a shell command (injected for testing; production uses exec).
type commandRunner func(ctx context.Context, command string, stdin []byte) ([]byte, error)

// realRunner runs commands via os/exec.
func realRunner(ctx context.Context, command string, stdin []byte) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "sh", "-c", command)
	cmd.Stdin = strings.NewReader(string(stdin))
	return cmd.Output() // stderr is included in *ExitError when the exit code is non-zero
}

// UserHooksMiddleware wires user-configured shell hooks into the agent loop.
//
// Behavior:
//   - PreToolUse: inside WrapTool, runs matching hooks before next; returns deny → short-circuits with IsError;
//     returns modify → replaces input before calling next; returns approve/empty → allows.
//   - PostToolUse: after next returns, runs matching hooks (observation only, hook stdout does not affect result).
//   - Stop: runs matching hooks in AfterAgent.
//
// Hook failures (non-zero exit / JSON parse failure): fail-closed for PreToolUse (treated as deny)
// to avoid silently allowing when hooks are broken. PostToolUse/Stop failures are logged to stderr only
// and do not affect the main flow.
type UserHooksMiddleware struct {
	core.BaseMiddleware

	cfg    HooksConfig
	runner commandRunner
}

var _ core.Middleware = (*UserHooksMiddleware)(nil)

// NewUserHooks creates a user hooks middleware (production uses realRunner).
func NewUserHooks(cfg HooksConfig) *UserHooksMiddleware {
	return &UserHooksMiddleware{cfg: cfg, runner: realRunner}
}

// WrapTool implements Pre/PostToolUse.
func (h *UserHooksMiddleware) WrapTool(name string, next core.ToolFunc) core.ToolFunc {
	return func(ctx context.Context, input json.RawMessage) (core.ToolResult, error) {
		// PreToolUse: run matching hooks, may short-circuit or rewrite input
		input, err := h.runPreToolUse(ctx, name, input)
		if err != nil {
			// deny or hook failure → short-circuit with an error result
			return core.ToolResult{Content: "blocked by hook: " + err.Error(), IsError: true}, nil
		}

		start := time.Now()
		res, execErr := next(ctx, input)
		duration := time.Since(start)

		// PostToolUse: observation only (does not change res/execErr)
		h.runPostToolUse(ctx, name, input, res, execErr, duration)
		return res, execErr
	}
}

// AfterAgent implements Stop hooks.
func (h *UserHooksMiddleware) AfterAgent(ctx context.Context, s *core.RunState) error {
	for _, hook := range h.cfg.Stop {
		if !matchToolName(hook.Matcher, "") {
			continue // Stop hook matcher is usually empty (match all); non-empty matches by tool name (no per-tool context here, so only empty runs)
		}
		payload := map[string]any{"event": "stop", "messages": len(s.Messages)}
		_, _ = h.runHook(ctx, hook.Command, payload) // Stop failure does not affect the main flow
	}
	return nil
}

// runPreToolUse runs all matching PreToolUse hooks; returns (final input, denyError).
func (h *UserHooksMiddleware) runPreToolUse(ctx context.Context, name string, input json.RawMessage) (json.RawMessage, error) {
	cur := input
	for _, hook := range h.cfg.PreToolUse {
		if !matchToolName(hook.Matcher, name) {
			continue
		}
		payload := map[string]any{"event": "pre_tool_use", "tool": name, "input": json.RawMessage(cur)}
		out, err := h.runHook(ctx, hook.Command, payload)
		if err != nil {
			// Hook execution failure → fail-closed as deny
			return nil, fmt.Errorf("pre_tool_use hook %q failed: %w", hook.Command, err)
		}
		var dec hookDecision
		if jerr := json.Unmarshal(out, &dec); jerr != nil {
			// Non-JSON output → fail-closed
			return nil, fmt.Errorf("pre_tool_use hook %q returned non-JSON output", hook.Command)
		}
		switch strings.ToLower(dec.Decision) {
		case "deny", "block":
			reason := dec.Reason
			if reason == "" {
				reason = "denied by hook"
			}
			return nil, fmt.Errorf("%s", reason)
		case "modify":
			if len(dec.ModifiedInput) > 0 {
				cur = dec.ModifiedInput
			}
		case "approve", "allow", "":
			// allow
		default:
			// Unknown decision → fail-closed
			return nil, fmt.Errorf("pre_tool_use hook returned unknown decision %q", dec.Decision)
		}
	}
	return cur, nil
}

// runPostToolUse runs matching PostToolUse hooks (observation only, failures are ignored).
func (h *UserHooksMiddleware) runPostToolUse(ctx context.Context, name string, input json.RawMessage, res core.ToolResult, err error, d time.Duration) {
	for _, hook := range h.cfg.PostToolUse {
		if !matchToolName(hook.Matcher, name) {
			continue
		}
		payload := map[string]any{
			"event":    "post_tool_use",
			"tool":     name,
			"input":    json.RawMessage(input),
			"duration": d.String(),
			"error":    "",
		}
		if err != nil {
			payload["error"] = err.Error()
		}
		payload["is_error"] = res.IsError
		payload["content_len"] = len(res.Content)
		_, _ = h.runHook(ctx, hook.Command, payload)
	}
}

// runHook executes a single hook command with stdin=JSON(payload), returning stdout.
func (h *UserHooksMiddleware) runHook(ctx context.Context, command string, payload any) ([]byte, error) {
	stdin, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal payload: %w", err)
	}
	return h.runner(ctx, command, stdin)
}

// matchToolName checks whether a matcher glob matches a tool name; empty matcher = match all.
func matchToolName(matcher, name string) bool {
	if matcher == "" || matcher == "*" {
		return true
	}
	return globMatch(matcher, name)
}
