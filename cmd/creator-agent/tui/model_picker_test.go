package tui

// model_picker_test.go covers the /models overlay: opening from injected profiles, live filtering,
// commit switching via the rebuild factory, the current-profile pin, the /model no-arg-opens-picker
// behavior, and the shared applyProfileSwitch helper.

import (
	"strings"
	"testing"

	"github.com/skys-mission/creator-agent/config"
)

// newAppWithProfiles builds a sim App with injected profiles + a mock rebuild that records the
// last-switched name. The current profile is "current".
func newAppWithProfiles(t *testing.T, profiles map[string]config.Profile) (*App, *string) {
	t.Helper()
	a, _ := newAppWithSim(t, 80, 24)
	a.rt.profiles = profiles
	a.rt.prof = profile{Name: "current", Model: "cur-model", BaseURL: "https://api.cur.com"}
	var switchedTo string
	a.rt.rebuild = func(name string) (config.Profile, error) {
		switchedTo = name
		p := profiles[name]
		return p, nil
	}
	return a, &switchedTo
}

// TestModelPickerOpenFromProfiles verifies /models lists every injected profile.
func TestModelPickerOpenFromProfiles(t *testing.T) {
	profiles := map[string]config.Profile{
		"alpha": {Model: "a-model", BaseURL: "https://a.com"},
		"beta":  {Model: "b-model", BaseURL: "https://b.com"},
	}
	a, _ := newAppWithProfiles(t, profiles)

	if !handleSlashCommand(a, "/models") {
		t.Fatalf("/models should be handled")
	}
	if !a.modelPicker.open {
		t.Fatalf("/models should open the model picker")
	}
	if len(a.modelPicker.entries) != 2 {
		t.Fatalf("picker entries = %d, want 2: %+v", len(a.modelPicker.entries), a.modelPicker.entries)
	}
	got := pickerEntryNames(a.modelPicker.entries)
	for _, want := range []string{"alpha", "beta"} {
		if !strings.Contains(strings.Join(got, ","), want) {
			t.Errorf("picker missing %s; got %v", want, got)
		}
	}
}

// TestModelPickerEmptyProfilesDoesNotOpen verifies that with no profiles configured, /models
// reports the situation instead of opening an empty modal.
func TestModelPickerEmptyProfilesDoesNotOpen(t *testing.T) {
	a, _ := newAppWithSim(t, 80, 24)
	a.rt.profiles = nil
	if !handleSlashCommand(a, "/models") {
		t.Fatalf("/models should be handled")
	}
	if a.modelPicker.open {
		t.Errorf("picker should not open when no profiles are configured")
	}
	// A guidance system message should be emitted.
	if len(a.messages) == 0 {
		t.Fatalf("expected a guidance message about configuring profiles")
	}
	if !strings.Contains(a.messages[len(a.messages)-1].content.String(), "profiles") {
		t.Errorf("guidance message should mention profiles; got %q", a.messages[len(a.messages)-1].content.String())
	}
}

// TestModelPickerCurrentPinnedAndMarked verifies the current profile is pinned to the top.
func TestModelPickerCurrentPinnedAndMarked(t *testing.T) {
	profiles := map[string]config.Profile{
		"current": {Model: "cur", BaseURL: "https://cur.com"},
		"zzz":     {Model: "z", BaseURL: "https://z.com"},
		"aaa":     {Model: "a", BaseURL: "https://a.com"},
	}
	a, _ := newAppWithProfiles(t, profiles)
	openModelPicker(a)
	if len(a.modelPicker.entries) == 0 {
		t.Fatalf("picker should have entries")
	}
	// "current" must be first (pinned) even though "aaa" sorts before it alphabetically.
	if a.modelPicker.entries[0].name != "current" {
		t.Errorf("first entry = %q, want current (pinned)", a.modelPicker.entries[0].name)
	}
}

// TestModelPickerFilter verifies typing narrows the list.
func TestModelPickerFilter(t *testing.T) {
	profiles := map[string]config.Profile{
		"alpha": {Model: "gpt-4o", BaseURL: "https://a.com"},
		"beta":  {Model: "claude", BaseURL: "https://b.com"},
	}
	a, _ := newAppWithProfiles(t, profiles)
	openModelPicker(a)
	// Type "alp" -> only alpha matches (name subsequence).
	for _, r := range "alp" {
		injectRune(a, r)
	}
	for _, e := range a.modelPicker.entries {
		if e.name != "alpha" && e.name != "current" {
			t.Errorf("filter 'alp' kept unexpected %q; menu = %+v", e.name, a.modelPicker.entries)
		}
	}
}

// TestModelPickerCommitSwitches verifies Enter on a selected profile calls rebuild and updates prof.
func TestModelPickerCommitSwitches(t *testing.T) {
	profiles := map[string]config.Profile{
		"current": {Model: "cur", BaseURL: "https://cur.com"},
		"target":  {Model: "tgt-model", BaseURL: "https://tgt.com"},
	}
	a, switchedTo := newAppWithProfiles(t, profiles)
	openModelPicker(a)
	// Select "target" (it's after "current" which is pinned first).
	for a.modelPicker.selIdx < len(a.modelPicker.entries)-1 {
		if a.modelPicker.entries[a.modelPicker.selIdx].name == "target" {
			break
		}
		injectKey(a, KeyDown)
	}
	if a.modelPicker.entries[a.modelPicker.selIdx].name != "target" {
		t.Fatalf("could not select target; landed on %q", a.modelPicker.entries[a.modelPicker.selIdx].name)
	}
	injectKey(a, KeyEnter)
	if a.modelPicker.open {
		t.Errorf("Enter should close the picker")
	}
	if *switchedTo != "target" {
		t.Errorf("rebuild called with %q, want target", *switchedTo)
	}
	if a.rt.prof.Name != "target" || a.rt.prof.Model != "tgt-model" {
		t.Errorf("prof after switch = %+v, want target/tgt-model", a.rt.prof)
	}
}

// TestModelPickerCommitNoopForCurrent verifies committing the current profile does not call rebuild.
func TestModelPickerCommitNoopForCurrent(t *testing.T) {
	profiles := map[string]config.Profile{
		"current": {Model: "cur", BaseURL: "https://cur.com"},
		"other":   {Model: "o", BaseURL: "https://o.com"},
	}
	a, switchedTo := newAppWithProfiles(t, profiles)
	openModelPicker(a)
	// Selection defaults to the current profile (pinned first).
	if a.modelPicker.entries[a.modelPicker.selIdx].name != "current" {
		t.Fatalf("precondition: current should be selected; got %q", a.modelPicker.entries[a.modelPicker.selIdx].name)
	}
	injectKey(a, KeyEnter)
	if *switchedTo != "" {
		t.Errorf("rebuild should not be called for the current profile; called with %q", *switchedTo)
	}
}

// TestModelPickerEscClose verifies Esc dismisses without switching.
func TestModelPickerEscClose(t *testing.T) {
	profiles := map[string]config.Profile{
		"current": {Model: "cur", BaseURL: "https://cur.com"},
		"other":   {Model: "o", BaseURL: "https://o.com"},
	}
	a, switchedTo := newAppWithProfiles(t, profiles)
	openModelPicker(a)
	injectKey(a, KeyEsc)
	if a.modelPicker.open {
		t.Errorf("Esc should close the picker")
	}
	if *switchedTo != "" {
		t.Errorf("Esc should not switch; rebuild called with %q", *switchedTo)
	}
}

// TestModelCommandNoArgOpensPicker verifies /model with no argument opens the picker when profiles
// are configured (opencode-aligned behavior).
func TestModelCommandNoArgOpensPicker(t *testing.T) {
	profiles := map[string]config.Profile{
		"current": {Model: "cur", BaseURL: "https://cur.com"},
	}
	a, _ := newAppWithProfiles(t, profiles)
	if !handleSlashCommand(a, "/model") {
		t.Fatalf("/model should be handled")
	}
	if !a.modelPicker.open {
		t.Errorf("/model with no arg should open the picker when profiles are configured")
	}
}

// TestModelCommandWithArgSwitches verifies the /model <name> text path still switches directly.
func TestModelCommandWithArgSwitches(t *testing.T) {
	profiles := map[string]config.Profile{
		"current": {Model: "cur", BaseURL: "https://cur.com"},
		"target":  {Model: "tgt", BaseURL: "https://tgt.com"},
	}
	a, switchedTo := newAppWithProfiles(t, profiles)
	if !handleSlashCommand(a, "/model target") {
		t.Fatalf("/model target should be handled")
	}
	if *switchedTo != "target" {
		t.Errorf("/model target should call rebuild with target; got %q", *switchedTo)
	}
	if a.rt.prof.Name != "target" {
		t.Errorf("prof after /model target = %q, want target", a.rt.prof.Name)
	}
}

// TestApplyProfileSwitchError verifies applyProfileSwitch surfaces rebuild errors and leaves state
// unchanged.
func TestApplyProfileSwitchError(t *testing.T) {
	a, _ := newAppWithSim(t, 80, 24)
	a.rt.prof = profile{Name: "current", Model: "cur"}
	a.rt.rebuild = func(name string) (config.Profile, error) {
		return config.Profile{}, errFailed
	}
	prevName := a.rt.prof.Name
	err := applyProfileSwitch(a, "bogus")
	if err == nil {
		t.Fatalf("applyProfileSwitch should return the rebuild error")
	}
	if a.rt.prof.Name != prevName {
		t.Errorf("prof name changed on error: %q -> %q", prevName, a.rt.prof.Name)
	}
}

// TestModelsInRegistry verifies /models is registered so it shows in /help and the command palette.
func TestModelsInRegistry(t *testing.T) {
	if findCommand("/models") == nil {
		t.Fatalf("/models should be in the command registry")
	}
	if commandDesc("/models") == "" {
		t.Fatalf("/models should have a description")
	}
}

// pickerEntryNames returns the names of a slice of modelPickerItem, for assertion error messages.
func pickerEntryNames(items []modelPickerItem) []string {
	out := make([]string, 0, len(items))
	for _, it := range items {
		out = append(out, it.name)
	}
	return out
}

// errFailed is a sentinel error reused by the rebuild-error test.
var errFailed = strErr("rebuild failed")

type strErr string

func (e strErr) Error() string { return string(e) }
