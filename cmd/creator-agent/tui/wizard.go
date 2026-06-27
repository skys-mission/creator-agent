package tui

import (
	"fmt"
	"strings"

	"github.com/skys-mission/creator-agent/cmd/creator-agent/tui/i18n"
	"github.com/skys-mission/creator-agent/config"
)

// WizardResult is the outcome of the first-run setup wizard. Confirmed is false when the user
// aborted (Esc/Ctrl+C); in that case the other fields are meaningless and the caller falls back to
// the text guide.
type WizardResult struct {
	Confirmed   bool
	ProfileName string
	Profile     config.Profile
}

// wizardStep enumerates the linear flow of the setup wizard.
type wizardStep int

const (
	stepProvider wizardStep = iota
	stepBaseURL
	stepAPIKey
	stepModel
	stepConfirm
)

// providerPreset is a selectable provider on the first wizard screen. A custom preset prompts for a
// base URL; the others hardcode the well-known OpenAI-compatible endpoint and a sensible default
// model the user can override on the model step.
type providerPreset struct {
	id           string
	label        string
	baseURL      string
	defaultModel string
	custom       bool
}

func wizardPresets() []providerPreset {
	return []providerPreset{
		{id: "deepseek", label: "DeepSeek", baseURL: "https://api.deepseek.com", defaultModel: "deepseek-chat"},
		{id: "openai", label: "OpenAI", baseURL: "https://api.openai.com/v1", defaultModel: "gpt-4o-mini"},
		{id: "custom", label: i18n.T("wizard.provider.custom"), custom: true},
	}
}

// wizard holds the full-screen setup wizard state. It reuses the TUI's low-level primitives
// (terminal, screen, input buffer, palette) without depending on the agent App, so it can run before
// any agent is constructed.
type wizard struct {
	screen *Screen
	term   *Terminal
	w, h   int

	step    wizardStep
	presets []providerPreset
	selIdx  int

	// committedPresetID is the preset id that last seeded baseURL/model; empty before the first
	// commit or after choosing custom. Used so switching presets resets model while re-selecting
	// the same preset preserves user edits on the model step.
	committedPresetID string

	baseURL inputBuffer
	apiKey  inputBuffer
	model   inputBuffer

	result wizardResultState
}

// wizardResultState tracks loop termination: done ends the loop; confirmed distinguishes a successful
// completion from an abort.
type wizardResultState struct {
	done      bool
	confirmed bool
}

// RunSetupWizard runs the interactive first-run setup wizard on the controlling terminal and returns
// the chosen profile. It fully restores the terminal before returning (so the caller can proceed to
// load config / start the TUI without nested terminal ownership). On abort it returns a result with
// Confirmed=false and a nil error.
func RunSetupWizard(lang string) (WizardResult, error) {
	i18n.SetLang(i18n.ParseLang(lang))

	prevCond := widthCond
	widthCond = configureRunewidth()
	defer func() { widthCond = prevCond }()

	term, err := OpenTerminal()
	if err != nil {
		return WizardResult{}, fmt.Errorf("init terminal: %w", err)
	}
	defer term.Close()

	w, h := term.Size()
	wz := &wizard{
		screen:  NewScreen(term, w, h),
		term:    term,
		w:       w,
		h:       h,
		presets: wizardPresets(),
	}
	return wz.run()
}

// run drives the wizard event loop until the user confirms or aborts.
func (wz *wizard) run() (WizardResult, error) {
	quit := make(chan struct{})
	defer close(quit)
	ch := make(chan Event, 32)
	go PumpInput(stdinReader(), ch, quit)

	resizeCh := wz.term.ResizeCh()

	wz.render()
	for {
		select {
		case ev := <-ch:
			if ek, ok := ev.(*EventKey); ok && ek != nil {
				wz.handleKey(ek)
				if wz.result.done {
					return wz.finalize(), nil
				}
			}
			wz.render()
		case <-resizeCh:
			w, h := wz.term.Size()
			wz.w, wz.h = w, h
			wz.screen.SetSize(w, h)
			wz.render()
		}
	}
}

// finalize builds the WizardResult from the collected fields (only meaningful when confirmed).
func (wz *wizard) finalize() WizardResult {
	if !wz.result.confirmed {
		return WizardResult{Confirmed: false}
	}
	preset := wz.presets[wz.selIdx]
	return WizardResult{
		Confirmed:   true,
		ProfileName: preset.id,
		Profile: config.Profile{
			Type:    "openai",
			BaseURL: strings.TrimSpace(wz.baseURL.Value()),
			APIKey:  strings.TrimSpace(wz.apiKey.Value()),
			Model:   strings.TrimSpace(wz.model.Value()),
		},
	}
}

// handleKey dispatches a key event according to the current step.
func (wz *wizard) handleKey(ek *EventKey) {
	k := ek.Key()
	// Ctrl+C aborts from anywhere.
	if k == KeyCtrlC {
		wz.result = wizardResultState{done: true, confirmed: false}
		return
	}
	switch wz.step {
	case stepProvider:
		wz.handleProviderKey(k)
	case stepBaseURL:
		wz.handleTextKey(k, ek.Rune(), &wz.baseURL, stepProvider, stepAPIKey)
	case stepAPIKey:
		wz.handleTextKey(k, ek.Rune(), &wz.apiKey, wz.prevOfAPIKey(), stepModel)
	case stepModel:
		wz.handleTextKey(k, ek.Rune(), &wz.model, stepAPIKey, stepConfirm)
	case stepConfirm:
		wz.handleConfirmKey(k)
	}
}

// prevOfAPIKey returns the step to go back to from the API-key step: the base-URL step for a custom
// provider, otherwise the provider selection.
func (wz *wizard) prevOfAPIKey() wizardStep {
	if wz.presets[wz.selIdx].custom {
		return stepBaseURL
	}
	return stepProvider
}

func (wz *wizard) handleProviderKey(k Key) {
	switch k {
	case KeyEsc:
		wz.result = wizardResultState{done: true, confirmed: false}
	case KeyUp:
		if wz.selIdx > 0 {
			wz.selIdx--
		}
	case KeyDown:
		if wz.selIdx < len(wz.presets)-1 {
			wz.selIdx++
		}
	case KeyEnter, KeyTab:
		wz.commitProvider()
	}
}

// commitProvider seeds the base URL and model fields from the chosen preset and advances to the next
// step (base-URL entry for custom, otherwise straight to the API key).
func (wz *wizard) commitProvider() {
	preset := wz.presets[wz.selIdx]
	if preset.custom {
		wz.committedPresetID = ""
		wz.step = stepBaseURL
		return
	}
	wz.baseURL.SetValue(preset.baseURL)
	if wz.committedPresetID != preset.id || strings.TrimSpace(wz.model.Value()) == "" {
		wz.model.SetValue(preset.defaultModel)
	}
	wz.committedPresetID = preset.id
	wz.step = stepAPIKey
}

// handleTextKey edits a text field and handles step transitions. Enter advances to next only when the
// field is non-empty; Esc returns to prev.
func (wz *wizard) handleTextKey(k Key, r rune, buf *inputBuffer, prev, next wizardStep) {
	switch k {
	case KeyEsc:
		wz.step = prev
	case KeyEnter:
		if strings.TrimSpace(buf.Value()) == "" {
			return
		}
		if next == stepModel && strings.TrimSpace(wz.model.Value()) == "" {
			wz.model.SetValue(wz.presets[wz.selIdx].defaultModel)
		}
		wz.step = next
	case KeyBackspace, KeyBackspace2:
		buf.Backspace()
	case KeyDelete:
		buf.Delete()
	case KeyLeft:
		buf.CursorLeft()
	case KeyRight:
		buf.CursorRight()
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
	}
}

func (wz *wizard) handleConfirmKey(k Key) {
	switch k {
	case KeyEnter:
		wz.result = wizardResultState{done: true, confirmed: true}
	case KeyEsc:
		wz.step = stepModel
	}
}
