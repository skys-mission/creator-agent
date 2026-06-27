package builtins

import (
	"time"

	"github.com/skys-mission/creator-agent/core"
)

// This file exports the builtin-default layer inputs for each configurable tool
// so the cmd layer can feed them into core.ResolveToolSettings as the lowest
// inheritance layer. Keeping the defaults co-located with each tool would split
// them across files; exporting them here gives the cmd layer a single import
// surface and keeps the inheritance resolution in one place.

// GrepDefaultsInput returns the builtin default settings input for the grep tool
// (used as the lowest layer in core.ResolveToolSettings by the cmd layer).
func GrepDefaultsInput() core.ToolSettingsInput {
	return core.ToolSettingsInput{
		MaxResultChars: 20000,
		IgnoreDirs:     append([]string(nil), defaultIgnoreDirs...),
		MaxDepth:       20,
		MaxMatches:     100,
	}
}

// GlobDefaultsInput returns the builtin default settings input for the glob tool.
func GlobDefaultsInput() core.ToolSettingsInput {
	return core.ToolSettingsInput{
		MaxResultChars: 20000,
		IgnoreDirs:     append([]string(nil), defaultIgnoreDirs...),
		MaxDepth:       20,
	}
}

// TaskDefaultsInput returns the builtin default settings input for the task tool.
// MaxDepth here is the task recursion limit (default 2).
func TaskDefaultsInput() core.ToolSettingsInput {
	return core.ToolSettingsInput{
		MaxDepth: defaultMaxTaskDepth,
	}
}

// BashDefaultsInput returns the builtin default settings input for the bash tool.
func BashDefaultsInput() core.ToolSettingsInput {
	return core.ToolSettingsInput{
		Timeout: 60 * time.Second,
	}
}
