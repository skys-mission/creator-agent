package tui

import (
	"fmt"
	"strings"

	"github.com/skys-mission/creator-agent/cmd/creator-agent/tui/i18n"
	"github.com/skys-mission/creator-agent/contract"
)

// The model-creation form (/model → New): a centered dialog collecting the fields of
// contract.Model, validating them with Model.Validate, and saving the result via modelStore. It
// reuses the wizard's field vocabulary (one inputBuffer per text field) but lives on App as a
// full-screen overlay, so it opens from the /model menu at any time.
//
// The row list is dynamic: the reasoning-control rows depend on the capability kind chosen on
// the form itself (none / on-off only / effort levels — see contract.ReasoningKind). Field IDs
// are identities, not positions; formRows(f) is the single source of layout order.
//
// Layout contract with model_form_view.go: the view only reads this state; all mutation happens
// here in the event-loop goroutine (App state discipline).

// modelFormField identifies the focusable rows of the form.
type modelFormField int

const (
	formFieldName modelFormField = iota
	formFieldProtocol
	formFieldBaseURL
	formFieldModelID
	formFieldAPIKey
	formFieldThinkingEcho
	formFieldReasoningKey
	formFieldReasoningKind
	formFieldReasoningToggle // kind=toggle: default on/off
	formFieldToggleDialect   // kind=toggle: which gateway switch shape to send
	formFieldEffortNone      // kind=effort: per-preset "supported" toggles
	formFieldEffortMinimal   //   (one row per contract.ReasoningEfforts entry,
	formFieldEffortLow       //    order and count must match)
	formFieldEffortMedium
	formFieldEffortHigh
	formFieldEffortXhigh
	formFieldEffortMax
	formFieldReasoningDefault // kind=effort: default level among the supported ones
)

// modelFormState is the create-model overlay state.
type modelFormState struct {
	open    bool
	confirm bool // true = the confirm page is showing
	focus   modelFormField
	err     string // inline validation/save error; cleared on the next field move

	name         inputBuffer
	baseURL      inputBuffer
	modelID      inputBuffer
	apiKey       inputBuffer
	reasoningKey inputBuffer

	protocolIdx      int // index into protocolChoices()
	echoIdx          int // index into echoChoices(); 0 = on (the default)
	kindIdx          int // index into kindChoices(); 0 = none (the fail-safe default)
	toggleIdx        int // index into toggleChoices(); 0 = on
	toggleDialectIdx int // index into contract.ReasoningToggleDialects; 0 = enable_thinking
	effortsOn        [len(contract.ReasoningEfforts)]bool
	effortDefaultIdx int // index into contract.ReasoningEfforts; -1 = nothing checked
}

// protocolChoices returns the selectable wire protocols. Only implemented protocols are offered:
// the factory rejects reserved IDs, so the form must not suggest them (fail-closed).
func protocolChoices() []contract.Protocol {
	return []contract.Protocol{contract.ProtocolOpenAIChat}
}

// echoChoices returns the thinking-echo modes in cycle order (on first — on is the default).
func echoChoices() []contract.ThinkingEchoMode {
	return []contract.ThinkingEchoMode{contract.ThinkingEchoOn, contract.ThinkingEchoOff}
}

// kindChoices returns the reasoning-control kinds in cycle order. none leads because it is the
// fail-safe default: a stray thinking parameter is how you 400 a model that takes none.
func kindChoices() []contract.ReasoningKind {
	return []contract.ReasoningKind{contract.ReasoningKindNone, contract.ReasoningKindToggle, contract.ReasoningKindEffort}
}

// toggleChoices returns the on/off values for kind=toggle in cycle order.
func toggleChoices() []string {
	return []string{contract.ReasoningToggleOn, contract.ReasoningToggleOff}
}

// openModelForm opens the create-model dialog with a fresh (defaulted) form.
func openModelForm(a *App) {
	a.modelForm = modelFormState{echoIdx: 0, open: true, effortDefaultIdx: -1}
	a.forceRender = true
}

// closeModelForm dismisses the dialog without touching the store.
func closeModelForm(a *App) {
	a.modelForm = modelFormState{}
	a.forceRender = true
}

// kind returns the currently selected reasoning-control kind.
func (f *modelFormState) kind() contract.ReasoningKind {
	return kindChoices()[f.kindIdx]
}

// effortIdx maps an effort row to its contract.ReasoningEfforts index.
func (f modelFormField) effortIdx() (int, bool) {
	if f >= formFieldEffortNone && f <= formFieldEffortMax {
		return int(f - formFieldEffortNone), true
	}
	return 0, false
}

// formRows returns the visible rows, top to bottom, for the current form state. The reasoning
// rows appear only for the selected kind — a toggle model has no levels to check off.
func formRows(f *modelFormState) []modelFormField {
	rows := []modelFormField{
		formFieldName, formFieldProtocol, formFieldBaseURL, formFieldModelID, formFieldAPIKey,
		formFieldThinkingEcho, formFieldReasoningKey, formFieldReasoningKind,
	}
	switch f.kind() {
	case contract.ReasoningKindToggle:
		rows = append(rows, formFieldReasoningToggle, formFieldToggleDialect)
	case contract.ReasoningKindEffort:
		for i := range contract.ReasoningEfforts {
			rows = append(rows, formFieldEffortNone+modelFormField(i))
		}
		rows = append(rows, formFieldReasoningDefault)
	}
	return rows
}

// isText reports whether the field is a free-text field (vs. a cycle-choice field).
func (f modelFormField) isText() bool {
	switch f {
	case formFieldProtocol, formFieldThinkingEcho, formFieldReasoningKind,
		formFieldReasoningToggle, formFieldToggleDialect, formFieldReasoningDefault:
		return false
	}
	if _, ok := f.effortIdx(); ok {
		return false
	}
	return true
}

// textBuf returns the input buffer backing a text field (nil for choice fields).
func (f *modelFormState) textBuf(k modelFormField) *inputBuffer {
	switch k {
	case formFieldName:
		return &f.name
	case formFieldBaseURL:
		return &f.baseURL
	case formFieldModelID:
		return &f.modelID
	case formFieldAPIKey:
		return &f.apiKey
	case formFieldReasoningKey:
		return &f.reasoningKey
	}
	return nil
}

// buildReasoning assembles the reasoning declaration from the kind-specific rows.
func (f *modelFormState) buildReasoning() contract.Reasoning {
	switch f.kind() {
	case contract.ReasoningKindToggle:
		return contract.Reasoning{
			Kind:          contract.ReasoningKindToggle,
			ToggleDialect: contract.ReasoningToggleDialects[f.toggleDialectIdx],
			Default:       toggleChoices()[f.toggleIdx],
		}
	case contract.ReasoningKindEffort:
		r := contract.Reasoning{Kind: contract.ReasoningKindEffort}
		for i, on := range f.effortsOn {
			if on {
				r.Efforts = append(r.Efforts, contract.ReasoningEfforts[i])
			}
		}
		if f.effortDefaultIdx >= 0 && f.effortDefaultIdx < len(contract.ReasoningEfforts) {
			r.Default = contract.ReasoningEfforts[f.effortDefaultIdx]
		}
		return r
	}
	return contract.Reasoning{}
}

// build assembles the Model from the current fields. Choice fields always carry a value; text
// fields are trimmed (a pasted secret never carries meaningful leading/trailing spaces).
func (f *modelFormState) build() contract.Model {
	return contract.Model{
		Name:     strings.TrimSpace(f.name.Value()),
		Protocol: protocolChoices()[f.protocolIdx],
		BaseURL:  strings.TrimSpace(f.baseURL.Value()),
		ModelID:  strings.TrimSpace(f.modelID.Value()),
		APIKey:   strings.TrimSpace(f.apiKey.Value()),
		Params: contract.Params{
			ThinkingEcho: echoChoices()[f.echoIdx],
			ReasoningKey: strings.TrimSpace(f.reasoningKey.Value()),
			Reasoning:    f.buildReasoning(),
		},
	}
}

// handleModelFormKey routes keys to the create-model dialog. The form consumes every key while
// open: the confirm page answers Enter/Esc only; the edit page navigates with ↑↓/Tab(Shift-Tab),
// cycles choice fields with ←→ (effort rows toggle on ←→), edits text fields with the usual
// editing keys, and Enter either reports a validation error or advances to the confirm page.
func handleModelFormKey(a *App, e *EventKey) {
	k := e.Key()
	f := &a.modelForm
	if f.confirm {
		switch k {
		case KeyEnter:
			commitModelForm(a)
		case KeyEsc, KeyCtrlC:
			// Back to the edit page (Esc) or abandon (Ctrl+C, matching the pickers).
			if k == KeyCtrlC {
				closeModelForm(a)
			} else {
				f.confirm = false
				a.forceRender = true
			}
		}
		return
	}
	switch k {
	case KeyEsc, KeyCtrlC:
		closeModelForm(a)
		return
	case KeyUp, KeyBacktab:
		moveFocus(f, -1)
		a.forceRender = true
		return
	case KeyDown, KeyTab:
		moveFocus(f, +1)
		a.forceRender = true
		return
	case KeyLeft:
		if f.focus.isText() {
			f.textBuf(f.focus).CursorLeft()
		} else {
			cycleChoice(f, -1)
		}
		return
	case KeyRight:
		if f.focus.isText() {
			f.textBuf(f.focus).CursorRight()
		} else {
			cycleChoice(f, +1)
		}
		return
	case KeyEnter:
		m := f.build()
		if err := m.Validate(); err != nil {
			f.err = fmt.Sprintf(i18n.T("model_form.err.invalid"), err)
			a.forceRender = true
			return
		}
		f.confirm = true
		f.err = ""
		a.forceRender = true
		return
	}
	if f.focus.isText() {
		if editTextBuffer(k, e.Rune(), f.textBuf(f.focus)) {
			f.err = ""
		}
	}
}

// moveFocus moves the focus by delta rows within the current row list (clamped, not wrapping —
// the form is a finite document, ↑↓ at the ends should not teleport).
func moveFocus(f *modelFormState, delta int) {
	rows := formRows(f)
	idx := 0
	for i, r := range rows {
		if r == f.focus {
			idx = i
			break
		}
	}
	idx = clampi(idx+delta, 0, len(rows)-1)
	f.focus = rows[idx]
	f.err = ""
}

// cycleChoice applies ←→ to the focused choice field. Effort rows toggle rather than cycle.
func cycleChoice(f *modelFormState, delta int) {
	switch f.focus {
	case formFieldProtocol:
		f.protocolIdx = wrapIdx(f.protocolIdx, delta, len(protocolChoices()))
	case formFieldThinkingEcho:
		f.echoIdx = wrapIdx(f.echoIdx, delta, len(echoChoices()))
	case formFieldReasoningKind:
		f.kindIdx = wrapIdx(f.kindIdx, delta, len(kindChoices()))
		if f.kind() == contract.ReasoningKindEffort {
			f.ensureEffortDefaults()
		}
	case formFieldReasoningToggle:
		f.toggleIdx = wrapIdx(f.toggleIdx, delta, len(toggleChoices()))
	case formFieldToggleDialect:
		f.toggleDialectIdx = wrapIdx(f.toggleDialectIdx, delta, len(contract.ReasoningToggleDialects))
	case formFieldReasoningDefault:
		f.cycleEffortDefault(delta)
	default:
		if i, ok := f.focus.effortIdx(); ok {
			f.effortsOn[i] = !f.effortsOn[i]
			f.normalizeEfforts()
		}
	}
}

// ensureEffortDefaults seeds a sensible supported set the first time the effort rows appear:
// low/medium/high checked, default medium. The user then trims to the model's real subset.
func (f *modelFormState) ensureEffortDefaults() {
	for _, on := range f.effortsOn {
		if on {
			return
		}
	}
	f.effortsOn[effortIndex(contract.ReasoningEffortLow)] = true
	f.effortsOn[effortIndex(contract.ReasoningEffortMedium)] = true
	f.effortsOn[effortIndex(contract.ReasoningEffortHigh)] = true
	f.effortDefaultIdx = effortIndex(contract.ReasoningEffortMedium)
}

// normalizeEfforts keeps the default inside the supported set: unchecking the current default
// moves it to the lowest supported level (or clears it when none is checked).
func (f *modelFormState) normalizeEfforts() {
	if f.effortDefaultIdx >= 0 && f.effortsOn[f.effortDefaultIdx] {
		return
	}
	f.effortDefaultIdx = -1
	for i, on := range f.effortsOn {
		if on {
			f.effortDefaultIdx = i
			return
		}
	}
}

// cycleEffortDefault moves the default level across the supported levels (ascending, wrapping).
func (f *modelFormState) cycleEffortDefault(delta int) {
	var checked []int
	for i, on := range f.effortsOn {
		if on {
			checked = append(checked, i)
		}
	}
	if len(checked) == 0 {
		return
	}
	pos := -1
	for i, idx := range checked {
		if idx == f.effortDefaultIdx {
			pos = i
			break
		}
	}
	f.effortDefaultIdx = checked[wrapIdx(pos, delta, len(checked))]
}

// effortIndex returns the index of a preset in contract.ReasoningEfforts (-1 if unknown).
func effortIndex(effort string) int {
	for i, e := range contract.ReasoningEfforts {
		if e == effort {
			return i
		}
	}
	return -1
}

// wrapIdx moves idx by delta within [0,n), wrapping around.
func wrapIdx(idx, delta, n int) int {
	return (idx + delta%n + n) % n
}

// clampi clamps v into [lo, hi].
func clampi(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// editTextBuffer applies one editing key to a text field. Returns true when the key edited text.
func editTextBuffer(k Key, r rune, buf *inputBuffer) bool {
	switch k {
	case KeyBackspace, KeyBackspace2:
		buf.Backspace()
	case KeyDelete:
		buf.Delete()
	case KeyHome, KeyCtrlA:
		buf.CursorStart()
	case KeyEnd, KeyCtrlE:
		buf.CursorEnd()
	case KeyCtrlU:
		buf.SetValue("")
	case KeyCtrlW:
		buf.WordLeft()
	case KeyRune:
		if r != 0 {
			buf.InsertRune(r)
		}
	default:
		return false
	}
	return true
}

// commitModelForm validates once more and saves the model. On failure the dialog stays open with
// the error shown; on success it closes and a system message reports the created model and store.
func commitModelForm(a *App) {
	f := &a.modelForm
	m := f.build()
	if err := m.Validate(); err != nil {
		f.confirm = false
		f.err = fmt.Sprintf(i18n.T("model_form.err.invalid"), err)
		a.forceRender = true
		return
	}
	n, err := a.models.add(m)
	if err != nil {
		f.err = fmt.Sprintf(i18n.T("model_form.err.save"), err)
		a.forceRender = true
		return
	}
	closeModelForm(a)
	a.notice = fmt.Sprintf(i18n.T("model_form.created"), m.DisplayName(), modelStorePath(a), n)
	// The form stacks over the /model panel, which would cover the main notice line — mirror
	// the result into the panel's flash so the feedback lands where the user is looking.
	if a.modelMenu.open {
		a.modelMenu.flash = a.notice
	}
}

// modelStorePath returns the store path for display in messages (fallback: the bare file name).
func modelStorePath(a *App) string {
	p, err := a.models.resolve()
	if err != nil {
		return contract.ModelsFileName
	}
	return p
}
