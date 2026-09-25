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
// The dialog has three pages:
//
//	edit      — identity fields + one "reasoning settings" entry row
//	reasoning — the second-level settings page (thinking switch, levels, dialect, wire key, echo)
//	confirm   — review + create
//
// The thinking switch is a plain on/off control (never a kind cycle): turning it on reveals the
// allowed levels and the default level; the switch dialect row models the rare on/off-only
// gateways. Supported levels and the switch dialect are mutually exclusive capabilities — setting
// one clears the other — so the stored declaration can never contradict the form.
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
	formFieldReasoningEntry // edit page: opens the reasoning page (shows a one-line summary)

	// Reasoning page.
	formFieldThinkingSwitch // standalone thinking on/off
	formFieldEffortNone     // per-preset "supported" toggles
	formFieldEffortMinimal  //   (one row per contract.ReasoningEfforts entry,
	formFieldEffortLow      //    order and count must match)
	formFieldEffortMedium
	formFieldEffortHigh
	formFieldEffortXhigh
	formFieldEffortMax
	formFieldReasoningDefault   // default level among the supported ones
	formFieldToggleDialect      // on/off-only gateway switch shape ("not used" for effort models)
	formFieldToggleCustom       // free-text wire field path for the custom switch dialect
	formFieldReasoningKeyChoice // reasoning-content wire field: auto / preset / custom
	formFieldReasoningKeyCustom // free-text wire field name for the custom preset
	formFieldThinkingEcho       // thinking history round-trip
)

// modelFormState is the create-model overlay state.
type modelFormState struct {
	open          bool
	confirm       bool // true = the confirm page is showing
	reasoning     bool // true = the reasoning page is showing (edit page underneath)
	focus         modelFormField
	err           string // inline validation/save error; cleared on the next field move
	effortsSeeded bool   // the effort presets were seeded once; off/on cycles must not re-seed

	name               inputBuffer
	baseURL            inputBuffer
	modelID            inputBuffer
	apiKey             inputBuffer
	reasoningKeyCustom inputBuffer
	toggleCustom       inputBuffer

	protocolIdx      int  // index into protocolChoices()
	switchOn         bool // the standalone thinking switch
	toggleDialectIdx int  // 0 = not used; 1..n = contract.ReasoningToggleDialects[i-1]
	effortsOn        [len(contract.ReasoningEfforts)]bool
	effortDefaultIdx int // index into contract.ReasoningEfforts; -1 = nothing checked
	reasoningKeyIdx  int // index into reasoningKeyChoices(); 0 = auto (smart adaptation)
	echoIdx          int // index into echoChoices(); 0 = on (the default)
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

// reasoningKeyChoices returns the reasoning-content wire-field presets in cycle order. The first
// entry ("" = auto) is the recommended default: smart adaptation scans every known dialect
// inbound and echoes the dialect the endpoint spoke. The rest pin one wire name; the last is the
// custom free-text entry.
func reasoningKeyChoices() []string {
	return []string{"", "reasoning_content", "reasoning_details", "reasoning", "reasoning_text", "-"}
}

// reasoningKeyCustomIdx is the index of the custom (free-text) preset in reasoningKeyChoices().
const reasoningKeyCustomIdx = 5

// openModelForm opens the create-model dialog with a fresh (defaulted) form.
func openModelForm(a *App) {
	a.modelForm = modelFormState{open: true, effortDefaultIdx: -1}
	a.modelForm.focus = formFieldName
	a.forceRender = true
}

// closeModelForm dismisses the dialog without touching the store.
func closeModelForm(a *App) {
	a.modelForm = modelFormState{}
	a.forceRender = true
}

// effortIdx maps an effort row to its contract.ReasoningEfforts index.
func (f modelFormField) effortIdx() (int, bool) {
	if f >= formFieldEffortNone && f <= formFieldEffortMax {
		return int(f - formFieldEffortNone), true
	}
	return 0, false
}

// formRows returns the visible rows, top to bottom, for the current page and form state. The
// capability rows (levels, default level, switch dialect) appear only while the thinking switch
// is on — flipping it on is what reveals them.
func formRows(f *modelFormState) []modelFormField {
	if f.reasoning {
		return reasoningRows(f)
	}
	return []modelFormField{
		formFieldName, formFieldProtocol, formFieldBaseURL, formFieldModelID, formFieldAPIKey,
		formFieldReasoningEntry,
	}
}

// reasoningRows returns the rows of the reasoning settings page.
func reasoningRows(f *modelFormState) []modelFormField {
	rows := []modelFormField{formFieldThinkingSwitch}
	if f.switchOn {
		for i := range contract.ReasoningEfforts {
			rows = append(rows, formFieldEffortNone+modelFormField(i))
		}
		if f.anyEffort() {
			rows = append(rows, formFieldReasoningDefault)
		}
		rows = append(rows, formFieldToggleDialect)
		if f.dialect() == contract.ToggleDialectCustom {
			rows = append(rows, formFieldToggleCustom)
		}
	}
	rows = append(rows, formFieldReasoningKeyChoice)
	if f.reasoningKeyIdx == reasoningKeyCustomIdx {
		rows = append(rows, formFieldReasoningKeyCustom)
	}
	rows = append(rows, formFieldThinkingEcho)
	return rows
}

// anyEffort reports whether at least one supported level is checked.
func (f *modelFormState) anyEffort() bool {
	for _, on := range f.effortsOn {
		if on {
			return true
		}
	}
	return false
}

// isText reports whether the field is a free-text field (vs. a cycle-choice field).
func (f modelFormField) isText() bool {
	switch f {
	case formFieldProtocol, formFieldThinkingSwitch, formFieldReasoningDefault,
		formFieldToggleDialect, formFieldReasoningKeyChoice, formFieldThinkingEcho,
		formFieldReasoningEntry:
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
	case formFieldReasoningKeyCustom:
		return &f.reasoningKeyCustom
	case formFieldToggleCustom:
		return &f.toggleCustom
	}
	return nil
}

// dialect returns the currently selected toggle dialect ("" when "not used").
func (f *modelFormState) dialect() contract.ReasoningToggleDialect {
	if f.toggleDialectIdx <= 0 || f.toggleDialectIdx > len(contract.ReasoningToggleDialects) {
		return ""
	}
	return contract.ReasoningToggleDialects[f.toggleDialectIdx-1]
}

// buildReasoning assembles the reasoning declaration from the reasoning page. The rules encode
// "tell the endpoint not to think in its own dialect, or stay silent":
//
//   - levels checked, switch on  -> effort, Default = the chosen default level
//   - levels checked, switch off -> effort, Default = none (only when "none" is among the
//     supported levels — the endpoint declared it accepts explicit off); otherwise silent
//   - no levels, dialect chosen  -> toggle, Default = on/off straight from the switch
//   - otherwise                  -> nothing is sent (the fail-safe default)
func (f *modelFormState) buildReasoning() contract.Reasoning {
	if f.anyEffort() {
		r := contract.Reasoning{Kind: contract.ReasoningKindEffort}
		for i, on := range f.effortsOn {
			if on {
				r.Efforts = append(r.Efforts, contract.ReasoningEfforts[i])
			}
		}
		switch {
		case f.switchOn:
			if f.effortDefaultIdx >= 0 && f.effortDefaultIdx < len(contract.ReasoningEfforts) {
				r.Default = contract.ReasoningEfforts[f.effortDefaultIdx]
			}
		case f.effortsOn[effortIndex(contract.ReasoningEffortNone)]:
			r.Default = contract.ReasoningEffortNone
		default:
			// The endpoint accepts no explicit off: sending any level would turn thinking ON,
			// so the adapters stay silent instead.
			return contract.Reasoning{}
		}
		return r
	}
	if d := f.dialect(); d != "" {
		def := contract.ReasoningToggleOff
		if f.switchOn {
			def = contract.ReasoningToggleOn
		}
		r := contract.Reasoning{
			Kind:          contract.ReasoningKindToggle,
			ToggleDialect: d,
			Default:       def,
		}
		if d == contract.ToggleDialectCustom {
			// An empty field fails Reasoning.Validate() on submit — fail loud rather than
			// silently dropping the switch the user asked for.
			r.ToggleField = strings.TrimSpace(f.toggleCustom.Value())
		}
		return r
	}
	return contract.Reasoning{}
}

// build assembles the Model from the current fields. Choice fields always carry a value; text
// fields are trimmed (a pasted secret never carries meaningful leading/trailing spaces).
func (f *modelFormState) build() contract.Model {
	key := ""
	if f.reasoningKeyIdx == reasoningKeyCustomIdx {
		key = strings.TrimSpace(f.reasoningKeyCustom.Value())
	} else if f.reasoningKeyIdx > 0 {
		key = reasoningKeyChoices()[f.reasoningKeyIdx]
	}
	return contract.Model{
		Name:     strings.TrimSpace(f.name.Value()),
		Protocol: protocolChoices()[f.protocolIdx],
		BaseURL:  strings.TrimSpace(f.baseURL.Value()),
		ModelID:  strings.TrimSpace(f.modelID.Value()),
		APIKey:   strings.TrimSpace(f.apiKey.Value()),
		Params: contract.Params{
			ThinkingEcho: echoChoices()[f.echoIdx],
			ReasoningKey: key,
			Reasoning:    f.buildReasoning(),
		},
	}
}

// handleModelFormKey routes keys to the create-model dialog. The form consumes every key while
// open. Navigation is ↑↓/Tab(Shift-Tab); ←→ cycles choice fields (effort rows toggle); text
// fields take the usual editing keys. Enter always means "forward": on the entry row it opens
// the reasoning page, everywhere else it validates and advances to the confirm page. Esc backs
// out one level (reasoning page → edit page → closed).
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
	case KeyCtrlC:
		closeModelForm(a)
		return
	case KeyEsc:
		if f.reasoning {
			f.reasoning = false
			f.focus = formFieldReasoningEntry
			f.err = ""
			a.forceRender = true
			return
		}
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
		} else if f.focus != formFieldReasoningEntry {
			cycleChoice(f, -1)
		}
		return
	case KeyRight:
		// The reasoning entry row opens on Enter only: a Right-open would invite Left to go
		// back, which a drill-in page cannot honor. ←→ stays a pure value key everywhere.
		if f.focus.isText() {
			f.textBuf(f.focus).CursorRight()
		} else if f.focus != formFieldReasoningEntry {
			cycleChoice(f, +1)
		}
		return
	case KeyEnter:
		if f.focus == formFieldReasoningEntry && !f.reasoning {
			openReasoningPage(a)
			return
		}
		m := f.build()
		if err := m.Validate(); err != nil {
			f.err = fmt.Sprintf(i18n.T("model_form.err.invalid"), err)
			a.forceRender = true
			return
		}
		f.confirm = true
		f.reasoning = false
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

// openReasoningPage opens the reasoning settings page (the edit page stays underneath).
func openReasoningPage(a *App) {
	f := &a.modelForm
	f.reasoning = true
	f.focus = formFieldThinkingSwitch
	f.err = ""
	a.forceRender = true
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

// cycleChoice applies ←→ to the focused choice field. Effort rows toggle rather than cycle. The
// capability rows are mutually exclusive: choosing a switch dialect clears the levels (a
// switch-only model has none), checking any level resets the dialect to "not used".
func cycleChoice(f *modelFormState, delta int) {
	switch f.focus {
	case formFieldProtocol:
		f.protocolIdx = wrapIdx(f.protocolIdx, delta, len(protocolChoices()))
	case formFieldThinkingSwitch:
		f.switchOn = !f.switchOn
		if f.switchOn {
			f.ensureEffortDefaults()
		}
		// Turning the switch off hides the capability rows; the focus is on the switch row,
		// which is always visible.
		f.focus = formFieldThinkingSwitch
	case formFieldToggleDialect:
		f.toggleDialectIdx = wrapIdx(f.toggleDialectIdx, delta, 1+len(contract.ReasoningToggleDialects))
		if f.toggleDialectIdx > 0 {
			f.effortsOn = [len(contract.ReasoningEfforts)]bool{}
			f.effortDefaultIdx = -1
		}
		if f.dialect() != contract.ToggleDialectCustom {
			// The custom text row disappears; keep the focus on the choice row.
			f.focus = formFieldToggleDialect
		}
	case formFieldReasoningDefault:
		f.cycleEffortDefault(delta)
	case formFieldReasoningKeyChoice:
		f.reasoningKeyIdx = wrapIdx(f.reasoningKeyIdx, delta, len(reasoningKeyChoices()))
		if f.reasoningKeyIdx != reasoningKeyCustomIdx {
			// The custom text row disappears; keep the focus on the choice row.
			f.focus = formFieldReasoningKeyChoice
		}
	case formFieldThinkingEcho:
		f.echoIdx = wrapIdx(f.echoIdx, delta, len(echoChoices()))
	default:
		if i, ok := f.focus.effortIdx(); ok {
			f.effortsOn[i] = !f.effortsOn[i]
			if f.effortsOn[i] {
				// Levels and the switch dialect are exclusive capabilities.
				f.toggleDialectIdx = 0
			}
			if !f.anyEffort() && f.focus == formFieldReasoningDefault {
				// The default-level row disappears; the dialect row takes its place.
				f.focus = formFieldToggleDialect
			}
			f.normalizeEfforts()
		}
	}
}

// ensureEffortDefaults seeds a sensible supported set the first time the thinking switch turns
// on: low/medium/high checked, default medium. The user then trims to the model's real subset.
// Seeding happens once per form — flipping the switch off/on must not fight deliberate edits.
func (f *modelFormState) ensureEffortDefaults() {
	if f.effortsSeeded || f.anyEffort() {
		f.effortsSeeded = true
		return
	}
	f.effortsSeeded = true
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
