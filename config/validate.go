package config

import (
	"fmt"
	"strings"
)

// validate performs structural checks after decode and returns non-fatal warnings. It intentionally
// does NOT hard-fail: required runtime values (api_key, profile existence) are enforced in Resolve
// where flags/env have had their say, so a config that looks incomplete on disk can still be made
// valid at runtime. Validation here catches typos and misconfigured enums early, with friendly hints.
func (c *Config) validate() []string {
	var w []string

	if c.Version > SchemaVersion {
		w = append(w, fmt.Sprintf("config version %d is newer than this binary supports (%d); some keys may be ignored — consider upgrading creator-agent", c.Version, SchemaVersion))
	}

	if c.Default != "" {
		if _, ok := c.Profiles[c.Default]; !ok {
			w = append(w, fmt.Sprintf("default profile %q is not defined under [profiles] (have: %v)", c.Default, profileNames(c.Profiles)))
		}
	}

	for _, name := range profileNames(c.Profiles) {
		t := strings.ToLower(strings.TrimSpace(c.Profiles[name].Type))
		if t != "" && t != "openai" && t != "openai-responses" && t != "anthropic" {
			w = append(w, fmt.Sprintf("profile %q has unknown type %q (expected openai, openai-responses, or anthropic)", name, c.Profiles[name].Type))
		}
	}

	if v := strings.ToLower(strings.TrimSpace(c.Permissions.Mode)); v != "" {
		if !oneOf(v, "default", "trust", "auto", "readonly") {
			w = append(w, fmt.Sprintf("permissions.mode %q is not recognized (expected default/trust/auto/readonly); falling back to default", c.Permissions.Mode))
		}
	}

	if v := strings.ToLower(strings.TrimSpace(c.Sandbox.Mode)); v != "" {
		if !oneOf(v, "filesystem", "strict") {
			w = append(w, fmt.Sprintf("sandbox.mode %q is not recognized (expected filesystem/strict)", c.Sandbox.Mode))
		}
	}

	return w
}

// oneOf reports whether v equals one of the allowed values.
func oneOf(v string, allowed ...string) bool {
	for _, a := range allowed {
		if v == a {
			return true
		}
	}
	return false
}
