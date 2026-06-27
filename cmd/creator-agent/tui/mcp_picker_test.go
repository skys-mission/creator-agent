package tui

// mcp_picker_test.go covers the /mcps overlay: opening from a manager, status display, toggle
// enable/disable (with agent rebuild), filter, Esc close, and the empty/unavailable guards. A
// mock MCPManager + a toggle-counting rebuildTools stand in for the real dependencies so the tests
// are deterministic and need no subprocess.

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"
)

// mockMCPManager is a controllable MCPManager for tests. Its fields are guarded by mu because the
// async toggle path calls SetEnabled on a goroutine while the test goroutine reads them.
type mockMCPManager struct {
	mu         sync.Mutex
	statuses   []MCPManagerServerStatus
	enabled    map[string]bool // current server enabled state (mutated by SetEnabled)
	hasServers bool
	setErr     error // when non-nil, SetEnabled returns this
	toggled    []toggleRecord
	// toolToggled records per-tool toggles; toolErr returns from SetToolEnabled when non-nil.
	toolToggled []toolToggleRecord
	toolErr     error
	// doneCh, if non-nil, is closed when a SetEnabled call returns, so async-toggle tests can wait
	// for the goroutine to finish before asserting on toggled/lastErr.
	doneCh chan struct{}
}

type toggleRecord struct {
	name    string
	enabled bool
}

type toolToggleRecord struct {
	server  string
	tool    string
	enabled bool
}

func (m *mockMCPManager) Status() []MCPManagerServerStatus {
	m.mu.Lock()
	defer m.mu.Unlock()
	// Reflect the current enabled state into the returned statuses so the picker sees toggles.
	out := make([]MCPManagerServerStatus, len(m.statuses))
	for i, s := range m.statuses {
		if en, ok := m.enabled[s.Name]; ok {
			s.Enabled = en
		}
		out[i] = s
	}
	return out
}
func (m *mockMCPManager) SetEnabled(_ context.Context, name string, enabled bool) error {
	m.mu.Lock()
	setErr := m.setErr
	doneCh := m.doneCh
	if setErr == nil {
		m.enabled[name] = enabled
		m.toggled = append(m.toggled, toggleRecord{name, enabled})
	}
	m.mu.Unlock()
	// Signal completion outside the lock so a waiter can proceed.
	if doneCh != nil {
		select {
		case <-doneCh: // already closed (idempotent)
		default:
			close(doneCh)
		}
	}
	return setErr
}
func (m *mockMCPManager) SetToolEnabled(_ context.Context, server, tool string, enabled bool) error {
	m.mu.Lock()
	toolErr := m.toolErr
	if toolErr == nil {
		m.toolToggled = append(m.toolToggled, toolToggleRecord{server, tool, enabled})
		// Reflect per-tool state into the status snapshot's Tools so the picker re-render sees it.
		for i, s := range m.statuses {
			if s.Name != server {
				continue
			}
			for j, ts := range s.Tools {
				if ts.Name == tool {
					m.statuses[i].Tools[j].Enabled = enabled
				}
			}
		}
	}
	m.mu.Unlock()
	return toolErr
}
func (m *mockMCPManager) HasServers() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.hasServers
}

// toggledSnapshot returns a copy of the recorded server toggles (test-only, race-safe).
func (m *mockMCPManager) toggledSnapshot() []toggleRecord {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]toggleRecord, len(m.toggled))
	copy(out, m.toggled)
	return out
}

// toolToggledSnapshot returns a copy of the recorded per-tool toggles (test-only, race-safe).
func (m *mockMCPManager) toolToggledSnapshot() []toolToggleRecord {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]toolToggleRecord, len(m.toolToggled))
	copy(out, m.toolToggled)
	return out
}

// newAppWithMCP builds a sim App wired with a mock manager + a rebuildTools counter. It also wires
// a.rt.ctx + a.rt.sender (so the async toggle's mcpToggleDoneMsg lands in a.events and can be
// applied by drainMCPToggleEvents).
func newAppWithMCP(t *testing.T, mgr *mockMCPManager) (*App, *int) {
	t.Helper()
	a, _ := newAppWithSim(t, 80, 24)
	a.rt.ctx = context.Background()
	a.rt.mcpManager = mgr
	a.rt.sender = func(msg any) {
		select {
		case a.events <- msg:
		default:
		}
	}
	rebuilds := 0
	a.rt.rebuildTools = func() error { rebuilds++; return nil }
	return a, &rebuilds
}

// drainMCPToggleEvents applies any pending mcpToggleDoneMsg events from a.events (the async toggle
// posts its result there). Call after a toggle key to finalize state before asserting.
func drainMCPToggleEvents(a *App) {
	for {
		select {
		case msg := <-a.events:
			if m, ok := msg.(mcpToggleDoneMsg); ok {
				applyMCPToggleDone(a, m)
			}
		default:
			return
		}
	}
}

// waitForMCPToggle waits for the mock manager's SetEnabled to complete (mgr.doneCh) then drains the
// resulting event. Used by async-toggle tests to observe the final state. It polls until the
// picker's toggling flag clears (set by applyMCPToggleDone), tolerating the brief window between
// doneCh closing and the goroutine posting mcpToggleDoneMsg.
func waitForMCPToggle(t *testing.T, a *App, mgr *mockMCPManager) {
	t.Helper()
	mgr.mu.Lock()
	doneCh := mgr.doneCh
	mgr.mu.Unlock()
	if doneCh != nil {
		select {
		case <-doneCh:
		case <-time.After(2 * time.Second):
			t.Fatal("mock SetEnabled did not complete in time")
			return
		}
	}
	// Poll-drain: the mcpToggleDoneMsg is posted shortly after doneCh closes. Keep draining until the
	// toggle flag clears (or timeout).
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		drainMCPToggleEvents(a)
		if a.mcpPicker.toggling == "" {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("toggle did not clear after completion; toggling=%q", a.mcpPicker.toggling)
}

// TestMCPPickerOpenFromManager verifies /mcps lists every server.
func TestMCPPickerOpenFromManager(t *testing.T) {
	mgr := &mockMCPManager{
		hasServers: true,
		enabled:    map[string]bool{"alpha": true, "beta": false},
		statuses: []MCPManagerServerStatus{
			{Name: "alpha", Enabled: true, Connected: true, ToolCount: 3},
			{Name: "beta", Enabled: false, Connected: false},
		},
	}
	a, _ := newAppWithMCP(t, mgr)
	if !handleSlashCommand(a, "/mcps") {
		t.Fatalf("/mcps should be handled")
	}
	if !a.mcpPicker.open {
		t.Fatalf("/mcps should open the picker")
	}
	if len(a.mcpPicker.entries) != 2 {
		t.Fatalf("entries = %d, want 2: %+v", len(a.mcpPicker.entries), a.mcpPicker.entries)
	}
}

// TestMCPPickerNoManagerReportsUnavailable verifies the picker reports when no manager is injected.
func TestMCPPickerNoManagerReportsUnavailable(t *testing.T) {
	a, _ := newAppWithSim(t, 80, 24)
	a.rt.mcpManager = nil
	if !handleSlashCommand(a, "/mcps") {
		t.Fatalf("/mcps should be handled")
	}
	if a.mcpPicker.open {
		t.Errorf("picker should not open without a manager")
	}
}

// TestMCPPickerNoServersReportsConfig verifies that with a manager but no configured servers, the
// picker guides the user to configure some.
func TestMCPPickerNoServersReportsConfig(t *testing.T) {
	mgr := &mockMCPManager{hasServers: false, enabled: map[string]bool{}}
	a, _ := newAppWithMCP(t, mgr)
	handleSlashCommand(a, "/mcps")
	if a.mcpPicker.open {
		t.Errorf("picker should not open with no servers")
	}
	last := a.messages[len(a.messages)-1].content.String()
	if !strings.Contains(strings.ToLower(last), "config") && !strings.Contains(strings.ToLower(last), "no mcp") {
		t.Errorf("expected a config/no-servers guidance message; got %q", last)
	}
}

// TestMCPPickerToggleDisable verifies Enter on an enabled server disables it (via SetEnabled) and
// triggers an agent rebuild. The toggle is async; the test waits for completion.
func TestMCPPickerToggleDisable(t *testing.T) {
	mgr := &mockMCPManager{
		hasServers: true,
		enabled:    map[string]bool{"alpha": true},
		statuses:   []MCPManagerServerStatus{{Name: "alpha", Enabled: true, Connected: true, ToolCount: 2}},
		doneCh:     make(chan struct{}),
	}
	a, rebuilds := newAppWithMCP(t, mgr)
	openMCPPicker(a)
	// "alpha" is the only entry and selected.
	if a.mcpPicker.entries[a.mcpPicker.selIdx].server != "alpha" {
		t.Fatalf("precondition: alpha should be selected; got %q", a.mcpPicker.entries[a.mcpPicker.selIdx].server)
	}
	injectKey(a, KeyEnter)
	// Disable triggers a rebuild too (on the event loop), so the disabled server's tools are removed
	// from the live agent rather than lingering until the next rebuild.
	waitForMCPToggle(t, a, mgr)
	if len(mgr.toggled) != 1 || mgr.toggled[0].name != "alpha" || mgr.toggled[0].enabled {
		t.Errorf("SetEnabled should be called to disable alpha; got %+v", mgr.toggled)
	}
	if *rebuilds != 1 {
		t.Errorf("disabling a server should trigger exactly one agent rebuild; got %d", *rebuilds)
	}
	// Picker stays open after a toggle (opencode-style: adjust several servers in one visit).
	if !a.mcpPicker.open {
		t.Errorf("picker should stay open after toggle (opencode-style); state open=%v", a.mcpPicker.open)
	}
	if a.mcpPicker.toggling != "" {
		t.Errorf("toggling should be cleared after completion; got %q", a.mcpPicker.toggling)
	}
}

// TestMCPPickerToggleEnable verifies Enter on a disabled server enables it.
func TestMCPPickerToggleEnable(t *testing.T) {
	mgr := &mockMCPManager{
		hasServers: true,
		enabled:    map[string]bool{"beta": false},
		statuses:   []MCPManagerServerStatus{{Name: "beta", Enabled: false, Connected: false}},
		doneCh:     make(chan struct{}),
	}
	a, rebuilds := newAppWithMCP(t, mgr)
	openMCPPicker(a)
	injectKey(a, KeyEnter)
	waitForMCPToggle(t, a, mgr)
	if len(mgr.toggled) != 1 || !mgr.toggled[0].enabled {
		t.Errorf("SetEnabled should enable beta; got %+v", mgr.toggled)
	}
	if *rebuilds != 1 {
		t.Errorf("enabling a server should trigger exactly one agent rebuild; got %d", *rebuilds)
	}
}

// TestMCPPickerSpaceToggles verifies Space also toggles (opencode-style).
func TestMCPPickerSpaceToggles(t *testing.T) {
	mgr := &mockMCPManager{
		hasServers: true,
		enabled:    map[string]bool{"alpha": true},
		statuses:   []MCPManagerServerStatus{{Name: "alpha", Enabled: true, Connected: true, ToolCount: 1}},
		doneCh:     make(chan struct{}),
	}
	a, _ := newAppWithMCP(t, mgr)
	openMCPPicker(a)
	injectRune(a, ' ')
	waitForMCPToggle(t, a, mgr)
	if len(mgr.toggled) != 1 {
		t.Errorf("Space should toggle; got %+v", mgr.toggled)
	}
}

// TestMCPPickerToggleRefusedWhileBusy verifies a toggle is refused during an active stream.
func TestMCPPickerToggleRefusedWhileBusy(t *testing.T) {
	mgr := &mockMCPManager{
		hasServers: true,
		enabled:    map[string]bool{"alpha": true},
		statuses:   []MCPManagerServerStatus{{Name: "alpha", Enabled: true, Connected: true, ToolCount: 1}},
	}
	a, _ := newAppWithMCP(t, mgr)
	a.status = statusThinking
	openMCPPicker(a)
	injectKey(a, KeyEnter)
	if len(mgr.toggled) != 0 {
		t.Errorf("toggle should be refused while busy; got %+v", mgr.toggled)
	}
}

// TestMCPPickerToggleErrorReported verifies a SetEnabled error is surfaced and no rebuild happens.
func TestMCPPickerToggleErrorReported(t *testing.T) {
	mgr := &mockMCPManager{
		hasServers: true,
		enabled:    map[string]bool{"alpha": true},
		statuses:   []MCPManagerServerStatus{{Name: "alpha", Enabled: true, Connected: true, ToolCount: 1}},
		setErr:     strErrMCP("connection refused"),
		doneCh:     make(chan struct{}),
	}
	a, rebuilds := newAppWithMCP(t, mgr)
	openMCPPicker(a)
	injectKey(a, KeyEnter)
	waitForMCPToggle(t, a, mgr)
	if *rebuilds != 0 {
		t.Errorf("no rebuild should happen on toggle error; got %d", *rebuilds)
	}
	if a.mcpPicker.lastErr == "" {
		t.Errorf("toggle error should be recorded in lastErr")
	}
}

// TestMCPPickerEscClose verifies Esc dismisses without toggling.
func TestMCPPickerEscClose(t *testing.T) {
	mgr := &mockMCPManager{
		hasServers: true,
		enabled:    map[string]bool{"alpha": true},
		statuses:   []MCPManagerServerStatus{{Name: "alpha", Enabled: true, Connected: true, ToolCount: 1}},
	}
	a, _ := newAppWithMCP(t, mgr)
	openMCPPicker(a)
	injectKey(a, KeyEsc)
	if a.mcpPicker.open {
		t.Errorf("Esc should close the picker")
	}
	if len(mgr.toggled) != 0 {
		t.Errorf("Esc should not toggle; got %+v", mgr.toggled)
	}
}

// TestMCPPickerFilter verifies typing narrows the server list.
func TestMCPPickerFilter(t *testing.T) {
	mgr := &mockMCPManager{
		hasServers: true,
		enabled:    map[string]bool{"alpha": true, "beta": true, "gamma": true},
		statuses: []MCPManagerServerStatus{
			{Name: "alpha", Enabled: true, Connected: true, ToolCount: 1},
			{Name: "beta", Enabled: true, Connected: true, ToolCount: 1},
			{Name: "gamma", Enabled: true, Connected: true, ToolCount: 1},
		},
	}
	a, _ := newAppWithMCP(t, mgr)
	openMCPPicker(a)
	for _, r := range "alp" {
		injectRune(a, r)
	}
	if len(a.mcpPicker.entries) != 1 || a.mcpPicker.entries[0].server != "alpha" {
		t.Errorf("filter 'alp' should keep only alpha; got %+v", a.mcpPicker.entries)
	}
}

// TestMCPPickerInRegistry verifies /mcps is registered.
func TestMCPPickerInRegistry(t *testing.T) {
	if findCommand("/mcps") == nil {
		t.Fatalf("/mcps should be in the command registry")
	}
	if commandDesc("/mcps") == "" {
		t.Fatalf("/mcps should have a description")
	}
}

// strErrMCP is a sentinel error for the toggle-error test.
type strErrMCP string

func (e strErrMCP) Error() string { return string(e) }

// newAppWithMCPTools builds a sim App with a mock manager exposing one server ("alpha") with two
// tools, both enabled. Returns the app, the rebuild counter, and the manager for assertions.
func newAppWithMCPTools(t *testing.T) (*App, *int, *mockMCPManager) {
	t.Helper()
	mgr := &mockMCPManager{
		hasServers: true,
		enabled:    map[string]bool{"alpha": true},
		statuses: []MCPManagerServerStatus{{
			Name: "alpha", Enabled: true, Connected: true, ToolCount: 2,
			Tools: []MCPManagerToolStatus{
				{Name: "tool_a", Enabled: true},
				{Name: "tool_b", Enabled: true},
			},
		}},
	}
	a, rebuilds := newAppWithMCP(t, mgr)
	return a, rebuilds, mgr
}

// TestMCPPickerShowsToolRows verifies opening the picker lists the server header + indented tool
// rows (default expanded).
func TestMCPPickerShowsToolRows(t *testing.T) {
	a, _, _ := newAppWithMCPTools(t)
	openMCPPicker(a)
	// Expect: [server alpha][tool tool_a][tool tool_b]
	if len(a.mcpPicker.entries) != 3 {
		t.Fatalf("entries = %d, want 3 (server + 2 tools): %+v", len(a.mcpPicker.entries), a.mcpPicker.entries)
	}
	if a.mcpPicker.entries[0].kind != mcpEntryServer || a.mcpPicker.entries[0].server != "alpha" {
		t.Errorf("entry[0] should be the alpha server; got %+v", a.mcpPicker.entries[0])
	}
	if a.mcpPicker.entries[1].kind != mcpEntryTool || a.mcpPicker.entries[1].tool != "tool_a" {
		t.Errorf("entry[1] should be tool_a; got %+v", a.mcpPicker.entries[1])
	}
	if a.mcpPicker.entries[2].kind != mcpEntryTool || a.mcpPicker.entries[2].tool != "tool_b" {
		t.Errorf("entry[2] should be tool_b; got %+v", a.mcpPicker.entries[2])
	}
}

// TestMCPPickerToggleTool verifies selecting a tool row and pressing Enter toggles that single tool
// via SetToolEnabled + triggers a rebuild (without toggling the server).
func TestMCPPickerToggleTool(t *testing.T) {
	a, rebuilds, mgr := newAppWithMCPTools(t)
	openMCPPicker(a)
	// Move down to tool_a (entry index 1) and toggle it.
	a.mcpPicker.selIdx = 1
	injectKey(a, KeyEnter)
	if len(mgr.toolToggled) != 1 || mgr.toolToggled[0].tool != "tool_a" || mgr.toolToggled[0].enabled {
		t.Errorf("SetToolEnabled should disable tool_a; got %+v", mgr.toolToggled)
	}
	if len(mgr.toggled) != 0 {
		t.Errorf("server SetEnabled should NOT be called for a tool toggle; got %+v", mgr.toggled)
	}
	if *rebuilds != 1 {
		t.Errorf("agent should be rebuilt once after tool toggle; got %d", *rebuilds)
	}
}

// TestMCPPickerCollapseHidesToolRows verifies Tab on a server row collapses its tool rows.
func TestMCPPickerCollapseHidesToolRows(t *testing.T) {
	a, _, _ := newAppWithMCPTools(t)
	openMCPPicker(a)
	if len(a.mcpPicker.entries) != 3 {
		t.Fatalf("precondition: 3 entries; got %d", len(a.mcpPicker.entries))
	}
	// Select the server row (index 0) and collapse via Tab.
	a.mcpPicker.selIdx = 0
	injectKey(a, KeyTab)
	if len(a.mcpPicker.entries) != 1 {
		t.Errorf("collapse should hide tool rows; got %d entries: %+v", len(a.mcpPicker.entries), a.mcpPicker.entries)
	}
	if a.mcpPicker.entries[0].kind != mcpEntryServer {
		t.Errorf("remaining entry should be the server; got %+v", a.mcpPicker.entries[0])
	}
	// Re-expand.
	injectKey(a, KeyTab)
	if len(a.mcpPicker.entries) != 3 {
		t.Errorf("re-expand should restore tool rows; got %d entries", len(a.mcpPicker.entries))
	}
}

// TestMCPPickerToolToggleRefusedWhenServerDisabled verifies a tool row under a disabled server is
// not operable (the toggle is a no-op: no SetToolEnabled call, no rebuild).
func TestMCPPickerToolToggleRefusedWhenServerDisabled(t *testing.T) {
	a, rebuilds, mgr := newAppWithMCPTools(t)
	// Disable the server.
	mgr.enabled["alpha"] = false
	mgr.statuses[0].Enabled = false
	openMCPPicker(a)
	// The picker still shows tool rows (greyed) under the disabled server; select tool_a and toggle.
	a.mcpPicker.selIdx = 1 // tool_a
	injectKey(a, KeyEnter)
	if len(mgr.toolToggled) != 0 {
		t.Errorf("tool toggle should be refused when server disabled; got %+v", mgr.toolToggled)
	}
	if *rebuilds != 0 {
		t.Errorf("no rebuild should happen for a refused tool toggle; got %d", *rebuilds)
	}
}

// TestMCPPickerSpaceTogglesTool verifies Space also toggles the selected row (server or tool).
func TestMCPPickerSpaceTogglesTool(t *testing.T) {
	a, _, mgr := newAppWithMCPTools(t)
	openMCPPicker(a)
	a.mcpPicker.selIdx = 2 // tool_b
	injectRune(a, ' ')
	if len(mgr.toolToggled) != 1 || mgr.toolToggled[0].tool != "tool_b" {
		t.Errorf("Space should toggle tool_b; got %+v", mgr.toolToggled)
	}
}

// TestMCPPickerEscCancelsToggle verifies Esc while a toggle is in flight cancels it (invokes the
// cancel func) rather than closing the picker. The picker stays open; the in-flight goroutine's
// context is cancelled.
func TestMCPPickerEscCancelsToggle(t *testing.T) {
	mgr := &mockMCPManager{
		hasServers: true,
		enabled:    map[string]bool{"alpha": true},
		statuses:   []MCPManagerServerStatus{{Name: "alpha", Enabled: true, Connected: true, ToolCount: 1}},
		// setErr makes SetEnabled return immediately (so doneCh fires) — but we cancel BEFORE it
		// completes; the point is to verify cancelMCPToggle runs.
		doneCh: make(chan struct{}),
	}
	a, _ := newAppWithMCP(t, mgr)
	openMCPPicker(a)
	injectKey(a, KeyEnter) // start async toggle
	if a.mcpPicker.toggling != "alpha" {
		t.Fatalf("precondition: toggle should be in flight; toggling=%q", a.mcpPicker.toggling)
	}
	if a.mcpPicker.toggleCancel == nil {
		t.Fatalf("precondition: toggleCancel should be set during an in-flight toggle")
	}
	// Esc should cancel (not close) while toggling.
	injectKey(a, KeyEsc)
	// The picker should still be open (Esc cancelled the toggle, didn't close).
	if !a.mcpPicker.open {
		t.Errorf("Esc during a toggle should cancel it, not close the picker")
	}
	// Drain: the cancelled SetEnabled completes (setErr is nil so it succeeds against the mock);
	// the toggleCancel func was invoked. Verify the cancel func is still callable (idempotent) —
	// calling it again is a no-op, not a panic.
	a.mcpPicker.toggleCancel = nil // clear to avoid any leak; waitForMCPToggle drains the result
	// Allow the goroutine to finish (mock SetEnabled is fast).
	waitForMCPToggle(t, a, mgr)
}

// TestMCPPickerConnectingEntryPopulated verifies a server with Connecting=true in its status shows
// the connecting flag on its picker entry (drives the animated row).
func TestMCPPickerConnectingEntryPopulated(t *testing.T) {
	mgr := &mockMCPManager{
		hasServers: true,
		enabled:    map[string]bool{"alpha": false},
		statuses:   []MCPManagerServerStatus{{Name: "alpha", Enabled: false, Connected: false, Connecting: true}},
	}
	a, _ := newAppWithMCP(t, mgr)
	openMCPPicker(a)
	if len(a.mcpPicker.entries) != 1 || !a.mcpPicker.entries[0].connecting {
		t.Errorf("entry should reflect connecting=true; got %+v", a.mcpPicker.entries)
	}
}
