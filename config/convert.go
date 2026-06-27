package config

import (
	"github.com/skys-mission/creator-agent/core/middlewares"
)

// convert.go is the boundary that turns the config-layer DTOs (which carry TOML tags) into the
// format-agnostic value types the core packages consume. Keeping the conversion here lets core/mcp
// and core/middlewares stay free of any serialization concern.

// ToHooks converts the config-layer hooks into the middleware-layer HooksConfig.
func (c *Config) ToHooks() middlewares.HooksConfig {
	return middlewares.HooksConfig{
		PreToolUse:  toHookEntries(c.Hooks.PreToolUse),
		PostToolUse: toHookEntries(c.Hooks.PostToolUse),
		Stop:        toHookEntries(c.Hooks.Stop),
	}
}

// toHookEntries converts a slice of config HookEntry into middleware HookEntry.
func toHookEntries(in []HookEntry) []middlewares.HookEntry {
	if len(in) == 0 {
		return nil
	}
	out := make([]middlewares.HookEntry, len(in))
	for i, e := range in {
		out[i] = middlewares.HookEntry{Matcher: e.Matcher, Command: e.Command}
	}
	return out
}
