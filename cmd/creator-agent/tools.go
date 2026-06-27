package main

import (
	"time"

	"github.com/skys-mission/creator-agent/config"
	"github.com/skys-mission/creator-agent/core"
)

// toToolSettingsInput converts a config-layer ToolConfig into the core-layer
// input type used by core.ResolveToolSettings. It parses the Timeout duration
// string here (config stays a thin TOML carrier; duration parsing is a runtime
// concern). An invalid timeout is dropped (zero value = unset), so the lower
// layer or builtin default wins instead of failing startup over one bad field.
func toToolSettingsInput(tc config.ToolConfig) core.ToolSettingsInput {
	out := core.ToolSettingsInput{
		MaxResultChars: tc.MaxResultChars,
		IgnoreDirs:     tc.IgnoreDirs,
		MaxDepth:       tc.MaxDepth,
		MaxMatches:     tc.MaxMatches,
	}
	if tc.Timeout != "" {
		if d, err := time.ParseDuration(tc.Timeout); err == nil {
			out.Timeout = d
		}
	}
	return out
}

// resolveToolSettings merges the three configurable layers (per-tool > defaults
// > builtin default) for a named tool and returns the effective core.ToolSettings.
//
// MCP server-level overrides are NOT applied here — they live in the MCP adapter
// (core/mcp) and take precedence at the adapter's Info() call site, which is the
// only place that knows a tool came from MCP. This function serves the built-in
// tools (read/write/edit/bash/grep/glob/task/skill/todo).
func resolveToolSettings(name string, cfg *config.Config, builtinDefault core.ToolSettingsInput) core.ToolSettings {
	var perTool core.ToolSettingsInput
	if cfg != nil {
		if tc, ok := cfg.Tools[name]; ok {
			perTool = toToolSettingsInput(tc)
		}
	}
	var defaults core.ToolSettingsInput
	if cfg != nil {
		defaults = toToolSettingsInput(cfg.ToolsDefaults)
	}
	return core.ResolveToolSettings(builtinDefault, defaults, perTool)
}
