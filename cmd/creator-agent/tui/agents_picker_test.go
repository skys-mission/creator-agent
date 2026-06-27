package tui

// agents_picker_test.go covers the /agents overlay: opening from injected specs, filtering, commit
// (switch via the injected factory), Esc close, and the empty/unavailable guards. A switch-call
// recorder stands in for the real rebuild factory so the tests are deterministic and need no
// subprocess.

import (
	"strings"
	"testing"
)

// newAppWithAgents builds a sim App wired with agent specs + a switch recorder.
func newAppWithAgents(t *testing.T, specs []AgentSpec) (*App, *[]string) {
	t.Helper()
	a, _ := newAppWithSim(t, 80, 24)
	a.rt.agents = specs
	a.rt.currentAgent = "build"
	calls := []string{}
	a.rt.agentSwitch = func(name string) error { calls = append(calls, name); return nil }
	return a, &calls
}

// TestAgentsPickerOpenFromSpecs verifies /agents lists every agent.
func TestAgentsPickerOpenFromSpecs(t *testing.T) {
	specs := []AgentSpec{
		{Name: "build", Description: "default"},
		{Name: "plan", Description: "read-only", ReadOnly: true},
	}
	a, _ := newAppWithAgents(t, specs)
	if !handleSlashCommand(a, "/agents") {
		t.Fatalf("/agents should be handled")
	}
	if !a.agentsPicker.open {
		t.Fatalf("/agents should open the picker")
	}
	if len(a.agentsPicker.entries) != 2 {
		t.Fatalf("entries = %d, want 2: %+v", len(a.agentsPicker.entries), a.agentsPicker.entries)
	}
}

// TestAgentsPickerNoSpecsReportsUnavailable verifies the picker reports when no specs are injected.
func TestAgentsPickerNoSpecsReportsUnavailable(t *testing.T) {
	a, _ := newAppWithSim(t, 80, 24)
	a.rt.agents = nil
	if !handleSlashCommand(a, "/agents") {
		t.Fatalf("/agents should be handled")
	}
	if a.agentsPicker.open {
		t.Errorf("picker should not open without specs")
	}
	last := a.messages[len(a.messages)-1].content.String()
	if !strings.Contains(strings.ToLower(last), "no agents") {
		t.Errorf("expected an unavailable guidance message; got %q", last)
	}
}

// TestAgentsPickerCommitSwitches verifies selecting a different agent switches via the factory and
// updates currentAgent.
func TestAgentsPickerCommitSwitches(t *testing.T) {
	specs := []AgentSpec{
		{Name: "build", Description: "default"},
		{Name: "plan", Description: "read-only", ReadOnly: true},
	}
	a, calls := newAppWithAgents(t, specs)
	openAgentsPicker(a)
	// build is first/pinned + preselected; move down to plan, then commit.
	injectKey(a, KeyDown)
	if a.agentsPicker.entries[a.agentsPicker.selIdx].name != "plan" {
		t.Fatalf("precondition: plan should be selected after Down; got %q", a.agentsPicker.entries[a.agentsPicker.selIdx].name)
	}
	injectKey(a, KeyEnter)
	if len(*calls) != 1 || (*calls)[0] != "plan" {
		t.Errorf("switch factory should be called with plan; got %+v", *calls)
	}
	if a.agentsPicker.open {
		t.Errorf("picker should close after commit")
	}
	if a.rt.currentAgent != "plan" {
		t.Errorf("currentAgent should be plan; got %q", a.rt.currentAgent)
	}
}

// TestAgentsPickerCommitCurrentIsNoop verifies committing the already-current agent does not call
// the switch factory.
func TestAgentsPickerCommitCurrentIsNoop(t *testing.T) {
	specs := []AgentSpec{
		{Name: "build", Description: "default"},
		{Name: "plan", Description: "read-only", ReadOnly: true},
	}
	a, calls := newAppWithAgents(t, specs)
	openAgentsPicker(a)
	// build is preselected; Enter on it should be a no-op.
	injectKey(a, KeyEnter)
	if len(*calls) != 0 {
		t.Errorf("switch factory should NOT be called when committing the current agent; got %+v", *calls)
	}
}

// TestAgentsPickerEscClose verifies Esc dismisses without switching.
func TestAgentsPickerEscClose(t *testing.T) {
	specs := []AgentSpec{{Name: "build", Description: "default"}, {Name: "plan", Description: "ro"}}
	a, calls := newAppWithAgents(t, specs)
	openAgentsPicker(a)
	injectKey(a, KeyEsc)
	if a.agentsPicker.open {
		t.Errorf("Esc should close the picker")
	}
	if len(*calls) != 0 {
		t.Errorf("Esc should not switch; got %+v", *calls)
	}
}

// TestAgentsPickerFilter verifies typing narrows the agent list.
func TestAgentsPickerFilter(t *testing.T) {
	specs := []AgentSpec{
		{Name: "build", Description: "default agent"},
		{Name: "plan", Description: "plan mode"},
	}
	a, _ := newAppWithAgents(t, specs)
	openAgentsPicker(a)
	for _, r := range "pla" {
		injectRune(a, r)
	}
	if len(a.agentsPicker.entries) != 1 || a.agentsPicker.entries[0].name != "plan" {
		t.Errorf("filter 'pla' should keep only plan; got %+v", a.agentsPicker.entries)
	}
}

// TestAgentsPickerInRegistry verifies /agents is registered.
func TestAgentsPickerInRegistry(t *testing.T) {
	if findCommand("/agents") == nil {
		t.Fatalf("/agents should be in the command registry")
	}
	if commandDesc("/agents") == "" {
		t.Fatalf("/agents should have a description")
	}
}
