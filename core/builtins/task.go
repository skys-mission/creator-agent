package builtins

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/skys-mission/creator-agent/core"
)

// TaskTool is the subagent tool ("task"): spawns a read-only child session to execute an independent exploration task,
// returning a concise conclusion summary to the parent session.
//
// Purpose (subagent = context compression mechanism):
// The main agent delegates large exploration work (reading multiple files, grepping code) to a child agent.
// The child agent runs in an isolated context and returns only the conclusion — the parent context is not polluted by intermediate steps.
//
// Safety guardrails (defense in depth):
//  1. Recursive task is disabled by default (child session toolset does not contain task);
//  2. Derived permissions (child session defaults to read-only — only read/grep/glob are given);
//  3. Reuses the main loop (RunForked) + systemPrompt (cache prefix shared);
//  4. Child session step limit is tightened (prevents runaway);
//  5. Recursion depth counter: each TaskTool instance carries its session depth; forking into a child
//     that itself exposes task rebuilds the child's TaskTool at depth+1 (see Exec). The counter caps
//     nesting even if guardrail 1 is bypassed, with an absolute hard cap (hardMaxTaskDepth) that no
//     configuration can raise.
type TaskTool struct {
	parentCfg core.AgentConfig // base config for deriving child sessions (depth set via TaskWithDepth)
	tools     []core.Tool      // tools allowed in the child session (task itself is already excluded)
	maxSteps  int              // child session step limit

	// depth is this tool's own session depth (top-level = 0). Forked child TaskTools
	// (when task is allowed in the child) are rebuilt at parent depth + 1 so the
	// counter actually propagates instead of staying pinned at the construction value.
	depth int
	// maxTaskDepth is the configurable recursion limit (child forks at depth+1).
	// Capped by hardMaxTaskDepth in effectiveMaxDepth().
	maxTaskDepth int
}

// TaskOption configures a TaskTool.
type TaskOption func(*TaskTool)

// TaskWithDepth sets the session depth of the constructed tool (used when
// rebuilding a TaskTool for a child session so the depth counter propagates).
func TaskWithDepth(d int) TaskOption {
	return func(t *TaskTool) { t.depth = d }
}

// TaskWithMaxDepth overrides the recursion limit (default defaultMaxTaskDepth=2).
// Values above hardMaxTaskDepth are silently clamped, so a misconfigured high
// value cannot enable runaway nesting.
func TaskWithMaxDepth(n int) TaskOption {
	return func(t *TaskTool) { t.maxTaskDepth = n }
}

// defaultMaxTaskDepth is the default sub-agent recursion limit.
// Top-level depth=0; nesting beyond this is rejected unless the caller raises it.
const defaultMaxTaskDepth = 2

// effectiveMaxDepth returns the active recursion limit: the configured value
// clamped by the absolute hard cap so no configuration can disable the ceiling.
func (t *TaskTool) effectiveMaxDepth() int {
	limit := t.maxTaskDepth
	if limit <= 0 {
		limit = defaultMaxTaskDepth
	}
	return clampInt(limit, hardMaxTaskDepth)
}

// NewTaskTool creates a TaskTool. parentCfg is used for derivation (reuses Model + SystemPrompt).
// allowedTools are the tools available to the child session (the caller should exclude task itself and only provide read-only tools).
func NewTaskTool(parentCfg core.AgentConfig, allowedTools []core.Tool, maxSteps int, opts ...TaskOption) *TaskTool {
	if maxSteps <= 0 {
		maxSteps = 10
	}
	// Defensive: exclude task from allowedTools (recursion guard, first line of defense)
	filtered := make([]core.Tool, 0, len(allowedTools))
	for _, tl := range allowedTools {
		if tl.Info().Name != "task" {
			filtered = append(filtered, tl)
		}
	}
	t := &TaskTool{parentCfg: parentCfg, tools: filtered, maxSteps: maxSteps}
	for _, o := range opts {
		o(t)
	}
	return t
}

// Info declares tool metadata.
func (t *TaskTool) Info() core.ToolInfo {
	return core.ToolInfo{
		Name: "task",
		Description: `Launch a sub-agent to handle a sub-task in an isolated context. The sub-agent explores (read-only tools) and returns a concise conclusion. Use this to delegate large investigations so the main context stays clean.

Input:
- description: short label of the sub-task (one line)
- prompt: the full instructions for the sub-agent

The sub-agent runs with a restricted, read-only toolset (no writes, no nested task). It returns a summary you can act on.`,
		InputSchema: json.RawMessage(`{
  "type": "object",
  "properties": {
    "description": {"type": "string", "description": "short label of the sub-task"},
    "prompt": {"type": "string", "description": "full instructions for the sub-agent"}
  },
  "required": ["description", "prompt"]
}`),
		// task has side effects (spawns a child session): not read-only, not parallel, not interruptible
		ReadOnly:          false,
		ConcurrencySafe:   false,
		InterruptBehavior: core.InterruptBlock,
		MaxResultChars:    10000,
	}
}

// Exec runs the child session.
func (t *TaskTool) Exec(ctx context.Context, input json.RawMessage) (core.ToolResult, error) {
	var args struct {
		Description string `json:"description"`
		Prompt      string `json:"prompt"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return invalidInputResult(err), nil
	}
	if strings.TrimSpace(args.Prompt) == "" {
		return core.ToolResult{Content: "Error: prompt is required", IsError: true}, nil
	}
	// Recursion depth guard (second line of defense; the first is excluding task from
	// the child toolset). The child forks at depth+1, so reject when this tool's depth
	// already reaches the limit. effectiveMaxDepth() enforces the absolute hard cap.
	limit := t.effectiveMaxDepth()
	if t.depth >= limit {
		return core.ToolResult{Content: fmt.Sprintf("Error: sub-agent recursion limit reached (depth %d >= %d)", t.depth, limit), IsError: true}, nil
	}

	// Derive child session config: reuse Model + SystemPrompt (cache prefix shared), restricted toolset, tighter step limit.
	// ForkWithDepth propagates the incremented depth so that, if the child toolset were to
	// include a rebuilt TaskTool, it would carry the correct depth (defense in depth).
	childCfg := core.NewForkedConfig(t.parentCfg,
		core.ForkWithTools(t.tools...),
		core.ForkWithMaxSteps(t.maxSteps),
		core.ForkWithDepth(t.depth+1),
	)

	// Build child session initial state: reuse parent systemPrompt (key for cache prefix sharing)
	state := &core.RunState{
		Messages: []core.Message{
			core.UserMessage(args.Prompt),
		},
	}

	// Collect child session text output (the child agent's "conclusion")
	ch := make(chan core.Event, 64)
	go func() {
		// close runs after the panic recover so a panic-recovered ErrorEvent still reaches the
		// consumer before the channel closes.
		defer close(ch)
		// Recover panics from RunForked's middleware hooks / stream consumption / tool chain in this
		// goroutine and forward them as ErrorEvent to the parent agent (execOne's recover cannot catch
		// panics in this child goroutine's stack). Use a blocking send (no default) so the panic error is
		// never dropped: the consumer ranges ch until close, and close is deferred to run after this.
		defer func() {
			if r := recover(); r != nil {
				ch <- core.ErrorEvent{Err: fmt.Errorf("sub-agent panicked: %v", r)}
			}
		}()
		core.RunForked(ctx, childCfg, state, ch)
	}()

	var sb strings.Builder
	var toolCalls int
	var lastErr string
	var finishReason core.FinishReason
	for ev := range ch {
		switch e := ev.(type) {
		case core.TextEvent:
			sb.WriteString(e.Delta)
		case core.ToolUseStartEvent:
			toolCalls++
		case core.ErrorEvent:
			lastErr = e.Err.Error()
		case core.FinishEvent:
			// Child session ended: record the reason so the parent agent gets a more useful hint when output is empty
			finishReason = e.Reason
		}
	}

	// Assemble the result returned to the parent agent
	result := sb.String()
	if result == "" {
		switch {
		case lastErr != "":
			result = "(sub-agent error: " + lastErr + ")"
		case finishReason == core.FinishStepLimit:
			// Step limit exhausted with no conclusion: the parent agent should know the child session did not finish
			result = fmt.Sprintf("(sub-agent hit step limit (%d steps) without a conclusion; try a smaller/simpler sub-task)", t.maxSteps)
		case finishReason == core.FinishCanceled:
			result = "(sub-agent was canceled)"
		default:
			result = "(sub-agent produced no output)"
		}
	}
	// Attach metadata to help the parent agent understand
	header := fmt.Sprintf("[sub-agent task: %s | %d tool calls]\n", args.Description, toolCalls)
	return core.ToolResult{Content: header + result}, nil
}

// Ensure core.Tool implementation.
var _ core.Tool = (*TaskTool)(nil)
