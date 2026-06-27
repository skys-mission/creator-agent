package config

import (
	"fmt"
	"os"
	"sort"
	"strings"
)

// Resolve merges priorities and returns the final effective Profile.
//
// profileName: specifies a profile (empty = uses Default). overrides: flag overrides (empty fields
// ignored). Precedence applied here (highest to lowest): flag > env > profile (file) > builtin default.
func (c *Config) Resolve(profileName string, overrides Profile) (Profile, error) {
	name := profileName
	if name == "" {
		name = c.Default
	}

	var p Profile
	if name != "" {
		prof, ok := c.Profiles[name]
		if !ok {
			return Profile{}, fmt.Errorf("profile %q not found in config (have: %v)", name, profileNames(c.Profiles))
		}
		p = prof
	}

	applyString := func(dst *string, src string) {
		if src != "" {
			*dst = src
		}
	}
	applyString(&p.BaseURL, os.Getenv("OPENAI_BASE_URL"))
	applyString(&p.APIKey, os.Getenv("OPENAI_API_KEY"))
	applyString(&p.Model, os.Getenv("OPENAI_MODEL"))
	applyString(&p.Type, os.Getenv("CREATOR_AGENT_TYPE"))
	applyString(&p.RequestTimeout, os.Getenv("CREATOR_AGENT_REQUEST_TIMEOUT"))

	applyString(&p.Type, overrides.Type)
	applyString(&p.BaseURL, overrides.BaseURL)
	applyString(&p.APIKey, overrides.APIKey)
	applyString(&p.Model, overrides.Model)
	applyString(&p.RequestTimeout, overrides.RequestTimeout)

	if p.Model == "" {
		p.Model = "gpt-4o-mini"
	}
	if p.APIKey == "" {
		return p, fmt.Errorf("no API key found: set api_key under profile %q in %s, or OPENAI_API_KEY env, or -api-key flag", name, configLocation(c.loadedPath))
	}
	if msg := placeholderKeyHint(p.APIKey, name, c.loadedPath); msg != "" {
		// placeholder key (user forgot to fill the template) -> fail early with actionable guidance
		// instead of letting the user hit a raw HTTP 401.
		return p, fmt.Errorf("%s", msg)
	}
	return p, nil
}

// configLocation returns a human-readable description of where to edit the config (the loaded path,
// or a hint about the auto-created file when nothing was loaded).
func configLocation(loadedPath string) string {
	if loadedPath == "" {
		return "config.toml (auto-created on first run; see XDG_CONFIG_HOME or ~/.creator/)"
	}
	return loadedPath
}

// placeholderKeyHint detects keys that look like unfilled placeholders and returns a human-readable
// fix hint (empty = valid key). It only intercepts "obviously placeholder" values to avoid false
// positives on real keys.
func placeholderKeyHint(apiKey, profileName, loadedPath string) string {
	k := strings.TrimSpace(apiKey)
	if k == "" {
		return ""
	}
	known := []string{
		"sk-xxx", "sk-...", "sk-your-key", "sk-your_api_key",
		"sk-test", "sk-demo", "sk-example", "sk-placeholder", "sk-fake",
		"sk-123456", "sk-000000",
		"your-api-key", "your_api_key", "your-key",
		"<your-key>", "<your-api-key>", "<api-key>",
		"replace_me", "your_openai_api_key",
		"xxxxx",
	}
	lower := strings.ToLower(k)
	for _, p := range known {
		if lower == p {
			return looksLikePlaceholderErr(profileName, loadedPath)
		}
	}
	// "sk-" prefix but very short (< 16 chars) and contains no digits: likely a placeholder.
	if strings.HasPrefix(lower, "sk-") && len(k) < 16 && !containsDigit(k) {
		return looksLikePlaceholderErr(profileName, loadedPath)
	}
	return ""
}

// containsDigit reports whether the string contains any digit character (real keys almost always contain hex digits).
func containsDigit(s string) bool {
	for _, r := range s {
		if r >= '0' && r <= '9' {
			return true
		}
	}
	return false
}

// looksLikePlaceholderErr generates a unified placeholder error message.
func looksLikePlaceholderErr(profileName, loadedPath string) string {
	prof := profileName
	if prof == "" {
		prof = "(default profile)"
	}
	return fmt.Sprintf("api_key is still a placeholder (not a real key). Please edit %s and replace the api_key under profile %q with your real key, or use the OPENAI_API_KEY env var / -api-key flag", configLocation(loadedPath), prof)
}

// profileNames returns sorted profile names (for error messages).
func profileNames(m map[string]Profile) []string {
	names := make([]string, 0, len(m))
	for k := range m {
		names = append(names, k)
	}
	sort.Strings(names)
	return names
}
