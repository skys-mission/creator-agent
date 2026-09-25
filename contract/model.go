package contract

import (
	"fmt"
	"net/url"
	"strings"
)

// ModelRequest is the input to a single model call (the loop -> adapter direction).
//
// It is deliberately not StreamInput: one agent execution may contain many model calls, each with
// its own composed history and tool surface. StreamInput is what a client hands the agent;
// ModelRequest is what the agent hands a model.
type ModelRequest struct {
	Messages []Message
	Tools    []ToolInfo // tool surface advertised to the model; empty means no tools offered
}

// Protocol identifies the wire protocol a model endpoint speaks. It is the configuration
// discriminator: vendors are irrelevant, dialects are what matter (most third-party endpoints
// speak the OpenAI Chat Completions dialect).
type Protocol string

const (
	// ProtocolOpenAIChat is the OpenAI Chat Completions wire protocol (POST /chat/completions,
	// SSE streaming), also the dialect of most third-party endpoints.
	ProtocolOpenAIChat Protocol = "openai-chat-completions"
)

// Reserved protocol IDs (designed, not implemented): "anthropic-messages", "openai-responses".

// ThinkingEchoMode controls round-tripping assistant thinking into outbound history — a wire
// behavior, distinct from showing thinking to the user (a UI/display concern owned by the TUI,
// which defaults to visible).
type ThinkingEchoMode string

const (
	// ThinkingEchoOn (default, also the zero value) echoes assistant thinking back into
	// outbound history under the dialect key. Endpoints that don't understand the field ignore
	// it; some gateways require thinking in history.
	ThinkingEchoOn ThinkingEchoMode = "on"

	// ThinkingEchoOff keeps thinking out of outbound requests entirely, for endpoints that
	// reject unknown fields on messages.
	ThinkingEchoOff ThinkingEchoMode = "off"
)

// ReasoningKind says how an endpoint accepts thinking-depth control. The wire shapes are
// fundamentally different — a boolean switch and a level enum are not interchangeable — so the
// configuration branches on capability instead of pretending one field fits all models.
type ReasoningKind string

const (
	// ReasoningKindNone (the zero value) means the endpoint has no thinking control and the
	// adapters send nothing. Fail-safe default: sending a stray thinking parameter is how you
	// get an HTTP 400 out of models that don't accept one.
	ReasoningKindNone ReasoningKind = ""

	// ReasoningKindToggle means the endpoint accepts only a boolean (DashScope enable_thinking,
	// Zhipu/GLM thinking.type, Ollama think): thinking is on or off, depth is not tunable.
	ReasoningKindToggle ReasoningKind = "toggle"

	// ReasoningKindEffort means the endpoint accepts discrete effort levels (OpenAI
	// reasoning_effort, OpenRouter reasoning.effort, Gemini thinking_level, Anthropic
	// output_config.effort). Which levels a given model accepts is model-dependent (vendors
	// publish per-model subsets), hence the supported set is configuration, not a constant.
	ReasoningKindEffort ReasoningKind = "effort"

	// Reserved kind (designed, not implemented): "budget" — numeric thinking budgets (Anthropic
	// thinking.budget_tokens, DashScope thinking_budget, Gemini thinkingBudget).
)

// Effort presets: the neutral ascending superset of the vendor enums (OpenAI reasoning_effort
// spans none..max; OpenRouter reasoning.effort the same; Gemini thinking_level offers only
// minimal..high; mandatory-reasoning models reject "none"). A model declares its own subset via
// Params.Reasoning.Efforts.
const (
	ReasoningEffortNone    = "none" // reasoning off; only where the model accepts it
	ReasoningEffortMinimal = "minimal"
	ReasoningEffortLow     = "low"
	ReasoningEffortMedium  = "medium"
	ReasoningEffortHigh    = "high"
	ReasoningEffortXhigh   = "xhigh"
	ReasoningEffortMax     = "max"
)

// ReasoningEfforts lists every preset in ascending order — the validation domain and the pool the
// TUI lets users check off.
var ReasoningEfforts = [...]string{
	ReasoningEffortNone, ReasoningEffortMinimal, ReasoningEffortLow, ReasoningEffortMedium,
	ReasoningEffortHigh, ReasoningEffortXhigh, ReasoningEffortMax,
}

// Toggle values for ReasoningKindToggle's Default.
const (
	ReasoningToggleOn  = "on"
	ReasoningToggleOff = "off"
)

// ReasoningToggleDialect names the wire shape of a boolean thinking switch. Toggle models cannot
// share one field: gateways disagree on both the field name and the value shape (survey in
// docs/architecture.md §4), so the model object pins the dialect it speaks.
type ReasoningToggleDialect string

const (
	// ToggleDialectEnableThinking sends top-level "enable_thinking": true|false — DashScope/Qwen
	// and most Chinese OpenAI-compatible gateways.
	ToggleDialectEnableThinking ReasoningToggleDialect = "enable_thinking"

	// ToggleDialectThink sends top-level "think": true|false — Ollama and its gateways.
	ToggleDialectThink ReasoningToggleDialect = "think"

	// ToggleDialectThinkingType sends "thinking": {"type": "enabled"|"disabled"} — Zhipu/GLM.
	ToggleDialectThinkingType ReasoningToggleDialect = "thinking-type"
)

// ReasoningToggleDialects lists every dialect in cycle order (the TUI choice pool).
var ReasoningToggleDialects = [...]ReasoningToggleDialect{
	ToggleDialectEnableThinking, ToggleDialectThink, ToggleDialectThinkingType,
}

// Reasoning is the model's thinking-control capability declaration: what the endpoint accepts
// (Kind +, for effort models, the supported levels; for toggle models, the switch dialect) and
// the default to use on every call.
type Reasoning struct {
	Kind          ReasoningKind          // how the endpoint accepts control, see ReasoningKind
	ToggleDialect ReasoningToggleDialect // kind=toggle: which switch shape to send, see ReasoningToggleDialect
	Efforts       []string               // kind=effort: supported presets (non-empty, no duplicates)
	Default       string                 // kind=effort: one of Efforts; kind=toggle: ReasoningToggleOn/Off
}

// Validate reports whether the declaration is internally consistent.
func (r Reasoning) Validate() error {
	switch r.Kind {
	case ReasoningKindNone:
		if len(r.Efforts) != 0 || r.Default != "" || r.ToggleDialect != "" {
			return fmt.Errorf("reasoning: kind %q must not set Efforts/Default/ToggleDialect", r.Kind)
		}
	case ReasoningKindToggle:
		if len(r.Efforts) != 0 {
			return fmt.Errorf("reasoning: toggle kind takes no effort levels")
		}
		if r.Default != ReasoningToggleOn && r.Default != ReasoningToggleOff {
			return fmt.Errorf("reasoning: toggle Default %q (want %q or %q)", r.Default, ReasoningToggleOn, ReasoningToggleOff)
		}
		if !isToggleDialect(r.ToggleDialect) {
			return fmt.Errorf("reasoning: toggle dialect %q is unknown", r.ToggleDialect)
		}
	case ReasoningKindEffort:
		if r.ToggleDialect != "" {
			return fmt.Errorf("reasoning: effort kind takes no toggle dialect")
		}
		if len(r.Efforts) == 0 {
			return fmt.Errorf("reasoning: effort kind needs at least one supported level")
		}
		seen := make(map[string]bool, len(r.Efforts))
		for _, e := range r.Efforts {
			if !isReasoningEffort(e) {
				return fmt.Errorf("reasoning: unknown effort %q", e)
			}
			if seen[e] {
				return fmt.Errorf("reasoning: duplicate effort %q", e)
			}
			seen[e] = true
		}
		if !seen[r.Default] {
			return fmt.Errorf("reasoning: Default %q is not a supported effort", r.Default)
		}
	default:
		return fmt.Errorf("reasoning: unknown kind %q", r.Kind)
	}
	return nil
}

func isReasoningEffort(s string) bool {
	for _, e := range ReasoningEfforts {
		if e == s {
			return true
		}
	}
	return false
}

func isToggleDialect(d ReasoningToggleDialect) bool {
	for _, t := range ReasoningToggleDialects {
		if t == d {
			return true
		}
	}
	return false
}

// Params carries protocol-level call options. The field set grows as the parameter surface is
// agreed; adapters must treat the zero value as "no opinion, use protocol defaults". Options
// here are semantic (protocol-neutral); each protocol subpackage maps them onto its own wire
// dialect.
type Params struct {
	ThinkingEcho ThinkingEchoMode // thinking history round-trip, see ThinkingEchoMode ("" == on)
	ReasoningKey string           // pins the wire field name for reasoning content (non-standard gateways); empty = de-facto scan/default
	Reasoning    Reasoning        // thinking-depth control capability + default, see Reasoning
}

// Model is a configured model endpoint: one value object per configured endpoint, created by the
// TUI (or the config layer) and handed to the adapters factory.
//
// APIKey is the resolved secret. It must never be written into code, docs, config templates,
// tests or logs: only into environment variables and non-tracked local files (the model store
// writes 0600). String never prints it.
type Model struct {
	Name     string   // optional display label; falls back to ModelID
	Protocol Protocol // wire protocol discriminator (required)
	BaseURL  string   // endpoint root including the API prefix (e.g. https://host/v1); empty = official endpoint
	ModelID  string   // provider-side model identifier (required)
	APIKey   string   // resolved secret; never serialize to logs
	Params   Params   // protocol-level call options, see Params
}

// Validate reports whether the model configuration is usable. Empty APIKey is allowed: some
// endpoints (local servers, gateways) do not authenticate callers.
func (m Model) Validate() error {
	if m.Protocol == "" {
		return fmt.Errorf("contract: model %q: Protocol is required", m.Name)
	}
	if m.ModelID == "" {
		return fmt.Errorf("contract: model %q: ModelID is required", m.Name)
	}
	if m.BaseURL != "" {
		u, err := url.Parse(m.BaseURL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return fmt.Errorf("contract: model %q: invalid BaseURL %q", m.Name, m.BaseURL)
		}
	}
	switch m.Params.ThinkingEcho {
	case "", ThinkingEchoOn, ThinkingEchoOff:
	default:
		return fmt.Errorf("contract: model %q: unknown ThinkingEcho mode %q (want %q or %q)",
			m.Name, m.Params.ThinkingEcho, ThinkingEchoOn, ThinkingEchoOff)
	}
	if err := m.Params.Reasoning.Validate(); err != nil {
		return fmt.Errorf("contract: model %q: %w", m.Name, err)
	}
	return nil
}

// DisplayName returns the label for UI and logs.
func (m Model) DisplayName() string {
	if m.Name != "" {
		return m.Name
	}
	return m.ModelID
}

// String masks the API key so the model can be logged or dumped without leaking secrets.
func (m Model) String() string {
	key := "[unset]"
	if m.APIKey != "" {
		key = "[set]"
	}
	return fmt.Sprintf("Model{Name:%s Protocol:%s ModelID:%s BaseURL:%s APIKey:%s Params:%+v}",
		m.Name, m.Protocol, m.ModelID, strings.TrimSuffix(m.BaseURL, "/"), key, m.Params)
}
