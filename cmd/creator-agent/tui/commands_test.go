package tui

// commands_test.go covers the minimal command surface: the registry (only /model-new during the
// rebuild) and submitInput's dispatch rules.

import (
	"strings"
	"testing"

	"github.com/skys-mission/creator-agent/cmd/creator-agent/tui/i18n"
)

// TestCommandRegistryOnlyModel is the drift guard for the rebuilt command set: exactly /model is
// registered (the second-level menu owns create/manage), and lookup semantics stay intact.
func TestCommandRegistryOnlyModel(t *testing.T) {
	if len(commandRegistry) != 1 || commandRegistry[0].name != "/model" {
		t.Fatalf("commandRegistry = %v, want exactly [/model]", commandRegistry)
	}
	if findCommand("/model") == nil {
		t.Fatal("findCommand(/model) = nil")
	}
	if findCommand("/model-new") != nil || findCommand("/help") != nil || findCommand("/nope") != nil {
		t.Fatal("cut commands must not resolve")
	}
}

func TestSubmitUnknownSlashShowsHint(t *testing.T) {
	a, _ := newAppWithSim(t, 80, 24)
	a.input.SetValue("/nope")
	submitInput(a)
	if !strings.Contains(a.notice, "/nope") {
		t.Fatalf("notice %q should name the unknown command", a.notice)
	}
	if a.input.Value() != "" {
		t.Fatalf("typo command should clear the input; got %q", a.input.Value())
	}
}

func TestSubmitTextReportsRuntimeNotReady(t *testing.T) {
	a, _ := newAppWithSim(t, 80, 24)
	a.input.SetValue("hello")
	submitInput(a)
	if a.notice != i18n.T("msg.runtime_not_ready") {
		t.Fatalf("notice = %q, want runtime_not_ready", a.notice)
	}
	// The draft is kept: the runtime lands with the P2 rebuild and must not eat what was typed.
	if a.input.Value() != "hello" {
		t.Fatalf("draft was consumed: %q", a.input.Value())
	}
}

func TestSubmitPathLikeSlashIsText(t *testing.T) {
	a, _ := newAppWithSim(t, 80, 24)
	a.input.SetValue("/etc/hosts")
	submitInput(a)
	if a.notice != i18n.T("msg.runtime_not_ready") {
		t.Fatalf("path-like text should hit the runtime notice; got %q", a.notice)
	}
}

func TestSubmitModelCommandOpensMenu(t *testing.T) {
	a, _ := newAppWithSim(t, 80, 24)
	a.input.SetValue("/model")
	submitInput(a)
	if !a.modelMenu.open {
		t.Fatal("/model did not open the second-level menu")
	}
	if a.input.Value() != "" {
		t.Fatalf("handled command should clear the input; got %q", a.input.Value())
	}
}
