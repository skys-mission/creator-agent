package core

import (
	"testing"
	"time"
)

// TestResolveToolSettingsInheritance pins the four-layer merge priority:
// perTool > defaults > builtinDefault, with zero values meaning "inherit".
func TestResolveToolSettingsInheritance(t *testing.T) {
	builtin := ToolSettingsInput{
		MaxResultChars: 10000,
		IgnoreDirs:     []string{".git"},
		MaxDepth:       10,
		MaxMatches:     50,
		Timeout:        30 * time.Second,
	}

	t.Run("no overrides returns builtin defaults", func(t *testing.T) {
		got := ResolveToolSettings(builtin, ToolSettingsInput{}, ToolSettingsInput{})
		if got.MaxResultChars != 10000 || got.MaxDepth != 10 || got.MaxMatches != 50 {
			t.Errorf("builtin defaults not preserved: %+v", got)
		}
		if got.Timeout != 30*time.Second {
			t.Errorf("timeout = %v, want 30s", got.Timeout)
		}
		if len(got.IgnoreDirs) != 1 || got.IgnoreDirs[0] != ".git" {
			t.Errorf("ignoreDirs = %v, want [.git]", got.IgnoreDirs)
		}
	})

	t.Run("defaults layer overrides builtin", func(t *testing.T) {
		def := ToolSettingsInput{MaxMatches: 200, MaxDepth: 25}
		got := ResolveToolSettings(builtin, def, ToolSettingsInput{})
		if got.MaxMatches != 200 {
			t.Errorf("MaxMatches = %d, want 200 (defaults wins)", got.MaxMatches)
		}
		if got.MaxDepth != 25 {
			t.Errorf("MaxDepth = %d, want 25 (defaults wins)", got.MaxDepth)
		}
		// Unset in defaults: builtin value must survive.
		if got.MaxResultChars != 10000 {
			t.Errorf("MaxResultChars = %d, want 10000 (builtin, defaults unset)", got.MaxResultChars)
		}
		if got.Timeout != 30*time.Second {
			t.Errorf("timeout = %v, want 30s (builtin, defaults unset)", got.Timeout)
		}
	})

	t.Run("perTool layer wins over defaults and builtin", func(t *testing.T) {
		def := ToolSettingsInput{MaxMatches: 200, MaxDepth: 25}
		per := ToolSettingsInput{MaxMatches: 500}
		got := ResolveToolSettings(builtin, def, per)
		if got.MaxMatches != 500 {
			t.Errorf("MaxMatches = %d, want 500 (perTool wins)", got.MaxMatches)
		}
		if got.MaxDepth != 25 {
			t.Errorf("MaxDepth = %d, want 25 (defaults, perTool unset)", got.MaxDepth)
		}
	})

	t.Run("IgnoreDirs from higher layer fully replaces lower", func(t *testing.T) {
		def := ToolSettingsInput{IgnoreDirs: []string{"vendor", "build"}}
		per := ToolSettingsInput{IgnoreDirs: []string{".svn"}}
		got := ResolveToolSettings(builtin, def, per)
		if len(got.IgnoreDirs) != 1 || got.IgnoreDirs[0] != ".svn" {
			t.Errorf("IgnoreDirs = %v, want [.svn] (perTool replaces, not merges)", got.IgnoreDirs)
		}
		// defaults layer replacement (perTool unset).
		got = ResolveToolSettings(builtin, def, ToolSettingsInput{})
		if len(got.IgnoreDirs) != 2 {
			t.Errorf("IgnoreDirs = %v, want [vendor build] (defaults replaces builtin)", got.IgnoreDirs)
		}
	})

	t.Run("slice isolation: mutating result does not affect inputs", func(t *testing.T) {
		def := ToolSettingsInput{IgnoreDirs: []string{"a", "b"}}
		got := ResolveToolSettings(builtin, def, ToolSettingsInput{})
		got.IgnoreDirs[0] = "MUTATED"
		if def.IgnoreDirs[0] == "MUTATED" {
			t.Error("mutating result slice corrupted the input slice (aliasing bug)")
		}
	})
}

// TestEstimateCharsAndTokens pins the shared size estimation used by both
// compaction layers (MicroCompact uses chars; Summarization uses tokens=chars/3).
func TestEstimateCharsAndTokens(t *testing.T) {
	// "hello"=5, "world!"=6, {"a":1}=7, "think"=5  →  total 23
	msgs := []Message{
		{Role: RoleUser, Content: "hello"},
		{Role: RoleAssistant, Content: "world!", ToolCalls: []ToolCall{
			{ID: "1", Name: "x", Input: []byte(`{"a":1}`)},
		}},
		{Role: RoleAssistant, Reasoning: "think"},
	}
	const wantChars = 5 + 6 + 7 + 5 // 23
	if got := EstimateChars(msgs); got != wantChars {
		t.Errorf("EstimateChars = %d, want %d", got, wantChars)
	}
	// CJK-aware estimate: 23 ASCII chars / 4 = 5 tokens (was /3 = 7 under the old heuristic).
	const wantTokens = 5
	if got := EstimateTokens(msgs); got != wantTokens {
		t.Errorf("EstimateTokens = %d, want %d", got, wantTokens)
	}
	if got := EstimateChars(nil); got != 0 {
		t.Errorf("EstimateChars(nil) = %d, want 0", got)
	}
}
