package core

import (
	"context"
	"encoding/json"
	"time"
)

// Tool is the interface for tools callable by the agent.
//
// Tools expose capability declarations via Info (for the model and for loop concurrency/permission decisions)
// and execute via Exec. Exec receives json.RawMessage; the tool decodes it itself.
// InputSchema is both the JSON Schema shown to the model and the runtime contract.
type Tool interface {
	Info() ToolInfo
	Exec(ctx context.Context, input json.RawMessage) (ToolResult, error)
}

// ToolInfo describes tool metadata and capabilities.
//
// Capability declarations use fail-closed defaults (zero value = most conservative):
// not declaring ReadOnly means write; not declaring ConcurrencySafe means non-parallel.
type ToolInfo struct {
	Name        string
	Description string
	InputSchema json.RawMessage // JSON Schema for the model

	// ===== Capability declarations (fail-closed) =====
	ReadOnly          bool              // default false (treated as write)
	ConcurrencySafe   bool              // default false (treated as non-parallel)
	Destructive       bool              // default false
	InterruptBehavior InterruptBehavior // default Cancel
	MaxResultChars    int               // >0 spills to disk, returning only a preview + file path
}

// InterruptBehavior defines how a tool responds to user interruption.
type InterruptBehavior int

const (
	// InterruptCancel (default): the tool is canceled on user interrupt.
	InterruptCancel InterruptBehavior = iota
	// InterruptBlock: block until completion, ignoring interrupt (for critical operations that must not be interrupted).
	InterruptBlock
)

// ToolResult is the outcome of a tool execution.
//
// Error handling (dual track):
//   - IsError=true: business error (file not found, command failed) -> fed back to the model so it can retry or try another approach.
//   - Exec returns error != nil: system error -> aborts the loop and emits ErrorEvent.
type ToolResult struct {
	Content string // text for the model
	Parts   []Part // optional images/files
	IsError bool   // business error (distinct from system error)
}

// SkillInfo is the projection of a skill (used by SkillTool for lookups).
//
// Defined in the core package so both middlewares.Skills (data source) and builtins.SkillTool (consumer)
// can import core without circular dependencies.
type SkillInfo struct {
	Name        string
	Description string
	Body        string
	Tools       []string
}

// SkillProvider looks up a skill by name.
// Implemented by middlewares.Skills; consumed by builtins.SkillTool.
type SkillProvider interface {
	LookupSkill(name string) (SkillInfo, bool)
}

// ToolSettings is the resolved configuration for a single tool after applying
// the four-layer inheritance (highest priority first):
//  1. per-tool override (map entry for this tool name)
//  2. defaults layer (applies to all tools)
//  3. builtin default (the tool's own zero-value behavior)
//
// All numeric fields use the zero-value-means-unset convention: a 0 in an
// override/defaults layer means "not specified, inherit the lower layer".
type ToolSettings struct {
	MaxResultChars int           // cap for maybeSpillResult; <=0 means no cap
	IgnoreDirs     []string      // directory names to skip during walks (grep/glob)
	MaxDepth       int           // directory walk depth limit (grep/glob); also task recursion depth
	MaxMatches     int           // max grep matches returned
	Timeout        time.Duration // bash command timeout
}

// ToolSettingsInput is a raw settings layer before merging (mirrors config layer shape).
type ToolSettingsInput struct {
	MaxResultChars int
	IgnoreDirs     []string
	MaxDepth       int
	MaxMatches     int
	Timeout        time.Duration
}

func ResolveToolSettings(builtinDefault ToolSettingsInput, defaults, perTool ToolSettingsInput) ToolSettings {
	out := builtinDefault
	applyLayer(&out, defaults)
	applyLayer(&out, perTool)
	return ToolSettings(out)
}

func applyLayer(dst *ToolSettingsInput, src ToolSettingsInput) {
	if src.MaxResultChars != 0 {
		dst.MaxResultChars = src.MaxResultChars
	}
	if len(src.IgnoreDirs) > 0 {
		dst.IgnoreDirs = append([]string(nil), src.IgnoreDirs...)
	}
	if src.MaxDepth != 0 {
		dst.MaxDepth = src.MaxDepth
	}
	if src.MaxMatches != 0 {
		dst.MaxMatches = src.MaxMatches
	}
	if src.Timeout != 0 {
		dst.Timeout = src.Timeout
	}
}
