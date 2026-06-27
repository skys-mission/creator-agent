package builtins

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/skys-mission/creator-agent/core"
)

// BashTool executes shell commands.
//
// Capability: write + not concurrency-safe (commands may have side effects or dependencies).
// Security: can be injected with a Sandbox (macOS sandbox-exec / Linux bwrap) for OS-level isolation
// (opt-in; defaults to NoopSandbox).
type BashTool struct {
	Timeout time.Duration // default 60s
	sandbox Sandbox       // default NoopSandbox (plain bash -c); override with WithSandbox
}

// BashOption is a configuration option for BashTool.
type BashOption func(*BashTool)

// WithSandbox sets the sandbox (OS-level isolation).
func WithSandbox(s Sandbox) BashOption {
	return func(b *BashTool) {
		if s != nil {
			b.sandbox = s
		}
	}
}

// WithBashTimeout overrides the default 60s command timeout. A non-positive
// value is ignored so callers can forward a config-derived value without
// checking for "unset".
func WithBashTimeout(d time.Duration) BashOption {
	return func(b *BashTool) {
		if d > 0 {
			b.Timeout = d
		}
	}
}

// NewBashTool creates a BashTool with a 60s timeout and NoopSandbox by default.
func NewBashTool(opts ...BashOption) *BashTool {
	b := &BashTool{Timeout: 60 * time.Second, sandbox: NoopSandbox{}}
	for _, o := range opts {
		o(b)
	}
	return b
}

func (b *BashTool) Info() core.ToolInfo {
	return core.ToolInfo{
		Name:        "bash",
		Description: "Run a shell command via `bash -c`. Input: {\"command\": \"...\"}. Returns combined stdout+stderr.",
		InputSchema: json.RawMessage(`{
  "type": "object",
  "properties": {
    "command": {"type": "string", "description": "the shell command to run"}
  },
  "required": ["command"]
}`),
		ReadOnly:        false,
		ConcurrencySafe: false,
		MaxResultChars:  20000,
	}
}

func (b *BashTool) Exec(ctx context.Context, input json.RawMessage) (core.ToolResult, error) {
	var args struct {
		Command string `json:"command"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return invalidInputResult(err), nil
	}
	if args.Command == "" {
		return core.ToolResult{Content: "error: command is required", IsError: true}, nil
	}

	parentCtx := ctx
	if b.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, b.Timeout)
		defer cancel()
	}

	// cwd is the writable working directory for the sandbox (sandbox allows writes to cwd, blocks others).
	cwd, err := os.Getwd()
	if err != nil {
		cwd = ""
		core.Warnf("bash: getwd failed: %v", err)
	}
	cmd := b.sandbox.Wrap(ctx, args.Command, cwd)
	// The command and its children form a new process group. When the context is canceled or times out,
	// the entire tree is killed to prevent background child processes from escaping as orphans.
	setSysProcAttr(cmd)
	cmd.Cancel = func() error {
		// On context cancellation (timeout or Ctrl+C), kill the whole process group, not just bash.
		return killProcessGroup(cmd)
	}
	cmd.WaitDelay = 2 * time.Second // grace period after kill to wait for child reaping; forced cleanup on timeout to prevent zombies
	var out limitedWriter
	out.max = 1 << 20 // 1 MiB buffer cap (large outputs are spilled to disk by the loop layer; this buffer prevents OOM from commands like yes)
	cmd.Stdout = &out
	cmd.Stderr = &out

	runErr := cmd.Run()
	output := out.String()

	// Note: large outputs are spilled to disk by the loop layer using MaxResultChars; the tool does not truncate on its own.

	if runErr != nil {
		// Command failure (non-zero exit) -> IsError so the model sees stderr and can correct.
		// Distinguish timeout (ctx deadline) from ordinary non-zero exit: timeouts must explicitly
		// tell the model "command timed out and was killed", otherwise the model sees only
		// "signal: killed" and may retry the same long command thinking it was a transient failure.
		msg := output
		if isTimeout(parentCtx, ctx, runErr) {
			hint := fmt.Sprintf("(command timed out after %s and was killed)", b.Timeout)
			if msg == "" {
				msg = hint
			} else {
				msg = msg + "\n" + hint
			}
		} else if msg == "" {
			msg = runErr.Error()
		} else {
			msg = msg + "\n(exit error: " + runErr.Error() + ")"
		}
		return core.ToolResult{Content: msg, IsError: true}, nil
	}
	if output == "" {
		output = "(no output)"
	}
	return core.ToolResult{Content: output}, nil
}

// isTimeout reports whether the command failed because of this tool's own timeout.
// exec.CommandContext usually returns "signal: killed" on ctx timeout,
// so the error alone cannot distinguish timeout from user cancellation.
// We check DeadlineExceeded on the timeout ctx, but only when the parent ctx
// was not already DeadlineExceeded, so we do not mislabel a pre-canceled parent.
func isTimeout(parentCtx, ctx context.Context, runErr error) bool {
	_ = runErr // runErr is typically "signal: killed"; the ctx state is the reliable signal.
	// Only DeadlineExceeded counts as timeout; Canceled is user interrupt and should not say "timed out".
	// If the parent context was already DeadlineExceeded, the timeout did not come from this tool.
	return errors.Is(ctx.Err(), context.DeadlineExceeded) && !errors.Is(parentCtx.Err(), context.DeadlineExceeded)
}
