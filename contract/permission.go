package contract

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
)

// Mode is a permission mode: a named trust level governing how much the agent may do without asking.
//
// The four modes form a trust gradient (least to most autonomous): readonly -> default -> trust ->
// auto. Each mode is a threshold over the risk of an operation: below the threshold it runs without
// asking; at/above it the TUI asks; catastrophic operations are always blocked regardless of mode.
type Mode string

const (
	// ModeDefault: read-only tools auto-allowed; every write/modify operation asks for approval.
	ModeDefault Mode = "default"
	// ModeTrust: routine local writes (within the workspace) and benign commands auto-allowed;
	// risky operations (delete, network write, sudo, install, outside-workspace writes) still ask.
	ModeTrust Mode = "trust"
	// ModeAuto: everything auto-allowed except catastrophic operations (rm -rf /, mkfs, device writes).
	ModeAuto Mode = "auto"
	// ModeReadonly: all write/modify operations are hard-blocked (overrides even allow rules).
	ModeReadonly Mode = "readonly"
)

// NormalizeMode returns the validated mode, defaulting to ModeDefault for empty/unknown values.
// Tolerates common user-facing aliases (case-insensitive).
func NormalizeMode(m Mode) Mode {
	switch m {
	case ModeDefault, ModeTrust, ModeAuto, ModeReadonly:
		return m
	}
	switch Mode(strings.ToLower(strings.TrimSpace(string(m)))) {
	case "", "default", "ask":
		return ModeDefault
	case "trust", "trusted":
		return ModeTrust
	case "auto", "yolo":
		return ModeAuto
	case "readonly", "read-only", "safe", "view":
		return ModeReadonly
	}
	return ModeDefault
}

// ModeController holds the live permission mode, safe for concurrent read (every tool check) and
// write (TUI /mode switch). The permission layer reads it on each invocation, so the next call after
// Set reflects the new mode with no agent rebuild.
type ModeController struct {
	mu   sync.RWMutex
	mode Mode
}

// NewModeController creates a controller seeded with the given mode (normalized).
func NewModeController(initial Mode) *ModeController {
	return &ModeController{mode: NormalizeMode(initial)}
}

// Get returns the current mode.
func (c *ModeController) Get() Mode {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.mode
}

// Set switches the current mode (normalized). Takes effect on the next tool invocation.
func (c *ModeController) Set(m Mode) {
	c.mu.Lock()
	c.mode = NormalizeMode(m)
	c.mu.Unlock()
}

// AskResolver handles decisions that require user confirmation.
//
// Returns true=allow, false=deny. ctx is used to return false early on interruption (e.g. Ctrl+C),
// so a cancelled approval never blocks indefinitely. Headless mode returns false (default deny); the
// TUI prompts the user. The resolver decides whether to cache the decision.
type AskResolver func(ctx context.Context, toolName, input string) (allow bool)

// AllowSet is a thread-safe, session-level set of approved tool invocations. It is shared by the
// REPL and TUI approvers so both apply identical "remember this approval" semantics instead of each
// re-implementing the map + write/edit grouping (which drifted apart historically).
//
// Keys are ApproveKey(tool, input). The two file-writing tools write and edit are treated as one
// group: approving "all edits" for either allows both, matching users' mental model that "let it
// edit files" is a single decision.
type AllowSet struct {
	mu sync.Mutex
	m  map[string]bool
}

// NewAllowSet returns an empty AllowSet.
func NewAllowSet() *AllowSet {
	return &AllowSet{m: make(map[string]bool)}
}

// Allowed reports whether this exact invocation (or the write/edit group, for write/edit tools) has
// been remembered.
func (a *AllowSet) Allowed(toolName, input string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.m[ApproveKey(toolName, input)] {
		return true
	}
	if isWriteEditTool(toolName) && (a.m["write"] || a.m["edit"]) {
		return true
	}
	return false
}

// RememberExact remembers this specific invocation by its ApproveKey (no grouping).
func (a *AllowSet) RememberExact(toolName, input string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.m[ApproveKey(toolName, input)] = true
}

// RememberWriteEdit remembers the write/edit group so all subsequent write and edit calls are
// allowed.
func (a *AllowSet) RememberWriteEdit() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.m["write"] = true
	a.m["edit"] = true
}

// isWriteEditTool reports whether the tool is one of the grouped file-writing tools.
func isWriteEditTool(toolName string) bool {
	return toolName == "write" || toolName == "edit"
}

// ApproveKey generates a session-level authorization ("a") key, refining the AllowSet granularity
// from pure tool name to tool+input:
//   - bash: by primary command (first segment's first token) → "bash:rm", "bash:git". Authorizing
//     bash:mkdir still requires re-approval for bash:rm.
//   - write/edit: by path → "write:/tmp/x". Same path is allowed on repeat; different path requires
//     re-approval.
//   - others: by tool name (no input dimension).
func ApproveKey(toolName, input string) string {
	switch toolName {
	case "bash":
		cmd := extractBashCommand(json.RawMessage(input))
		if cmd == "" {
			return toolName
		}
		subs := splitBashCommand(cmd)
		if len(subs) == 0 {
			return toolName
		}
		first := strings.Fields(subs[0])
		if len(first) == 0 {
			return toolName
		}
		return "bash:" + first[0]
	case "write", "edit":
		if p := extractFilePath(json.RawMessage(input)); p != "" {
			return toolName + ":" + p
		}
		return toolName
	default:
		return toolName
	}
}

// extractBashCommand extracts the command field from bash tool input JSON.
func extractBashCommand(input json.RawMessage) string {
	if len(input) == 0 {
		return ""
	}
	var args struct {
		Command string `json:"command"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return ""
	}
	return args.Command
}

// extractFilePath extracts the path field from write/edit tool input JSON, so permission keys and
// glob rules address the actual path rather than the raw JSON wrapper.
func extractFilePath(input json.RawMessage) string {
	if len(input) == 0 {
		return ""
	}
	var args struct {
		Path     string `json:"path"`
		FilePath string `json:"file_path"` // some tools use file_path (e.g. read)
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return ""
	}
	if args.Path != "" {
		return args.Path
	}
	return args.FilePath
}

// splitBashCommand splits compound shell commands into subcommands.
//
// Splits by shell control operators: &&  ||  |  ;  &  and newlines.
//
// This is a best-effort heuristic (does not invoke a real shell parser):
//   - Covers common compound forms (&& / || / | / ; / & background);
//   - Preserves the redirect forms &> and >& (they are not command separators);
//   - Does not recognize operators inside quotes, command substitution $(...) / backticks,
//     subshells (...), heredocs, eval, etc.
//
// Biased toward over-splitting (extra checks are harmless). Note that this is only a grouping
// heuristic for approval granularity — command substitution is invisible to it by design, so it is
// no substitute for real isolation.
func splitBashCommand(cmd string) []string {
	s := cmd
	// Normalize multi-character operators to a single placeholder first.
	s = strings.ReplaceAll(s, "&&", "\x00")
	s = strings.ReplaceAll(s, "||", "\x00")
	// Protect redirect operators before splitting the single '&' (background/`a & b`),
	// so `cmd &> file` / `cmd >& file` are not shattered into bogus subcommands.
	s = strings.ReplaceAll(s, "&>", "\x01")
	s = strings.ReplaceAll(s, ">&", "\x02")
	s = strings.ReplaceAll(s, "&", "\x00")
	s = strings.ReplaceAll(s, "\x01", "&>")
	s = strings.ReplaceAll(s, "\x02", ">&")
	s = strings.ReplaceAll(s, "|", "\x00")
	s = strings.ReplaceAll(s, ";", "\x00")
	s = strings.ReplaceAll(s, "\n", "\x00")
	parts := strings.Split(s, "\x00")
	var out []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}
