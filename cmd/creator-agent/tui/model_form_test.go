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

// openReasoning walks to the "reasoning settings" entry row and opens the second-level page.
func openReasoning(t *testing.T, a *App) {
	t.Helper()
	for !a.modelForm.reasoning && a.modelForm.focus != formFieldReasoningEntry {
		injectKey(a, KeyTab)
	}
	if a.modelForm.focus != formFieldReasoningEntry {
		t.Fatalf("focus = %v, want the reasoning entry row", a.modelForm.focus)
	}
	injectKey(a, KeyEnter)
	if !a.modelForm.reasoning {
		t.Fatal("Enter on the entry row did not open the reasoning page")
	}
}

// fillModel walks the form top-to-bottom filling every field with the values of the walkthrough
// test model: identity fields, then the reasoning page (wire field "reasoning", echo off).
func fillModel(a *App) {
	typeText(a, "my-gpt")
	injectKey(a, KeyTab) // -> protocol (left at the only choice)
	injectKey(a, KeyTab) // -> base URL
	typeText(a, "https://example.com/v1")
	injectKey(a, KeyTab) // -> model ID
	typeText(a, "m1")
	injectKey(a, KeyTab) // -> API key
	typeText(a, "sk-test-abcd1234")
	injectKey(a, KeyTab) // -> reasoning entry
	injectKey(a, KeyEnter)
	injectKey(a, KeyTab) // -> reasoning field choice
	injectKey(a, KeyRight)
	injectKey(a, KeyRight)
	injectKey(a, KeyRight) // auto -> reasoning_content -> reasoning_details -> reasoning
	injectKey(a, KeyTab)   // -> thinking echo
	injectKey(a, KeyRight) // on -> off
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

	openReasoning(t, a)
	// Rows with the switch off: switch, reasoning field, thinking echo.
	if got := formRows(&a.modelForm); !reflect.DeepEqual(got, []modelFormField{
		formFieldThinkingSwitch, formFieldReasoningKeyChoice, formFieldThinkingEcho,
	}) {
		t.Fatalf("reasoning rows (switch off) = %v", got)
	}
	injectKey(a, KeyTab)
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

func TestModelFormSwitchRevealsLevelRows(t *testing.T) {
	a, sim := newModelFormApp(t)
	openForm(t, a)
	openReasoning(t, a)
	injectKey(a, KeyRight) // switch off -> on (seeds low/medium/high, default medium)
	if !a.modelForm.switchOn {
		t.Fatal("Right on the switch row did not turn the switch on")
	}
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
	// Rows grow to switch + 7 presets + default + dialect + wire field + echo.
	if got := len(formRows(&a.modelForm)); got != 12 {
		t.Fatalf("reasoning rows (switch on) = %d, want 12", got)
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

	// Check "max": Tab from the switch lands on the "none" row, then walk to "max".
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
	openReasoning(t, a)
	injectKey(a, KeyRight) // switch on -> seeds low/medium/high, default medium
	// Rows after the switch: none, minimal, low, medium — walk to "medium" and uncheck it.
	for i := 0; i < 4; i++ {
		injectKey(a, KeyTab)
	}
	injectKey(a, KeyRight) // uncheck medium
	got := a.modelForm.buildReasoning()
	if got.Default != contract.ReasoningEffortLow {
		t.Fatalf("default = %q, want fall back to lowest supported (low)", got.Default)
	}
	// Uncheck everything: no level control is declared (fail-safe: nothing is sent), and the
	// default row gives way to the switch-dialect row.
	for _, row := range []modelFormField{
		formFieldEffortLow, formFieldEffortHigh,
	} {
		a.modelForm.focus = row
		injectKey(a, KeyRight)
	}
	if got := a.modelForm.buildReasoning(); !reflect.DeepEqual(got, contract.Reasoning{}) {
		t.Fatalf("reasoning = %+v, want empty declaration with nothing checked", got)
	}
	rows := formRows(&a.modelForm)
	for _, r := range rows {
		if r == formFieldReasoningDefault {
			t.Fatalf("default-level row still visible with nothing checked: %v", rows)
		}
	}
	found := false
	for _, r := range rows {
		if r == formFieldToggleDialect {
			found = true
		}
	}
	if !found {
		t.Fatalf("switch-dialect row missing with nothing checked: %v", rows)
	}
}

func TestModelFormToggleDialectExclusive(t *testing.T) {
	a, _ := newModelFormApp(t)
	openForm(t, a)
	openReasoning(t, a)
	injectKey(a, KeyRight) // switch on -> seeds low/medium/high

	// Walk to the dialect row (switch, 7 presets, default, dialect) and choose a gateway shape:
	// the levels clear — an on/off-only model has none.
	for i := 0; i < 9; i++ {
		injectKey(a, KeyTab)
	}
	if a.modelForm.focus != formFieldToggleDialect {
		t.Fatalf("focus = %v, want the switch-dialect row", a.modelForm.focus)
	}
	injectKey(a, KeyRight) // not used -> enable_thinking
	got := a.modelForm.buildReasoning()
	want := contract.Reasoning{
		Kind:          contract.ReasoningKindToggle,
		ToggleDialect: contract.ToggleDialectEnableThinking,
		Default:       contract.ReasoningToggleOn,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("reasoning = %+v, want %+v (dialect choice must clear the levels)", got, want)
	}
	if err := got.Validate(); err != nil {
		t.Fatalf("toggle declaration should validate: %v", err)
	}

	// The dialect row cycles through every surveyed gateway switch shape and back to "not sent".
	injectKey(a, KeyRight)
	if got := a.modelForm.buildReasoning().ToggleDialect; got != contract.ToggleDialectThink {
		t.Fatalf("dialect = %q, want think (Ollama)", got)
	}
	injectKey(a, KeyRight)
	if got := a.modelForm.buildReasoning().ToggleDialect; got != contract.ToggleDialectThinkingType {
		t.Fatalf("dialect = %q, want thinking-type (GLM)", got)
	}
	injectKey(a, KeyRight)
	if got := a.modelForm.buildReasoning().ToggleDialect; got != contract.ToggleDialectChatTemplateEnableThinking {
		t.Fatalf("dialect = %q, want chat-template-enable-thinking (vLLM Qwen3)", got)
	}
	injectKey(a, KeyRight)
	if got := a.modelForm.buildReasoning().ToggleDialect; got != contract.ToggleDialectChatTemplateThinking {
		t.Fatalf("dialect = %q, want chat-template-thinking (vLLM Granite)", got)
	}
	injectKey(a, KeyRight)
	if got := a.modelForm.buildReasoning().ToggleDialect; got != contract.ToggleDialectReasoningEnabled {
		t.Fatalf("dialect = %q, want reasoning-enabled (OpenRouter)", got)
	}
	injectKey(a, KeyRight)
	if got := a.modelForm.buildReasoning().ToggleDialect; got != contract.ToggleDialectCustom {
		t.Fatalf("dialect = %q, want the custom escape hatch", got)
	}
	injectKey(a, KeyRight) // wraps through "not sent"
	if got := a.modelForm.dialect(); got != "" {
		t.Fatalf("dialect = %q, want wrap to not sent", got)
	}
	if got := a.modelForm.buildReasoning(); !reflect.DeepEqual(got, contract.Reasoning{}) {
		t.Fatalf("reasoning = %+v, want silent declaration at not sent", got)
	}
	injectKey(a, KeyRight)
	if got := a.modelForm.buildReasoning().ToggleDialect; got != contract.ToggleDialectEnableThinking {
		t.Fatalf("dialect = %q, want wrap to enable_thinking", got)
	}

	// Checking a level flips the capability back and resets the dialect to "not used".
	a.modelForm.focus = formFieldEffortMedium
	injectKey(a, KeyRight)
	if got := a.modelForm.dialect(); got != "" {
		t.Fatalf("dialect = %q, want reset to not used when levels are checked", got)
	}
	if got := a.modelForm.buildReasoning().Kind; got != contract.ReasoningKindEffort {
		t.Fatalf("kind = %q, want effort after checking a level", got)
	}
}

func TestModelFormToggleDialectCustom(t *testing.T) {
	a, _ := newModelFormApp(t)
	openForm(t, a)
	openReasoning(t, a)
	injectKey(a, KeyRight) // switch on
	f := &a.modelForm

	// The custom slot is the last cycle entry; selecting it reveals the free-text field row.
	f.focus = formFieldToggleDialect
	f.toggleDialectIdx = len(contract.ReasoningToggleDialects) - 1
	injectKey(a, KeyRight) // -> custom
	if f.dialect() != contract.ToggleDialectCustom {
		t.Fatalf("dialect = %q, want custom", f.dialect())
	}
	found := false
	for _, r := range formRows(f) {
		if r == formFieldToggleCustom {
			found = true
		}
	}
	if !found {
		t.Fatalf("rows = %v, want the custom switch field row", formRows(f))
	}

	// The field name feeds the declaration (dots nest: a.b -> {"a": {"b": ...}}).
	f.focus = formFieldToggleCustom
	typeText(a, "a.b")
	got := f.buildReasoning()
	want := contract.Reasoning{
		Kind:          contract.ReasoningKindToggle,
		ToggleDialect: contract.ToggleDialectCustom,
		ToggleField:   "a.b",
		Default:       contract.ReasoningToggleOn,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("reasoning = %+v, want %+v", got, want)
	}
	if err := got.Validate(); err != nil {
		t.Fatalf("custom declaration should validate: %v", err)
	}

	// An empty custom field must fail validation, not silently drop the switch the user asked for.
	f.toggleCustom = inputBuffer{}
	if err := f.buildReasoning().Validate(); err == nil {
		t.Fatal("empty custom field must fail validation")
	}

	// Leaving the custom slot hides the text row and keeps the focus on the choice row.
	f.focus = formFieldToggleDialect
	injectKey(a, KeyRight) // custom wraps to "not sent"
	if f.dialect() != "" {
		t.Fatalf("dialect = %q, want wrap to not sent", f.dialect())
	}
	for _, r := range formRows(f) {
		if r == formFieldToggleCustom {
			t.Fatalf("rows = %v, want the custom row gone", formRows(f))
		}
	}
	if f.focus != formFieldToggleDialect {
		t.Fatalf("focus = %v, want the choice row", f.focus)
	}
}

func TestModelFormSwitchOffSemantics(t *testing.T) {
	a, _ := newModelFormApp(t)
	openForm(t, a)
	openReasoning(t, a)
	f := &a.modelForm

	// On/off-only model with the switch off: the disable is sent explicitly in the dialect.
	f.toggleDialectIdx = 1 // enable_thinking
	got := f.buildReasoning()
	want := contract.Reasoning{
		Kind:          contract.ReasoningKindToggle,
		ToggleDialect: contract.ToggleDialectEnableThinking,
		Default:       contract.ReasoningToggleOff,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("toggle-off reasoning = %+v, want %+v", got, want)
	}

	// Effort model that accepts explicit off ("none" checked): the switch off sends "none".
	f.toggleDialectIdx = 0
	f.effortsOn[effortIndex(contract.ReasoningEffortNone)] = true
	f.effortsOn[effortIndex(contract.ReasoningEffortLow)] = true
	f.normalizeEfforts()
	got = f.buildReasoning()
	want = contract.Reasoning{
		Kind:    contract.ReasoningKindEffort,
		Efforts: []string{contract.ReasoningEffortNone, contract.ReasoningEffortLow},
		Default: contract.ReasoningEffortNone,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("effort-off reasoning = %+v, want %+v", got, want)
	}

	// Effort model with no off level: sending any level would turn thinking on, so the
	// adapters stay silent instead.
	f.effortsOn[effortIndex(contract.ReasoningEffortNone)] = false
	f.normalizeEfforts()
	if got = f.buildReasoning(); !reflect.DeepEqual(got, contract.Reasoning{}) {
		t.Fatalf("reasoning = %+v, want silent declaration", got)
	}
}

func TestModelFormReasoningKeyPresets(t *testing.T) {
	a, _ := newModelFormApp(t)
	openForm(t, a)
	openReasoning(t, a)
	injectKey(a, KeyTab) // -> reasoning field choice

	wantKeys := []string{"", "reasoning_content", "reasoning_details", "reasoning", "reasoning_text"}
	for i, want := range wantKeys {
		if got := a.modelForm.build().Params.ReasoningKey; got != want {
			t.Fatalf("cycle %d: ReasoningKey = %q, want %q", i, got, want)
		}
		injectKey(a, KeyRight)
	}
	// The last preset is the custom entry: its text row appears and feeds the wire key.
	if a.modelForm.reasoningKeyIdx != reasoningKeyCustomIdx {
		t.Fatalf("reasoningKeyIdx = %d, want the custom preset", a.modelForm.reasoningKeyIdx)
	}
	if rows := formRows(&a.modelForm); rows[2] != formFieldReasoningKeyCustom {
		t.Fatalf("rows = %v, want the custom key row after the choice row", rows)
	}
	injectKey(a, KeyTab) // -> custom text row
	typeText(a, "my_think")
	if got := a.modelForm.build().Params.ReasoningKey; got != "my_think" {
		t.Fatalf("ReasoningKey = %q, want my_think", got)
	}
	// Leaving the custom preset hides the text row again and keeps the focus on the choice row.
	a.modelForm.focus = formFieldReasoningKeyChoice
	injectKey(a, KeyLeft) // custom -> reasoning_text
	if got := formRows(&a.modelForm); len(got) != 3 || got[1] != formFieldReasoningKeyChoice {
		t.Fatalf("rows = %v, want the custom row gone", got)
	}
	if a.modelForm.focus != formFieldReasoningKeyChoice {
		t.Fatalf("focus = %v, want the choice row", a.modelForm.focus)
	}
}

func TestModelFormEntryRowOpensOnEnterOnly(t *testing.T) {
	// ←→ on the entry row must not drill in: a Right-open would invite Left to go back, which
	// the page cannot honor. Enter is the only key that opens.
	a, _ := newModelFormApp(t)
	openForm(t, a)
	for i := 0; i < 5; i++ {
		injectKey(a, KeyTab)
	}
	if a.modelForm.focus != formFieldReasoningEntry {
		t.Fatalf("focus = %v, want the reasoning entry row", a.modelForm.focus)
	}
	injectKey(a, KeyRight)
	if a.modelForm.reasoning {
		t.Fatal("Right opened the reasoning page")
	}
	injectKey(a, KeyLeft)
	if a.modelForm.reasoning {
		t.Fatal("Left opened the reasoning page")
	}
	injectKey(a, KeyEnter)
	if !a.modelForm.reasoning {
		t.Fatal("Enter did not open the reasoning page")
	}
}

func TestModelFormReasoningPageEscBack(t *testing.T) {
	a, _ := newModelFormApp(t)
	openForm(t, a)
	openReasoning(t, a)
	injectKey(a, KeyEsc)
	if !a.modelForm.open || a.modelForm.reasoning {
		t.Fatalf("Esc left the dialog (open=%v reasoning=%v)", a.modelForm.open, a.modelForm.reasoning)
	}
	if a.modelForm.focus != formFieldReasoningEntry {
		t.Fatalf("focus = %v, want the entry row after Esc", a.modelForm.focus)
	}
	injectKey(a, KeyEsc)
	if a.modelForm.open {
		t.Fatal("Esc on the edit page did not close the form")
	}
}

func TestModelFormConfirmShowsReasoningSummary(t *testing.T) {
	a, sim := newModelFormApp(t)
	openForm(t, a)
	a.modelForm.focus = formFieldModelID
	typeText(a, "m1")
	openReasoning(t, a)
	injectKey(a, KeyRight) // switch on -> low, medium, high / default medium
	injectKey(a, KeyEnter) // reasoning page Enter advances to the confirm page
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
		i18n.T("model_form.field.reasoning_key"),
		i18n.T("model_form.value.key.auto"),
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
