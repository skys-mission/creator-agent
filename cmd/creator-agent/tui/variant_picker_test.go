package tui

// variant_picker_test.go covers the /variants overlay: opening from injected variants (incl. the
// synthetic Default row), filtering, commit (switch via the injected factory), Esc close, and the
// unconfigured guard. A switch-call recorder stands in for the real rebuild factory.

import (
	"strings"
	"testing"

	"github.com/skys-mission/creator-agent/config"
)

// newAppWithVariants builds a sim App wired with variants + a switch recorder.
func newAppWithVariants(t *testing.T, variants map[string]config.Variant, current string) (*App, *[]string) {
	t.Helper()
	a, _ := newAppWithSim(t, 80, 24)
	a.rt.variants = variants
	a.rt.currentVariant = current
	calls := []string{}
	a.rt.variantSwitch = func(name string) error { calls = append(calls, name); return nil }
	return a, &calls
}

// TestVariantPickerOpenFromConfig verifies /variants lists Default + each configured variant.
func TestVariantPickerOpenFromConfig(t *testing.T) {
	variants := map[string]config.Variant{
		"fast": {Temperature: pFloat64(0.2)},
		"long": {MaxTokens: pInt(4096)},
	}
	a, _ := newAppWithVariants(t, variants, "")
	if !handleSlashCommand(a, "/variants") {
		t.Fatalf("/variants should be handled")
	}
	if !a.variantPicker.open {
		t.Fatalf("/variants should open the picker")
	}
	// 1 synthetic Default + 2 configured = 3 entries.
	if len(a.variantPicker.entries) != 3 {
		t.Fatalf("entries = %d, want 3 (Default + fast + long): %+v", len(a.variantPicker.entries), a.variantPicker.entries)
	}
	if a.variantPicker.entries[0].name != variantDefaultName {
		t.Errorf("first entry should be the synthetic Default; got %q", a.variantPicker.entries[0].name)
	}
}

// TestVariantPickerNoVariantsReportsUnconfigured verifies the picker guides the user when no
// variants are declared on the profile.
func TestVariantPickerNoVariantsReportsUnconfigured(t *testing.T) {
	a, _ := newAppWithSim(t, 80, 24)
	a.rt.variants = nil
	if !handleSlashCommand(a, "/variants") {
		t.Fatalf("/variants should be handled")
	}
	if a.variantPicker.open {
		t.Errorf("picker should not open without configured variants")
	}
	last := a.messages[len(a.messages)-1].content.String()
	if !strings.Contains(strings.ToLower(last), "no variants") {
		t.Errorf("expected an unconfigured guidance message; got %q", last)
	}
}

// TestVariantPickerCommitSwitches verifies selecting a configured variant switches via the factory.
func TestVariantPickerCommitSwitches(t *testing.T) {
	variants := map[string]config.Variant{
		"fast": {Temperature: pFloat64(0.2)},
	}
	a, calls := newAppWithVariants(t, variants, "")
	openVariantPicker(a)
	// Default (index 0) is preselected; move down to "fast", then commit.
	injectKey(a, KeyDown)
	if a.variantPicker.entries[a.variantPicker.selIdx].name != "fast" {
		t.Fatalf("precondition: fast should be selected after Down; got %q", a.variantPicker.entries[a.variantPicker.selIdx].name)
	}
	injectKey(a, KeyEnter)
	if len(*calls) != 1 || (*calls)[0] != "fast" {
		t.Errorf("switch factory should be called with fast; got %+v", *calls)
	}
	if a.variantPicker.open {
		t.Errorf("picker should close after commit")
	}
	if a.rt.currentVariant != "fast" {
		t.Errorf("currentVariant should be fast; got %q", a.rt.currentVariant)
	}
}

// TestVariantPickerCommitDefaultClears verifies selecting the Default row clears the variant
// (switch factory called with "").
func TestVariantPickerCommitDefaultClears(t *testing.T) {
	variants := map[string]config.Variant{
		"fast": {Temperature: pFloat64(0.2)},
	}
	a, calls := newAppWithVariants(t, variants, "fast")
	openVariantPicker(a)
	// Default is preselected when current is "fast"? No: current is "fast", so preselect = fast.
	// Move up to Default (index 0), then commit.
	for a.variantPicker.entries[a.variantPicker.selIdx].name != variantDefaultName {
		injectKey(a, KeyUp)
		if a.variantPicker.selIdx == 0 {
			break
		}
	}
	if a.variantPicker.entries[a.variantPicker.selIdx].name != variantDefaultName {
		t.Fatalf("precondition: Default should be selected; got %q", a.variantPicker.entries[a.variantPicker.selIdx].name)
	}
	injectKey(a, KeyEnter)
	if len(*calls) != 1 || (*calls)[0] != "" {
		t.Errorf("switch factory should be called with empty name (clear); got %+v", *calls)
	}
}

// TestVariantPickerEscClose verifies Esc dismisses without switching.
func TestVariantPickerEscClose(t *testing.T) {
	variants := map[string]config.Variant{"fast": {}}
	a, calls := newAppWithVariants(t, variants, "")
	openVariantPicker(a)
	injectKey(a, KeyEsc)
	if a.variantPicker.open {
		t.Errorf("Esc should close the picker")
	}
	if len(*calls) != 0 {
		t.Errorf("Esc should not switch; got %+v", *calls)
	}
}

// TestVariantPickerFilterKeepsDefault verifies filtering keeps the Default row so the user can
// always reset, plus any matching configured variants.
func TestVariantPickerFilterKeepsDefault(t *testing.T) {
	variants := map[string]config.Variant{
		"fast": {Temperature: pFloat64(0.2)},
		"long": {MaxTokens: pInt(4096)},
	}
	a, _ := newAppWithVariants(t, variants, "")
	openVariantPicker(a)
	for _, r := range "fas" {
		injectRune(a, r)
	}
	// Default is always kept; "fast" matches "fas"; "long" does not.
	names := make(map[string]bool, len(a.variantPicker.entries))
	for _, e := range a.variantPicker.entries {
		names[e.name] = true
	}
	if !names[variantDefaultName] {
		t.Errorf("Default row should always survive filtering; got %+v", names)
	}
	if !names["fast"] {
		t.Errorf("fast should match 'fas'; got %+v", names)
	}
	if names["long"] {
		t.Errorf("long should not match 'fas'; got %+v", names)
	}
}

// TestVariantPickerInRegistry verifies /variants is registered.
func TestVariantPickerInRegistry(t *testing.T) {
	if findCommand("/variants") == nil {
		t.Fatalf("/variants should be in the command registry")
	}
	if commandDesc("/variants") == "" {
		t.Fatalf("/variants should have a description")
	}
}

// pFloat64 returns a pointer to a float64 (test helper).
func pFloat64(v float64) *float64 { return &v }

// pInt returns a pointer to an int (test helper).
func pInt(v int) *int { return &v }
