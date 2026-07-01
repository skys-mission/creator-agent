// Package config loads runtime configuration for creator-agent.
//
// Format: TOML. Two files are layered (project deep-merges over global):
//
//  1. Global: $XDG_CONFIG_HOME/creator/config.toml, or ~/.creator/config.toml
//     when XDG_CONFIG_HOME is unset.
//  2. Project: ./.creator/config.toml (current working directory).
//
// Effective values are then resolved against environment variables and command-line
// flags. Final priority (highest to lowest):
//
//  1. Command-line flags (-base-url/-api-key/-model/-profile)
//  2. Environment variables (OPENAI_BASE_URL/OPENAI_API_KEY/OPENAI_MODEL)
//  3. Project config ./.creator/config.toml
//  4. Global config (XDG or ~/.creator)
//  5. Built-in defaults
//
// Architecture: this package owns all serialization concerns. The structs below carry
// the TOML tags; core packages (core/mcp, core/middlewares) stay format-agnostic and
// receive plain Go values converted at the boundary (see convert.go).
package config

import (
	"os"
	"sort"
	"strings"

	"github.com/skys-mission/creator-agent/core/middlewares"
)

// SchemaVersion is the configuration schema version this binary understands. A config
// file may declare a top-level `version`; a higher value triggers a non-fatal warning
// (forward-compatibility hint) and reserves a hook point for future migrations.
const SchemaVersion = 1

// Config is the full configuration (TOML root). Adding a new section is a matter of
// declaring a field with a `toml` tag plus, where relevant, a normalization method and
// a validation rule (validate.go); the load/merge pipeline needs no changes.
type Config struct {
	// Version is the schema version declared by the file (0 = unspecified, treated as current).
	Version int `toml:"version"`

	// Default names the profile used when no -profile flag is given.
	Default  string             `toml:"default"`
	Profiles map[string]Profile `toml:"profiles"`

	Permissions Permissions      `toml:"permissions"`
	Hooks       HooksConfig      `toml:"hooks"`
	Memory      MemoryConfig     `toml:"memory"`
	Skills      SkillsConfig     `toml:"skills"`
	Sandbox     SandboxConfig    `toml:"sandbox"`
	Appearance  AppearanceConfig `toml:"appearance"`

	// ToolsDefaults is the universal layer applied to all tools (lowest override priority
	// above the builtin defaults). Single-tool entries in Tools override these.
	ToolsDefaults ToolConfig `toml:"tools_defaults"`
	// Tools is the per-tool override layer keyed by tool name (highest priority above
	// tools_defaults). Example: [tools.grep] max_matches = 200.
	Tools map[string]ToolConfig `toml:"tools"`

	loadedPath string   // most specific loaded config file path (for hints); empty = none loaded
	warnings   []string // non-fatal load/validation warnings (printed by the caller)
}

// Profile describes a model configuration.
//
// Type specifies the provider type (determines which adapter to use):
//   - "openai" (default/empty): OpenAI Chat Completions, compatible with all OpenAI-compatible
//     endpoints (OpenAI/DeepSeek/Tongyi/Zhipu/Kimi/ollama, etc.)
//   - "openai-responses": OpenAI Responses API (/v1/responses); OpenAI only, not interoperable
//   - "anthropic": Anthropic Messages (/v1/messages); Claude models, with extended thinking
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

// NormalizedType returns the normalized provider type (empty is treated as "openai").
func (p Profile) NormalizedType() string {
	if p.Type == "" {
		return "openai"
	}
	return p.Type
}

// Permissions describes tool permission rules (allow/deny glob patterns) and the active mode.
//
// Rule format: "tool:spec" or "tool". spec is a glob (* matches any characters including /).
// Mode selects the trust level that governs how much the agent may do without asking
// (default/trust/auto/readonly); empty = default. Switchable at runtime via the /mode command.
type Permissions struct {
	Mode  string   `toml:"mode"` // permission mode: default/trust/auto/readonly; empty = default
	Allow []string `toml:"allow"`
	Deny  []string `toml:"deny"`
}

// NormalizedMode returns the validated permission mode (default/trust/auto/readonly), defaulting to "default".
func (p Permissions) NormalizedMode() string {
	return string(middlewares.NormalizeMode(middlewares.Mode(p.Mode)))
}

// HookEntry is a single user hook configuration.
type HookEntry struct {
	Matcher string `toml:"matcher"` // tool name glob ("bash" / "read" / "*"); empty = match all
	Command string `toml:"command"` // shell command; stdin receives JSON payload, stdout returns JSON decision
}

// HooksConfig groups user hook configurations by event (config-layer DTO; converted to
// middlewares.HooksConfig at the boundary in convert.go).
type HooksConfig struct {
	PreToolUse  []HookEntry `toml:"pre_tool_use"`  // before tool execution (can deny/modify)
	PostToolUse []HookEntry `toml:"post_tool_use"` // after tool execution (observation only)
	Stop        []HookEntry `toml:"stop"`          // when the agent finishes
}

// IsEmpty reports whether no hooks are configured.
func (h HooksConfig) IsEmpty() bool {
	return len(h.PreToolUse) == 0 && len(h.PostToolUse) == 0 && len(h.Stop) == 0
}

// MemoryConfig controls the long-term memory store (auto-memory). When Enabled, facts are extracted
// after a session ends and injected into the system prompt next session. Dir defaults to
// ~/.creator/memory.
type MemoryConfig struct {
	Enabled bool   `toml:"enabled"`
	Dir     string `toml:"dir"`
}

// SkillsConfig controls the Skills system (frontmatter prompt packs loaded on demand by the model).
type SkillsConfig struct {
	Enabled bool     `toml:"enabled"`
	Dirs    []string `toml:"dirs"`   // additional scan dirs (project .creator/skills is auto-scanned)
	Budget  int      `toml:"budget"` // summary injection byte budget (default 25000)
}

// SandboxConfig controls OS-level sandboxing for the bash tool (opt-in, default off).
type SandboxConfig struct {
	Enabled   bool     `toml:"enabled"`
	Mode      string   `toml:"mode"`       // "filesystem" (default) / "strict"; reserved, validated but not yet wired
	AllowDirs []string `toml:"allow_dirs"` // extra writable directories outside cwd
}

// AppearanceConfig controls the TUI look (theme + language).
type AppearanceConfig struct {
	Theme    string `toml:"theme"`    // "dark" (default) | "light"
	Language string `toml:"language"` // "en" | "zh"; empty = auto-detect from locale
}

// NormalizedTheme returns the validated theme name, defaulting to "dark" for empty/unknown values.
func (a AppearanceConfig) NormalizedTheme() string {
	switch strings.ToLower(strings.TrimSpace(a.Theme)) {
	case "light":
		return "light"
	default:
		return "dark"
	}
}

// NormalizedLanguage returns the validated UI language. Explicit "zh" or "en" wins; when empty,
// it auto-detects from LANG/LC_ALL/LC_CTYPE (Chinese locales map to "zh", otherwise "en").
func (a AppearanceConfig) NormalizedLanguage() string {
	s := strings.ToLower(strings.TrimSpace(a.Language))
	s = strings.ReplaceAll(s, "_", "-")
	switch s {
	case "zh", "zh-cn", "zh-tw", "zh-hk":
		return "zh"
	case "en":
		return "en"
	}
	for _, key := range []string{"LC_ALL", "LC_CTYPE", "LANG"} {
		loc := strings.ToLower(os.Getenv(key))
		if loc == "" || loc == "c" || loc == "posix" {
			continue
		}
		if strings.HasPrefix(loc, "zh") {
			return "zh"
		}
	}
	return "en"
}

// ToolConfig declares configurable per-tool settings used by the three-layer tool settings
// inheritance (single tool > tools_defaults > builtin default). All fields are optional (zero value
// = unset, inherit the lower layer). Field names map 1:1 to core.ToolSettingsInput.
type ToolConfig struct {
	MaxResultChars int      `toml:"max_result_chars"` // result cap before spilling to disk
	IgnoreDirs     []string `toml:"ignore_dirs"`      // directory names skipped during walks (grep/glob)
	MaxDepth       int      `toml:"max_depth"`        // walk depth (grep/glob) or task recursion depth (task)
	MaxMatches     int      `toml:"max_matches"`      // max grep matches returned
	Timeout        string   `toml:"timeout"`          // bash command timeout (e.g. "120s", "2m")
}
