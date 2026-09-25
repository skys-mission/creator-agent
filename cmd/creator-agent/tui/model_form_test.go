package tui

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/skys-mission/creator-agent/cmd/creator-agent/tui/i18n"
	"github.com/skys-mission/creator-agent/contract"
)

// newModelFormApp builds a sim App whose model store points at a temp file, so tests never touch
// the real ~/.creator/models.json.
func newModelFormApp(t *testing.T) (*App, *Screen) {
	t.Helper()
	a, sim := newAppWithSim(t, 100, 40)
	a.models.path = filepath.Join(t.TempDir(), "models.json")
	return a, sim
}

// openForm opens the create form directly and asserts the dialog is open. (The form is reached
// from the /model menu's "New model…" item; the menu flow is covered in model_menu_test.go.)
func openForm(t *testing.T, a *App) {
	t.Helper()
	openModelForm(a)
	if !a.modelForm.open {
		t.Fatal("form not open after openModelForm")
	}
}

// typeText injects a string of runes through the normal key dispatch.
func typeText(a *App, s string) {
	for _, r := range s {
		injectRune(a, r)
	}
}

// fillModel walks the form top-to-bottom filling every field with the values of the walkthrough
// test model (name, base URL, model ID, API key, thinking echo off, reasoning key).
func fillModel(a *App) {
	typeText(a, "my-gpt")
	injectKey(a, KeyTab) // -> protocol (left at the only choice)
	injectKey(a, KeyTab) // -> base URL
	typeText(a, "https://example.com/v1")
	injectKey(a, KeyTab) // -> model ID
	typeText(a, "m1")
	injectKey(a, KeyTab) // -> API key
	typeText(a, "sk-test-abcd1234")
	injectKey(a, KeyTab) // -> thinking echo
	injectKey(a, KeyRight)
	injectKey(a, KeyTab) // -> reasoning key
	typeText(a, "reasoning")
}

func TestModelFormRendersFields(t *testing.T) {
	a, sim := newModelFormApp(t)
	openForm(t, a)
	render(a)
	if dump := screenDump(sim); !strings.Contains(dump, i18n.T("model_form.title")) {
		t.Fatalf("rendered form missing title %q:\n%s", i18n.T("model_form.title"), dump)
	}
	// The focus marker sits on the first field.
	if dump := screenDump(sim); !strings.Contains(dump, "> "+i18n.T("model_form.field.name")) {
		t.Fatalf("rendered form missing focus marker on first field:\n%s", dump)
	}
}

func TestModelFormWalkthroughCreatesModel(t *testing.T) {
	a, _ := newModelFormApp(t)
	openForm(t, a)
	fillModel(a)

	injectKey(a, KeyEnter) // -> confirm page
	if !a.modelForm.confirm {
		t.Fatalf("Enter on a valid form did not open the confirm page (err=%q)", a.modelForm.err)
	}
	injectKey(a, KeyEnter) // -> create
	if a.modelForm.open {
		t.Fatalf("form still open after create (err=%q)", a.modelForm.err)
	}

	want := contract.Model{
		Name:     "my-gpt",
		Protocol: contract.ProtocolOpenAIChat,
		BaseURL:  "https://example.com/v1",
		ModelID:  "m1",
		APIKey:   "sk-test-abcd1234",
		Params:   contract.Params{ThinkingEcho: contract.ThinkingEchoOff, ReasoningKey: "reasoning"},
	}
	got := a.models.load()
	if len(got) != 1 || !reflect.DeepEqual(got[0], want) {
		t.Fatalf("saved models = %+v, want [%+v]", got, want)
	}

	if !strings.Contains(a.notice, "my-gpt") || !strings.Contains(a.notice, a.models.path) {
		t.Fatalf("created notice %q missing name or store path", a.notice)
	}
}

func TestModelFormValidationBlocksConfirm(t *testing.T) {
	a, _ := newModelFormApp(t)
	openForm(t, a)
	injectKey(a, KeyEnter) // model ID is empty -> must not advance
	if a.modelForm.confirm {
		t.Fatal("Enter with empty model ID advanced to the confirm page")
	}
	if !a.modelForm.open {
		t.Fatal("form closed on validation failure")
	}
	if a.modelForm.err == "" {
		t.Fatal("validation failure left no error message")
	}
	if got := a.models.load(); got != nil {
		t.Fatalf("store not empty after failed validation: %+v", got)
	}
}

func TestModelFormEscCancels(t *testing.T) {
	a, _ := newModelFormApp(t)
	openForm(t, a)
	typeText(a, "scratch")
	injectKey(a, KeyEsc)
	if a.modelForm.open {
		t.Fatal("Esc did not close the form")
	}
	if got := a.models.load(); got != nil {
		t.Fatalf("store not empty after cancel: %+v", got)
	}
}

func TestModelFormCtrlCOnConfirmCancels(t *testing.T) {
	a, _ := newModelFormApp(t)
	openForm(t, a)
	injectKey(a, KeyTab) // -> protocol
	injectKey(a, KeyTab) // -> base URL
	injectKey(a, KeyTab) // -> model ID
	typeText(a, "m1")
	injectKey(a, KeyEnter) // confirm page
	if !a.modelForm.confirm {
		t.Fatalf("did not reach confirm page (err=%q)", a.modelForm.err)
	}
	injectKey(a, KeyCtrlC)
	if a.modelForm.open {
		t.Fatal("Ctrl+C did not cancel the form")
	}
	if got := a.models.load(); got != nil {
		t.Fatalf("store not empty after cancel: %+v", got)
	}
}

func TestModelFormAPIKeyMaskedOnScreen(t *testing.T) {
	a, sim := newModelFormApp(t)
	openForm(t, a)
	for i := 0; i < 4; i++ { // name -> protocol -> base URL -> model ID -> API key
		injectKey(a, KeyTab)
	}
	typeText(a, "sk-super-secret")
	render(a)
	dump := screenDump(sim)
	if strings.Contains(dump, "sk-super-secret") {
		t.Fatalf("API key rendered in clear:\n%s", dump)
	}
	if !strings.Contains(dump, strings.Repeat("•", len("sk-super-secret"))) {
		t.Fatalf("API key not rendered as bullets:\n%s", dump)
	}
}

func TestModelFormConfirmMasksKey(t *testing.T) {
	a, sim := newModelFormApp(t)
	openForm(t, a)
	for i := 0; i < 3; i++ { // name -> protocol -> base URL -> model ID
		injectKey(a, KeyTab)
	}
	typeText(a, "m1")
	injectKey(a, KeyTab) // -> API key
	typeText(a, "sk-super-secret")
	injectKey(a, KeyEnter) // confirm page
	render(a)
	dump := screenDump(sim)
	if strings.Contains(dump, "sk-super-secret") {
		t.Fatalf("confirm page leaked the API key:\n%s", dump)
	}
	if !strings.Contains(dump, maskKey("sk-super-secret")) {
		t.Fatalf("confirm page missing masked key %q:\n%s", maskKey("sk-super-secret"), dump)
	}
}

func TestModelFormSaveFailureKeepsFormOpen(t *testing.T) {
	a, _ := newModelFormApp(t)
	blocker := filepath.Join(t.TempDir(), "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	a.models.path = filepath.Join(blocker, "models.json")

	openForm(t, a)
	injectKey(a, KeyTab) // -> protocol
	injectKey(a, KeyTab) // -> base URL
	injectKey(a, KeyTab) // -> model ID
	typeText(a, "m1")
	injectKey(a, KeyEnter) // confirm page
	injectKey(a, KeyEnter) // create -> save fails
	if !a.modelForm.open || !a.modelForm.confirm {
		t.Fatalf("save failure closed or exited the confirm page (open=%v confirm=%v)",
			a.modelForm.open, a.modelForm.confirm)
	}
	if a.modelForm.err == "" {
		t.Fatal("save failure left no error message")
	}
}

func TestModelFormCursorFollowsEditing(t *testing.T) {
	a, sim := newModelFormApp(t)
	openForm(t, a)
	render(a)
	_, cy0, vis := sim.GetCursor()
	if !vis {
		t.Fatal("cursor hidden on a focused text field")
	}
	typeText(a, "ab")
	render(a)
	cx1, cy1, vis := sim.GetCursor()
	if !vis {
		t.Fatal("cursor hidden after typing")
	}
	if cy1 != cy0 {
		t.Fatalf("cursor row moved: %d -> %d", cy0, cy1)
	}
	typeText(a, "c")
	render(a)
	cx2, _, _ := sim.GetCursor()
	if cx2 != cx1+1 {
		t.Fatalf("cursor column = %d, want %d (one column per rune)", cx2, cx1+1)
	}
}

func TestModelFormChoiceFieldsCycle(t *testing.T) {
	a, _ := newModelFormApp(t)
	openForm(t, a)
	injectKey(a, KeyTab) // -> protocol
	injectKey(a, KeyRight)
	if a.modelForm.protocolIdx != 0 {
		t.Fatalf("protocol index = %d, want 0 (single choice wraps)", a.modelForm.protocolIdx)
	}
	injectKey(a, KeyTab) // -> base URL
	injectKey(a, KeyTab) // -> model ID
	injectKey(a, KeyTab) // -> API key
	injectKey(a, KeyTab) // -> thinking echo
	if a.modelForm.echoIdx != 0 {
		t.Fatalf("echo index = %d, want 0 (default on)", a.modelForm.echoIdx)
	}
	injectKey(a, KeyRight)
	if a.modelForm.echoIdx != 1 {
		t.Fatalf("echo index = %d, want 1 after Right", a.modelForm.echoIdx)
	}
	injectKey(a, KeyLeft)
	if a.modelForm.echoIdx != 0 {
		t.Fatalf("echo index = %d, want 0 after Left", a.modelForm.echoIdx)
	}
}

// tabToKind walks from the name row to the reasoning-kind row (name -> ... -> kind).
func tabToKind(t *testing.T, a *App) {
	t.Helper()
	for i := 0; i < 7; i++ { // name, protocol, baseURL, modelID, apiKey, echo, reasoningKey
		injectKey(a, KeyTab)
	}
	if a.modelForm.focus != formFieldReasoningKind {
		t.Fatalf("focus = %v, want reasoning-kind row", a.modelForm.focus)
	}
}

func TestModelFormEffortRowsSeededAndToggled(t *testing.T) {
	a, sim := newModelFormApp(t)
	openForm(t, a)
	tabToKind(t, a)
	injectKey(a, KeyRight) // none -> toggle
	injectKey(a, KeyRight) // toggle -> effort
	if got := a.modelForm.kind(); got != contract.ReasoningKindEffort {
		t.Fatalf("kind = %q, want effort", got)
	}
	// First entry into the effort kind seeds low/medium/high with default medium.
	want := contract.Reasoning{
		Kind: contract.ReasoningKindEffort,
		Efforts: []string{
			contract.ReasoningEffortLow, contract.ReasoningEffortMedium, contract.ReasoningEffortHigh,
		},
		Default: contract.ReasoningEffortMedium,
	}
	if got := a.modelForm.buildReasoning(); !reflect.DeepEqual(got, want) {
		t.Fatalf("seeded reasoning = %+v, want %+v", got, want)
	}

	// The rows render as checkable presets (never any key material).
	render(a)
	dump := screenDump(sim)
	if !strings.Contains(dump, "[x]") || !strings.Contains(dump, "[ ]") {
		t.Fatalf("effort rows missing check marks:\n%s", dump)
	}
	for _, e := range contract.ReasoningEfforts {
		if !strings.Contains(dump, e) {
			t.Fatalf("effort preset %q missing from the form:\n%s", e, dump)
		}
	}

	// Check "max": Tab from kind lands on the "none" row, then walk to "max".
	for i := 0; i < 7; i++ {
		injectKey(a, KeyTab)
	}
	if _, ok := a.modelForm.focus.effortIdx(); !ok {
		t.Fatalf("focus = %v, want an effort row", a.modelForm.focus)
	}
	injectKey(a, KeyRight) // toggle "max" on
	got := a.modelForm.buildReasoning()
	if len(got.Efforts) != 4 || got.Efforts[3] != contract.ReasoningEffortMax {
		t.Fatalf("efforts = %+v, want low/medium/high/max", got.Efforts)
	}
}

func TestModelFormEffortDefaultFollowsChecks(t *testing.T) {
	a, _ := newModelFormApp(t)
	openForm(t, a)
	tabToKind(t, a)
	injectKey(a, KeyRight)
	injectKey(a, KeyRight) // -> effort kind (low/medium/high checked, default medium)
	// Walk to the "medium" row (rows after kind: none, minimal, low, medium) and uncheck it.
	for i := 0; i < 4; i++ {
		injectKey(a, KeyTab)
	}
	injectKey(a, KeyRight) // uncheck medium
	got := a.modelForm.buildReasoning()
	if got.Default != contract.ReasoningEffortLow {
		t.Fatalf("default = %q, want fall back to lowest supported (low)", got.Default)
	}
	// Uncheck everything: the default clears and Validate refuses to advance.
	for _, row := range []modelFormField{
		formFieldEffortLow, formFieldEffortHigh,
	} {
		a.modelForm.focus = row
		injectKey(a, KeyRight)
	}
	if got := a.modelForm.buildReasoning(); got.Default != "" {
		t.Fatalf("default = %q, want empty with nothing checked", got.Default)
	}
	// Fill the required model ID, then Enter must refuse: no supported level checked.
	a.modelForm.focus = formFieldModelID
	typeText(a, "m1")
	injectKey(a, KeyEnter)
	if a.modelForm.confirm {
		t.Fatal("Enter advanced to confirm with an empty supported set")
	}
	if a.modelForm.err == "" {
		t.Fatal("no validation error for an empty supported set")
	}
}

func TestModelFormToggleKindRows(t *testing.T) {
	a, _ := newModelFormApp(t)
	openForm(t, a)
	tabToKind(t, a)
	injectKey(a, KeyRight) // none -> toggle
	rows := formRows(&a.modelForm)
	if len(rows) != 10 { // base 7 + kind + toggle default + dialect
		t.Fatalf("rows = %v, want 10 rows for the toggle kind", rows)
	}
	injectKey(a, KeyTab) // -> toggle default row
	injectKey(a, KeyRight)
	got := a.modelForm.buildReasoning()
	want := contract.Reasoning{
		Kind:          contract.ReasoningKindToggle,
		ToggleDialect: contract.ToggleDialectEnableThinking,
		Default:       contract.ReasoningToggleOff,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("reasoning = %+v, want %+v", got, want)
	}

	// The dialect row cycles through the three gateway switch shapes.
	injectKey(a, KeyTab) // -> dialect row
	injectKey(a, KeyRight)
	if got := a.modelForm.buildReasoning().ToggleDialect; got != contract.ToggleDialectThink {
		t.Fatalf("dialect = %q, want think (Ollama)", got)
	}
	injectKey(a, KeyRight)
	if got := a.modelForm.buildReasoning().ToggleDialect; got != contract.ToggleDialectThinkingType {
		t.Fatalf("dialect = %q, want thinking-type (GLM)", got)
	}
	injectKey(a, KeyRight) // wraps back
	if got := a.modelForm.buildReasoning().ToggleDialect; got != contract.ToggleDialectEnableThinking {
		t.Fatalf("dialect = %q, want wrap to enable_thinking", got)
	}
	if err := a.modelForm.buildReasoning().Validate(); err != nil {
		t.Fatalf("toggle declaration should validate: %v", err)
	}
}

func TestModelFormConfirmShowsReasoningSummary(t *testing.T) {
	a, sim := newModelFormApp(t)
	openForm(t, a)
	a.modelForm.focus = formFieldModelID
	typeText(a, "m1")
	tabToKind(t, a)
	injectKey(a, KeyRight)
	injectKey(a, KeyRight) // -> effort kind (low, medium, high / default medium)
	injectKey(a, KeyEnter)
	if !a.modelForm.confirm {
		t.Fatalf("did not reach confirm page (err=%q)", a.modelForm.err)
	}
	render(a)
	dump := screenDump(sim)
	for _, want := range []string{
		i18n.T("model_form.field.reasoning_kind"),
		i18n.T("model_form.value.kind.effort"),
		"low, medium, high",
		contract.ReasoningEffortMedium,
	} {
		if !strings.Contains(dump, want) {
			t.Fatalf("confirm page missing %q:\n%s", want, dump)
		}
	}
	// Create and check the stored declaration.
	injectKey(a, KeyEnter)
	got := a.models.load()
	if len(got) != 1 {
		t.Fatalf("stored %d models, want 1", len(got))
	}
	want := contract.Reasoning{
		Kind: contract.ReasoningKindEffort,
		Efforts: []string{
			contract.ReasoningEffortLow, contract.ReasoningEffortMedium, contract.ReasoningEffortHigh,
		},
		Default: contract.ReasoningEffortMedium,
	}
	if !reflect.DeepEqual(got[0].Params.Reasoning, want) {
		t.Fatalf("stored reasoning = %+v, want %+v", got[0].Params.Reasoning, want)
	}
}
