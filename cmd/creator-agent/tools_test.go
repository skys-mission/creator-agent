package main

import (
	"testing"
	"time"

	"github.com/skys-mission/creator-agent/config"
	"github.com/skys-mission/creator-agent/core"
)

// TestResolveToolSettingsCmdLayer verifies the cmd-layer resolver merges the
// config three layers (per-tool > defaults > builtin) into core.ToolSettings,
// matching the inheritance contract the user configured.
func TestResolveToolSettingsCmdLayer(t *testing.T) {
	builtin := core.ToolSettingsInput{MaxMatches: 100, MaxDepth: 20, IgnoreDirs: []string{".git"}}

	t.Run("all layers unset returns builtin", func(t *testing.T) {
		cfg := &config.Config{}
		got := resolveToolSettings("grep", cfg, builtin)
		if got.MaxMatches != 100 {
			t.Errorf("MaxMatches = %d, want 100 (builtin)", got.MaxMatches)
		}
	})

	t.Run("defaults layer overrides builtin", func(t *testing.T) {
		cfg := &config.Config{
			ToolsDefaults: config.ToolConfig{MaxMatches: 200},
		}
		got := resolveToolSettings("grep", cfg, builtin)
		if got.MaxMatches != 200 {
			t.Errorf("MaxMatches = %d, want 200 (tools_defaults)", got.MaxMatches)
		}
	})

	t.Run("per-tool layer wins over defaults", func(t *testing.T) {
		cfg := &config.Config{
			ToolsDefaults: config.ToolConfig{MaxMatches: 200, MaxDepth: 30},
			Tools: map[string]config.ToolConfig{
				"grep": {MaxMatches: 500},
			},
		}
		got := resolveToolSettings("grep", cfg, builtin)
		if got.MaxMatches != 500 {
			t.Errorf("MaxMatches = %d, want 500 (tools.grep)", got.MaxMatches)
		}
		if got.MaxDepth != 30 {
			t.Errorf("MaxDepth = %d, want 30 (tools_defaults, tools.grep unset)", got.MaxDepth)
		}
	})

	t.Run("timeout string parsed to duration", func(t *testing.T) {
		cfg := &config.Config{
			Tools: map[string]config.ToolConfig{
				"bash": {Timeout: "2m30s"},
			},
		}
		got := resolveToolSettings("bash", cfg, core.ToolSettingsInput{})
		if got.Timeout != 2*time.Minute+30*time.Second {
			t.Errorf("Timeout = %v, want 2m30s", got.Timeout)
		}
	})

	t.Run("invalid timeout dropped (lower layer wins)", func(t *testing.T) {
		cfg := &config.Config{
			Tools: map[string]config.ToolConfig{
				"bash": {Timeout: "not-a-duration"},
			},
		}
		got := resolveToolSettings("bash", cfg, core.ToolSettingsInput{Timeout: 60 * time.Second})
		if got.Timeout != 60*time.Second {
			t.Errorf("Timeout = %v, want 60s (invalid config dropped, builtin wins)", got.Timeout)
		}
	})

	t.Run("nil config returns builtin", func(t *testing.T) {
		got := resolveToolSettings("grep", nil, builtin)
		if got.MaxMatches != 100 {
			t.Errorf("MaxMatches = %d, want 100 (builtin, nil config)", got.MaxMatches)
		}
	})
}
