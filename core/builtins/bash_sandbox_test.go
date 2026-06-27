package builtins

import (
	"context"
	"encoding/json"
	"os/exec"
	"testing"

	"github.com/skys-mission/creator-agent/core"
)

// recordingSandbox records the arguments passed to Wrap and returns an executable noop cmd
// (does not actually run a sandbox).
type recordingSandbox struct {
	gotCommand string
	gotCwd     string
	called     bool
}

func (r *recordingSandbox) Wrap(ctx context.Context, command, cwd string) *exec.Cmd {
	r.called = true
	r.gotCommand = command
	r.gotCwd = cwd
	// Return a real bash -c so Exec can finish, verifying the integration still works.
	return exec.CommandContext(ctx, "bash", "-c", command)
}

// Verify: default (no Option) uses NoopSandbox and the command executes normally.
func TestBashDefaultNoopExecutes(t *testing.T) {
	b := NewBashTool()
	res, err := b.Exec(context.Background(), json.RawMessage(`{"command":"echo hello"}`))
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Errorf("noop should succeed: %+v", res)
	}
	if res.Content != "hello\n" && res.Content != "hello" {
		t.Errorf("output = %q", res.Content)
	}
}

// Verify: after injecting a sandbox, Wrap is called with command + cwd.
func TestBashWithSandboxCallsWrap(t *testing.T) {
	sb := &recordingSandbox{}
	b := NewBashTool(WithSandbox(sb))
	_, err := b.Exec(context.Background(), json.RawMessage(`{"command":"echo test"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !sb.called {
		t.Error("sandbox.Wrap not called")
	}
	if sb.gotCommand != "echo test" {
		t.Errorf("command = %q, want 'echo test'", sb.gotCommand)
	}
	if sb.gotCwd == "" {
		t.Error("cwd not passed to sandbox")
	}
}

// Verify: WithSandbox(nil) does not panic and does not override the default NoopSandbox.
func TestBashWithNilSandbox(t *testing.T) {
	b := NewBashTool(WithSandbox(nil))
	// Should still execute (NoopSandbox)
	res, err := b.Exec(context.Background(), json.RawMessage(`{"command":"echo ok"}`))
	if err != nil || res.IsError {
		t.Errorf("nil sandbox should fall back to noop: %+v %v", res, err)
	}
}

// Verify: after sandbox integration, command failures still map to IsError correctly.
func TestBashSandboxFailureMapped(t *testing.T) {
	b := NewBashTool() // noop
	res, _ := b.Exec(context.Background(), json.RawMessage(`{"command":"exit 1"}`))
	if !res.IsError {
		t.Error("non-zero exit should be IsError")
	}
}

// Ensure BashTool implements core.Tool and recordingSandbox implements Sandbox.
var _ core.Tool = (*BashTool)(nil)
var _ Sandbox = (*recordingSandbox)(nil)
