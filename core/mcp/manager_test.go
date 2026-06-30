package mcp

// manager_test.go covers the MCP Manager: initial connect of all servers, enable/disable
// filtering, reconnect-on-enable, per-server status, and failure isolation. Tests inject an
// in-process client factory (real stdio/http transport is exercised by adapter_test's NewClient
// path; here we want fast, deterministic per-server connect).

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/skys-mission/creator-agent/core"
)

// registerOneTool returns an in-process server hook that registers a single tool (named name)
// returning the fixed text "ok".
func registerOneTool(name string) func(*mcp.Server) {
	return func(srv *mcp.Server) {
		mcp.AddTool(srv, &mcp.Tool{Name: name, Description: "test tool"},
			func(ctx context.Context, req *mcp.CallToolRequest, in struct{}) (*mcp.CallToolResult, any, error) {
				return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "ok"}}}, nil, nil
			})
	}
}

// inProcessFactory returns a clientFactory that builds a fresh in-process server + client per
// call, each registering one tool named toolName. connectFailures, if non-nil, is incremented on
// each call (so a test can assert reconnect happened).
func inProcessFactory(toolName string, connectFailures *int32) clientFactory {
	return func(ctx context.Context, cfg ServerConfig) (*Client, error) {
		if connectFailures != nil {
			atomic.AddInt32(connectFailures, 1)
		}
		return newInProcessClient(cfg.Name, nil, registerOneTool(toolName))
	}
}

// failingFactory returns a clientFactory that always errors, so a server starts (and stays)
// disconnected with lastErr set.
func failingFactory(err error) clientFactory {
	return func(ctx context.Context, cfg ServerConfig) (*Client, error) { return nil, err }
}

// TestManagerEnabledTools verifies the manager flattens tools from all enabled servers.
func TestManagerEnabledTools(t *testing.T) {
	configs := []ServerConfig{
		{Name: "alpha", Type: "stdio"},
		{Name: "beta", Type: "stdio"},
	}
	m := newManagerWithFactory(context.Background(), configs, inProcessFactory("tool_a", nil))
	defer m.Close()
	tools := m.EnabledTools()
	// Each in-process server registers exactly one tool.
	if len(tools) != 2 {
		t.Fatalf("EnabledTools = %d tools, want 2", len(tools))
	}
	names := map[string]bool{}
	for _, tl := range tools {
		names[tl.Info().Name] = true
	}
	if !names["tool_a"] {
		t.Errorf("expected tool_a from alpha; got %v", names)
	}
}

// TestManagerDisableFiltersTools verifies disabling a server removes its tools from EnabledTools.
func TestManagerDisableFiltersTools(t *testing.T) {
	configs := []ServerConfig{{Name: "alpha", Type: "stdio"}, {Name: "beta", Type: "stdio"}}
	m := newManagerWithFactory(context.Background(), configs, inProcessFactory("t", nil))
	defer m.Close()
	if err := m.SetEnabled(context.Background(), "alpha", false); err != nil {
		t.Fatalf("disable alpha: %v", err)
	}
	tools := m.EnabledTools()
	if len(tools) != 1 {
		t.Fatalf("after disabling alpha, EnabledTools = %d, want 1", len(tools))
	}
	// alpha should be marked disabled in Status.
	for _, st := range m.Status() {
		if st.Name == "alpha" && st.Enabled {
			t.Errorf("alpha should be disabled in status")
		}
		if st.Name == "beta" && !st.Enabled {
			t.Errorf("beta should still be enabled")
		}
	}
}

// TestManagerReEnableKeepsClient verifies re-enabling an already-connected server flips the flag
// back without reconnecting (the client is retained during disable).
func TestManagerReEnableKeepsClient(t *testing.T) {
	configs := []ServerConfig{{Name: "alpha", Type: "stdio"}}
	var connects int32
	m := newManagerWithFactory(context.Background(), configs, inProcessFactory("t", &connects))
	defer m.Close()
	firstConnects := atomic.LoadInt32(&connects)
	// Disable then re-enable: should not reconnect (client retained).
	if err := m.SetEnabled(context.Background(), "alpha", false); err != nil {
		t.Fatal(err)
	}
	if err := m.SetEnabled(context.Background(), "alpha", true); err != nil {
		t.Fatal(err)
	}
	if got := atomic.LoadInt32(&connects); got != firstConnects {
		t.Errorf("re-enable of connected server should not reconnect; connects %d -> %d", firstConnects, got)
	}
	if len(m.EnabledTools()) != 1 {
		t.Errorf("after re-enable, tools = %d, want 1", len(m.EnabledTools()))
	}
}

// TestManagerEnableReconnectsFailed verifies enabling a never-connected (failed) server triggers a
// real reconnect.
func TestManagerEnableReconnectsFailed(t *testing.T) {
	configs := []ServerConfig{{Name: "alpha", Type: "stdio"}}
	// Start with a failing factory so alpha is disconnected at init.
	m := newManagerWithFactory(context.Background(), configs, failingFactory(errors.New("boom")))
	defer m.Close()
	st := m.Status()
	if len(st) != 1 || st[0].Connected {
		t.Fatalf("alpha should start disconnected; got %+v", st)
	}
	// Swap the factory to a working one and enable -> reconnect.
	m.newClient = inProcessFactory("t", nil)
	if err := m.SetEnabled(context.Background(), "alpha", true); err != nil {
		t.Fatalf("enable failed server: %v", err)
	}
	st = m.Status()
	if !st[0].Connected || !st[0].Enabled || st[0].ToolCount != 1 {
		t.Errorf("after enable, alpha status = %+v, want connected+enabled+1tool", st[0])
	}
	if st[0].LastErr != "" {
		t.Errorf("lastErr should clear on successful connect; got %q", st[0].LastErr)
	}
}

// TestManagerFailureIsolation verifies one failing server does not block others.
func TestManagerFailureIsolation(t *testing.T) {
	configs := []ServerConfig{
		{Name: "good", Type: "stdio"},
		{Name: "bad", Type: "stdio"},
	}
	// Factory fails for "bad", succeeds for "good".
	factory := func(ctx context.Context, cfg ServerConfig) (*Client, error) {
		if cfg.Name == "bad" {
			return nil, errors.New("nope")
		}
		return inProcessFactory("g", nil)(ctx, cfg)
	}
	m := newManagerWithFactory(context.Background(), configs, factory)
	defer m.Close()
	tools := m.EnabledTools()
	if len(tools) != 1 {
		t.Errorf("good server should still contribute; got %d tools", len(tools))
	}
	for _, st := range m.Status() {
		if st.Name == "bad" {
			if st.Connected || st.Enabled {
				t.Errorf("bad should be disconnected+disabled; got %+v", st)
			}
			if !strings.Contains(st.LastErr, "nope") {
				t.Errorf("bad lastErr should mention cause; got %q", st.LastErr)
			}
		}
		if st.Name == "good" && !st.Enabled {
			t.Errorf("good should be enabled")
		}
	}
}

// TestManagerSetEnabledUnknownServer verifies toggling an unknown name errors.
func TestManagerSetEnabledUnknownServer(t *testing.T) {
	m := newManagerWithFactory(context.Background(), nil, inProcessFactory("t", nil))
	defer m.Close()
	if err := m.SetEnabled(context.Background(), "ghost", true); err == nil {
		t.Errorf("SetEnabled on unknown server should error")
	}
}

// TestManagerOrderStable verifies Status preserves config order (and skips duplicate names).
func TestManagerOrderStable(t *testing.T) {
	configs := []ServerConfig{
		{Name: "zeta", Type: "stdio"},
		{Name: "alpha", Type: "stdio"},
		{Name: "zeta", Type: "stdio"}, // duplicate, ignored
	}
	m := newManagerWithFactory(context.Background(), configs, inProcessFactory("t", nil))
	defer m.Close()
	st := m.Status()
	if len(st) != 2 {
		t.Fatalf("expected 2 unique servers (dup ignored); got %d", len(st))
	}
	if st[0].Name != "zeta" || st[1].Name != "alpha" {
		t.Errorf("order = [%s, %s], want [zeta, alpha]", st[0].Name, st[1].Name)
	}
}

// TestManagerHasServers covers the empty/non-empty check.
func TestManagerHasServers(t *testing.T) {
	empty := newManagerWithFactory(context.Background(), nil, inProcessFactory("t", nil))
	defer empty.Close()
	if empty.HasServers() {
		t.Errorf("empty manager should report no servers")
	}
	m := newManagerWithFactory(context.Background(), []ServerConfig{{Name: "x"}}, inProcessFactory("t", nil))
	defer m.Close()
	if !m.HasServers() {
		t.Errorf("manager with a server should HasServers")
	}
}

// inProcessMultiToolFactory returns a clientFactory that builds an in-process server registering
// every name in toolNames. Used by per-tool tests that need several tools on one server.
func inProcessMultiToolFactory(toolNames []string) clientFactory {
	return func(ctx context.Context, cfg ServerConfig) (*Client, error) {
		return newInProcessClient(cfg.Name, nil, func(srv *mcp.Server) {
			for _, name := range toolNames {
				n := name
				mcp.AddTool(srv, &mcp.Tool{Name: n, Description: "test tool"},
					func(ctx context.Context, req *mcp.CallToolRequest, in struct{}) (*mcp.CallToolResult, any, error) {
						return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "ok"}}}, nil, nil
					})
			}
		})
	}
}

// TestManagerSetToolEnabled verifies SetToolEnabled hides/shows a single tool in EnabledTools, that
// Status reports per-tool state, and that an unknown server/tool is an error.
func TestManagerSetToolEnabled(t *testing.T) {
	m := newManagerWithFactory(context.Background(),
		[]ServerConfig{{Name: "alpha", Type: "stdio"}},
		inProcessMultiToolFactory([]string{"tool_a", "tool_b"}),
	)
	defer m.Close()
	// Both tools present initially.
	if got := names(m.EnabledTools()); !sameSet(got, []string{"tool_a", "tool_b"}) {
		t.Fatalf("initial EnabledTools = %v, want [tool_a tool_b]", got)
	}
	// Disable tool_a individually.
	if err := m.SetToolEnabled(context.Background(), "alpha", "tool_a", false); err != nil {
		t.Fatalf("SetToolEnabled tool_a=false: %v", err)
	}
	if got := names(m.EnabledTools()); !sameSet(got, []string{"tool_b"}) {
		t.Fatalf("after disabling tool_a, EnabledTools = %v, want [tool_b]", got)
	}
	// Status reports tool_a disabled, tool_b enabled.
	st := m.Status()
	var alphaStatus *ServerStatus
	for i := range st {
		if st[i].Name == "alpha" {
			alphaStatus = &st[i]
		}
	}
	if alphaStatus == nil || len(alphaStatus.Tools) != 2 {
		t.Fatalf("alpha status tools = %+v", alphaStatus)
	}
	toolAEnabled, toolBEnabled := true, true
	for _, ts := range alphaStatus.Tools {
		if ts.Name == "tool_a" {
			toolAEnabled = ts.Enabled
		}
		if ts.Name == "tool_b" {
			toolBEnabled = ts.Enabled
		}
	}
	if toolAEnabled {
		t.Errorf("Status should report tool_a disabled")
	}
	if !toolBEnabled {
		t.Errorf("Status should report tool_b enabled")
	}
	// Re-enable tool_a -> back to both.
	if err := m.SetToolEnabled(context.Background(), "alpha", "tool_a", true); err != nil {
		t.Fatalf("SetToolEnabled tool_a=true: %v", err)
	}
	if got := names(m.EnabledTools()); !sameSet(got, []string{"tool_a", "tool_b"}) {
		t.Fatalf("after re-enabling tool_a, EnabledTools = %v, want both", got)
	}
	// Unknown server/tool is an error.
	if err := m.SetToolEnabled(context.Background(), "ghost", "tool_a", false); err == nil {
		t.Errorf("SetToolEnabled on unknown server should error")
	}
	if err := m.SetToolEnabled(context.Background(), "alpha", "ghost", false); err == nil {
		t.Errorf("SetToolEnabled on unknown tool should error")
	}
}

// TestManagerServerDisableHidesAllTools verifies that disabling a server hides all its tools even
// if some are individually enabled (server-level takes precedence), and re-enabling restores them
// with per-tool flags intact.
func TestManagerServerDisableHidesAllTools(t *testing.T) {
	m := newManagerWithFactory(context.Background(),
		[]ServerConfig{{Name: "alpha", Type: "stdio"}},
		inProcessMultiToolFactory([]string{"tool_a", "tool_b"}),
	)
	defer m.Close()
	// Disable tool_a per-tool, then disable the whole server.
	if err := m.SetToolEnabled(context.Background(), "alpha", "tool_a", false); err != nil {
		t.Fatal(err)
	}
	if err := m.SetEnabled(context.Background(), "alpha", false); err != nil {
		t.Fatal(err)
	}
	if got := m.EnabledTools(); len(got) != 0 {
		t.Errorf("disabling server should hide all tools; got %v", got)
	}
	// Re-enable the server: tool_b returns, tool_a stays individually disabled (flag preserved).
	if err := m.SetEnabled(context.Background(), "alpha", true); err != nil {
		t.Fatal(err)
	}
	if got := names(m.EnabledTools()); !sameSet(got, []string{"tool_b"}) {
		t.Errorf("after re-enabling server, EnabledTools = %v, want [tool_b] (tool_a still per-tool-disabled)", got)
	}
}

// names extracts tool names from a []core.Tool for assertion.
func names(tools []core.Tool) []string {
	out := make([]string, 0, len(tools))
	for _, t := range tools {
		out = append(out, t.Info().Name)
	}
	return out
}

// sameSet reports whether two string slices hold the same elements regardless of order.
func sameSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	set := make(map[string]bool, len(a))
	for _, s := range a {
		set[s] = true
	}
	for _, s := range b {
		if !set[s] {
			return false
		}
	}
	return true
}

// blockingFactory returns a clientFactory whose connect blocks until either release is closed or
// ctx is cancelled. Used to observe the "connecting" state mid-handshake and to test cancellation.
func blockingFactory(release <-chan struct{}) clientFactory {
	return func(ctx context.Context, cfg ServerConfig) (*Client, error) {
		select {
		case <-release:
			// Build a real in-process client once released (one tool so the server is "connected").
			return inProcessFactory(cfg.Name+"_t", nil)(ctx, cfg)
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

// connecting returns the Connecting flag for a server in the manager's Status snapshot.
func connecting(t *testing.T, m *Manager, name string) bool {
	t.Helper()
	for _, s := range m.Status() {
		if s.Name == name {
			return s.Connecting
		}
	}
	return false
}

// TestManagerConnectingStateObserved verifies Status() reports Connecting=true while a connect
// handshake is in flight (the lock is released during I/O so Status can read concurrently).
func TestManagerConnectingStateObserved(t *testing.T) {
	// Build with a FAILING factory so alpha starts disabled with no client (the connect path will be
	// exercised on enable, not the fast already-connected path).
	m := newManagerWithFactory(context.Background(),
		[]ServerConfig{{Name: "alpha", Type: "stdio"}},
		failingFactory(errors.New("initial fail")),
	)
	defer m.Close()
	if st := m.Status(); len(st) != 1 || st[0].Connected {
		t.Fatalf("precondition: alpha should start disconnected; got %+v", st)
	}
	// Enable with a BLOCKING factory so the handshake stalls and Connecting is observable.
	release := make(chan struct{})
	m.newClient = blockingFactory(release)
	go func() {
		_ = m.SetEnabled(context.Background(), "alpha", true)
	}()
	// While the reconnect is blocked, Status should report Connecting=true.
	observed := false
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if connecting(t, m, "alpha") {
			observed = true
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if !observed {
		t.Errorf("Status should report Connecting=true while a handshake is in flight")
	}
	// Release the connect; it should complete and clear Connecting.
	close(release)
	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if st := m.Status(); len(st) == 1 && st[0].Connected && !st[0].Connecting {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Errorf("connect did not complete cleanly; status: %+v", m.Status())
}

// TestManagerSetEnabledCancellable verifies cancelling the ctx aborts an in-flight connect.
func TestManagerSetEnabledCancellable(t *testing.T) {
	// Build with a FAILING factory so alpha starts with no client (enable exercises the connect path).
	m := newManagerWithFactory(context.Background(),
		[]ServerConfig{{Name: "alpha", Type: "stdio"}},
		failingFactory(errors.New("initial fail")),
	)
	defer m.Close()
	// Enable with a never-released blocking factory; cancel mid-handshake.
	m.newClient = blockingFactory(make(chan struct{}))
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- m.SetEnabled(ctx, "alpha", true) }()
	// Give the connect time to start (block), then cancel.
	time.Sleep(50 * time.Millisecond)
	cancel()
	select {
	case err := <-errCh:
		if err == nil {
			t.Errorf("SetEnabled should return an error after cancellation")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("SetEnabled did not return after cancellation")
	}
	// Connecting should be cleared, alpha disabled.
	if connecting(t, m, "alpha") {
		t.Errorf("Connecting should be cleared after a cancelled connect")
	}
}
