package adapters

import (
	"fmt"
	"net/url"
	"strings"
)

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

// Params carries protocol-level call options. The field set grows as the parameter surface is
// agreed; adapters must treat the zero value as "no opinion, use protocol defaults". Options
// here are semantic (protocol-neutral); each protocol subpackage maps them onto its own wire
// dialect.
type Params struct {
	ThinkingEcho ThinkingEchoMode // thinking history round-trip, see ThinkingEchoMode ("" == on)
	ReasoningKey string           // pins the wire field name for reasoning content (non-standard gateways); empty = de-facto scan/default
}

// Model is a configured model endpoint: one value object per configured endpoint, built by the
// config layer (or tests) and handed to New.
//
// APIKey is the resolved secret. It must never be written into config files, code, docs or logs:
// the config layer resolves it from the environment or a non-tracked credentials file, and only
// the resolved value lands here. String never prints it.
type Model struct {
	Name     string   // optional display label; falls back to ModelID
	Protocol Protocol // wire protocol discriminator (required)
	BaseURL  string   // endpoint root including the API prefix (e.g. https://host/v1); empty = official endpoint
	ModelID  string   // provider-side model identifier (required)
	APIKey   string   // resolved secret; never serialize or log
	Params   Params   // protocol-level call options, see Params
}

// Validate reports whether the model configuration is usable. Empty APIKey is allowed: some
// endpoints (local servers, gateways) do not authenticate callers.
func (m Model) Validate() error {
	if m.Protocol == "" {
		return fmt.Errorf("adapters: model %q: Protocol is required", m.Name)
	}
	if m.ModelID == "" {
		return fmt.Errorf("adapters: model %q: ModelID is required", m.Name)
	}
	if m.BaseURL != "" {
		u, err := url.Parse(m.BaseURL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return fmt.Errorf("adapters: model %q: invalid BaseURL %q", m.Name, m.BaseURL)
		}
	}
	switch m.Params.ThinkingEcho {
	case "", ThinkingEchoOn, ThinkingEchoOff:
	default:
		return fmt.Errorf("adapters: model %q: unknown ThinkingEcho mode %q (want %q or %q)",
			m.Name, m.Params.ThinkingEcho, ThinkingEchoOn, ThinkingEchoOff)
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
