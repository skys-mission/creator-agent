package contract

import (
	"net/url"
	"sort"
	"strings"
)

// Profile is one named model-provider configuration. The TUI's /model picker lists profiles and
// shows their host and variant; it never interprets the connection fields beyond that.
type Profile struct {
	Type           string `toml:"type"`
	BaseURL        string `toml:"base_url"`
	APIKey         string `toml:"api_key"`
	Model          string `toml:"model"`
	RequestTimeout string `toml:"request_timeout"` // total streaming timeout, e.g. "10m"/"600s"; empty = default

	// Variant names the profile's default variant (applied unless the user selects another at
	// runtime via /variants). Empty = the synthetic "Default" variant (no overrides).
	Variant  string             `toml:"variant"`
	Variants map[string]Variant `toml:"variants"` // named request-override presets; switchable at runtime via /variants
}

// Variant is a named preset of request overrides applied at LLM-call time. A profile may declare
// several; the user picks one (or the synthetic "Default") via the /variants picker. Headers merge
// onto the HTTP request, body merges into the JSON payload, and the typed generation params (when
// non-nil) take precedence over the agent's defaults.
type Variant struct {
	Headers     map[string]string `toml:"headers"`     // HTTP headers merged onto each request
	Body        map[string]any    `toml:"body"`        // arbitrary keys merged into the JSON request body
	Temperature *float64          `toml:"temperature"` // sampling temperature override (nil = no override)
	TopP        *float64          `toml:"top_p"`       // nucleus sampling override (nil = no override)
	MaxTokens   *int              `toml:"max_tokens"`  // max output tokens override (nil = no override)
}

// VariantNames returns the sorted variant names declared on the profile, or nil if none.
func (p Profile) VariantNames() []string {
	if len(p.Variants) == 0 {
		return nil
	}
	names := make([]string, 0, len(p.Variants))
	for k := range p.Variants {
		names = append(names, k)
	}
	sort.Strings(names)
	return names
}

// HostOf returns the host of base_url (for display). When base_url is empty, falls back to the
// official OpenAI host. A pure function of the base_url string so the entry point and the TUI share
// one implementation and always render the same host.
func HostOf(baseURL string) string {
	s := strings.TrimSpace(baseURL)
	if s == "" {
		return "api.openai.com"
	}
	if u, err := url.Parse(s); err == nil && u.Host != "" {
		return u.Host
	}
	if !strings.Contains(s, "://") {
		if u, err := url.Parse("http://" + s); err == nil && u.Host != "" {
			return u.Host
		}
	}
	s = strings.TrimPrefix(strings.TrimPrefix(s, "https://"), "http://")
	if i := strings.IndexByte(s, '/'); i >= 0 {
		s = s[:i]
	}
	return s
}
