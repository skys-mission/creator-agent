package contract

import (
	"strings"
	"testing"
)

func TestModelValidate(t *testing.T) {
	full := Model{
		Name:     "gpt",
		Protocol: ProtocolOpenAIChat,
		BaseURL:  "https://example.com/v1",
		ModelID:  "m1",
		APIKey:   "k",
	}
	tests := []struct {
		name    string
		mutate  func(*Model)
		wantErr string
	}{
		{"full config ok", func(m *Model) {}, ""},
		{"empty key allowed", func(m *Model) { m.APIKey = "" }, ""},
		{"name optional", func(m *Model) { m.Name = "" }, ""},
		{"missing protocol", func(m *Model) { m.Protocol = "" }, "Protocol is required"},
		{"missing model id", func(m *Model) { m.ModelID = "" }, "ModelID is required"},
		{"bad scheme", func(m *Model) { m.BaseURL = "ftp://example.com/v1" }, "invalid BaseURL"},
		{"no host", func(m *Model) { m.BaseURL = "https://" }, "invalid BaseURL"},
		{"unparsable", func(m *Model) { m.BaseURL = "http://[::1" }, "invalid BaseURL"},
		{"echo default on", func(m *Model) { m.Params.ThinkingEcho = "" }, ""},
		{"echo explicit on", func(m *Model) { m.Params.ThinkingEcho = ThinkingEchoOn }, ""},
		{"echo off", func(m *Model) { m.Params.ThinkingEcho = ThinkingEchoOff }, ""},
		{"echo unknown", func(m *Model) { m.Params.ThinkingEcho = "maybe" }, "unknown ThinkingEcho mode"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := full
			tt.mutate(&m)
			err := m.Validate()
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("Validate() = %v, want nil", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("Validate() = %v, want error containing %q", err, tt.wantErr)
			}
		})
	}
}

func TestModelStringMasksAPIKey(t *testing.T) {
	const secret = "sk-super-secret-value"
	m := Model{Protocol: ProtocolOpenAIChat, ModelID: "m1", APIKey: secret}
	s := m.String()
	if strings.Contains(s, secret) {
		t.Fatalf("String() leaked the API key: %s", s)
	}
	if !strings.Contains(s, "[set]") {
		t.Fatalf("String() should report the key as set: %s", s)
	}
	empty := Model{Protocol: ProtocolOpenAIChat, ModelID: "m1"}
	if !strings.Contains(empty.String(), "[unset]") {
		t.Fatalf("String() should report an absent key as unset: %s", empty.String())
	}
}

func TestModelDisplayName(t *testing.T) {
	m := Model{ModelID: "m1"}
	if got := m.DisplayName(); got != "m1" {
		t.Fatalf("DisplayName() = %q, want fallback to ModelID", got)
	}
	m.Name = "my model"
	if got := m.DisplayName(); got != "my model" {
		t.Fatalf("DisplayName() = %q, want %q", got, "my model")
	}
}

func TestReasoningValidate(t *testing.T) {
	cases := []struct {
		name string
		r    Reasoning
		ok   bool
	}{
		{"none clean", Reasoning{}, true},
		{"none with strays", Reasoning{Efforts: []string{ReasoningEffortLow}}, false},
		{"none with dialect", Reasoning{ToggleDialect: ToggleDialectThink}, false},
		{"none with toggle field", Reasoning{ToggleField: "my_flag"}, false},
		{"toggle on", Reasoning{Kind: ReasoningKindToggle, ToggleDialect: ToggleDialectEnableThinking, Default: ReasoningToggleOn}, true},
		{"toggle off", Reasoning{Kind: ReasoningKindToggle, ToggleDialect: ToggleDialectThink, Default: ReasoningToggleOff}, true},
		{"toggle glm dialect", Reasoning{Kind: ReasoningKindToggle, ToggleDialect: ToggleDialectThinkingType, Default: ReasoningToggleOn}, true},
		{"toggle chat-template enable_thinking", Reasoning{Kind: ReasoningKindToggle, ToggleDialect: ToggleDialectChatTemplateEnableThinking, Default: ReasoningToggleOff}, true},
		{"toggle chat-template thinking", Reasoning{Kind: ReasoningKindToggle, ToggleDialect: ToggleDialectChatTemplateThinking, Default: ReasoningToggleOn}, true},
		{"toggle reasoning-enabled", Reasoning{Kind: ReasoningKindToggle, ToggleDialect: ToggleDialectReasoningEnabled, Default: ReasoningToggleOn}, true},
		{"toggle custom root field", Reasoning{Kind: ReasoningKindToggle, ToggleDialect: ToggleDialectCustom, ToggleField: "my_flag", Default: ReasoningToggleOn}, true},
		{"toggle custom nested field", Reasoning{Kind: ReasoningKindToggle, ToggleDialect: ToggleDialectCustom, ToggleField: "a.b", Default: ReasoningToggleOff}, true},
		{"toggle custom missing field", Reasoning{Kind: ReasoningKindToggle, ToggleDialect: ToggleDialectCustom, Default: ReasoningToggleOn}, false},
		{"toggle custom empty segment", Reasoning{Kind: ReasoningKindToggle, ToggleDialect: ToggleDialectCustom, ToggleField: "a.", Default: ReasoningToggleOn}, false},
		{"toggle custom leading dot", Reasoning{Kind: ReasoningKindToggle, ToggleDialect: ToggleDialectCustom, ToggleField: ".b", Default: ReasoningToggleOn}, false},
		{"toggle preset with field", Reasoning{Kind: ReasoningKindToggle, ToggleDialect: ToggleDialectThink, ToggleField: "my_flag", Default: ReasoningToggleOn}, false},
		{"toggle missing dialect", Reasoning{Kind: ReasoningKindToggle, Default: ReasoningToggleOn}, false},
		{"toggle unknown dialect", Reasoning{Kind: ReasoningKindToggle, ToggleDialect: "enable_thinking2", Default: ReasoningToggleOn}, false},
		{"toggle bad default", Reasoning{Kind: ReasoningKindToggle, ToggleDialect: ToggleDialectThink, Default: ReasoningEffortHigh}, false},
		{"toggle with efforts", Reasoning{Kind: ReasoningKindToggle, ToggleDialect: ToggleDialectThink, Efforts: []string{ReasoningEffortLow}, Default: ReasoningToggleOn}, false},
		{"effort ok", Reasoning{Kind: ReasoningKindEffort, Efforts: []string{ReasoningEffortLow, ReasoningEffortHigh}, Default: ReasoningEffortHigh}, true},
		{"effort includes none", Reasoning{Kind: ReasoningKindEffort, Efforts: []string{ReasoningEffortNone, ReasoningEffortMax}, Default: ReasoningEffortNone}, true},
		{"effort with dialect", Reasoning{Kind: ReasoningKindEffort, ToggleDialect: ToggleDialectThink, Efforts: []string{ReasoningEffortLow}, Default: ReasoningEffortLow}, false},
		{"effort with toggle field", Reasoning{Kind: ReasoningKindEffort, ToggleField: "my_flag", Efforts: []string{ReasoningEffortLow}, Default: ReasoningEffortLow}, false},
		{"effort empty set", Reasoning{Kind: ReasoningKindEffort, Default: ReasoningEffortLow}, false},
		{"effort unknown level", Reasoning{Kind: ReasoningKindEffort, Efforts: []string{"ultra"}, Default: "ultra"}, false},
		{"effort duplicate", Reasoning{Kind: ReasoningKindEffort, Efforts: []string{ReasoningEffortLow, ReasoningEffortLow}, Default: ReasoningEffortLow}, false},
		{"effort default not supported", Reasoning{Kind: ReasoningKindEffort, Efforts: []string{ReasoningEffortLow}, Default: ReasoningEffortHigh}, false},
		{"unknown kind", Reasoning{Kind: "budget", Default: ReasoningToggleOn}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.r.Validate()
			if tc.ok && err != nil {
				t.Fatalf("Validate() = %v, want ok", err)
			}
			if !tc.ok && err == nil {
				t.Fatalf("Validate() = nil, want error")
			}
		})
	}

	// Model.Validate must surface the reasoning rules too.
	m := Model{Protocol: ProtocolOpenAIChat, ModelID: "m1",
		Params: Params{Reasoning: Reasoning{Kind: ReasoningKindEffort, Efforts: []string{ReasoningEffortLow}, Default: ReasoningEffortHigh}}}
	if err := m.Validate(); err == nil {
		t.Fatal("Model.Validate must reject a Default outside Efforts")
	}
	m.Params.Reasoning.Default = ReasoningEffortLow
	if err := m.Validate(); err != nil {
		t.Fatalf("Model.Validate() = %v, want ok", err)
	}
}
