package middlewares

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	pathpkg "path"
	"strings"

	"github.com/skys-mission/creator-agent/core"
)

// Effect is a permission decision outcome.
type Effect int

const (
	EffectAllow Effect = iota
	EffectAsk
	EffectDeny
)

// Decision is a permission decision.
type Decision struct {
	Effect Effect
	Reason string
}

// AskResolver handles EffectAsk decisions (scenarios requiring user confirmation).
//
// Returns true=allow, false=deny. ctx is used to return false early on interruption
// (e.g., Ctrl+C), avoiding blocking the approval indefinitely.
// Headless mode usually returns false (default deny); REPL mode prompts the user.
// The resolver decides whether to cache the decision.
type AskResolver func(ctx context.Context, toolName, input string) (allow bool)

// denyAll is the default Ask resolver: rejects all asks in non-interactive scenarios.
func denyAll(context.Context, string, string) bool { return false }

// Default policy constants.
const (
	// PolicyAskWrites: read-only tools (ReadOnly=true) are allowed by default; write tools default to Ask (most common default).
	PolicyAskWrites = "ask_writes"
	// PolicyAskAll: all tools Ask when no rule matches (strictest).
	PolicyAskAll = "ask_all"
	// PolicyAllowAll: all tools allowed when no rule matches (disables the permission layer; not recommended).
	PolicyAllowAll = "allow_all"
)

// PermissionConfig configures permission rules and ask behavior.
type PermissionConfig struct {
	Allow         []string        // allow rules ("tool:spec" or "tool"), spec is a glob
	Deny          []string        // deny rules (higher priority than allow)
	Resolve       AskResolver     // resolver for EffectAsk; nil means deny all
	DefaultPolicy string          // default policy when no rule matches (ask_writes/ask_all/allow_all); empty = ask_writes
	ToolInfos     []core.ToolInfo // tool capability declarations (used to check ReadOnly); required for ask_writes policy

	// Mode enables the mode-based decision path (default/trust/auto/readonly). When nil the middleware
	// falls back to DefaultPolicy (legacy behavior); when set, DefaultPolicy is ignored. The controller
	// is read on every check, so a runtime Set takes effect on the next invocation with no agent rebuild.
	Mode *ModeController
	// WorkspaceRoot is the absolute root used to classify write/edit paths as inside (normal) or
	// outside (risky) the workspace. Empty = resolved to the process cwd in NewPermission.
	WorkspaceRoot string
}

// PermissionMiddleware wires core.Permission rules into the agent loop.
//
// When a ModeController is attached (cfg.Mode != nil), each invocation is decided by priority:
//  1. Any Deny rule matches → deny
//  2. readonly mode + write/modify op → deny (hard block; overrides allow — the user's explicit
//     "block all writes" intent supersedes any standing allowlist)
//  3. Catastrophic op → deny (e.g. rm -rf /; also denied at the bash entry)
//  4. Any Allow rule matches → allow (whitelist lift)
//  5. Mode threshold over Risk (default: reads allow, writes ask; trust: routine local ops allow,
//     risky ask; auto: all allow; readonly: writes already blocked above)
//
// When cfg.Mode is nil the legacy path is used: deny → allow → DefaultPolicy (ask_writes/ask_all/allow_all).
//
// Bash compound command splitting: bash tool commands are split by &&/||/|/;,
// and each subcommand is matched independently. Any deny → overall deny
// (prevents `ls && rm -rf /` from bypassing a `bash:ls` rule).
//
// Note: permission rules are application-level best-effort filtering, not OS-level isolation.
// They cannot replace a sandbox against deliberate bypasses (e.g., command substitution `$(rm -rf /)`,
// quoting, eval, etc.). Strong isolation requires enabling a sandbox.
type PermissionMiddleware struct {
	core.BaseMiddleware

	cfg          PermissionConfig
	resolver     AskResolver
	toolReadOnly map[string]bool // toolName → ReadOnly (looked up from capability)

	mode          *ModeController // live permission mode (nil = legacy DefaultPolicy path)
	workspaceRoot string          // absolute root for write-risk classification (inside vs outside)
}

var _ core.Middleware = (*PermissionMiddleware)(nil)

// NewPermission creates a permission middleware.
func NewPermission(cfg PermissionConfig) *PermissionMiddleware {
	r := cfg.Resolve
	if r == nil {
		r = denyAll
	}
	ro := make(map[string]bool, len(cfg.ToolInfos))
	for _, ti := range cfg.ToolInfos {
		ro[ti.Name] = ti.ReadOnly
	}
	policy := cfg.DefaultPolicy
	if policy == "" {
		policy = PolicyAskWrites
	}
	ws := cfg.WorkspaceRoot
	if ws == "" {
		if wd, err := os.Getwd(); err == nil {
			ws = wd
		}
	}
	return &PermissionMiddleware{
		cfg: PermissionConfig{
			Allow: cfg.Allow, Deny: cfg.Deny, Resolve: r,
			DefaultPolicy: policy, ToolInfos: cfg.ToolInfos,
			Mode: cfg.Mode, WorkspaceRoot: ws,
		},
		resolver:      r,
		toolReadOnly:  ro,
		mode:          cfg.Mode,
		workspaceRoot: ws,
	}
}

// BeforeModel injects the active-permission-mode block into the system prompt (idempotent; refreshed
// each turn so a /mode switch is reflected on the next model call). No-op in the legacy path.
func (p *PermissionMiddleware) BeforeModel(_ context.Context, st *core.RunState) error {
	if p.mode == nil {
		return nil
	}
	block := buildModeBlock(p.mode.Get())
	st.Messages = rewriteSystemBlock(st.Messages, permissionModeStart, permissionModeEnd, block)
	return nil
}

// WrapTool performs permission checks for each tool invocation.
func (p *PermissionMiddleware) WrapTool(name string, next core.ToolFunc) core.ToolFunc {
	return func(ctx context.Context, input json.RawMessage) (core.ToolResult, error) {
		dec, _ := p.check(name, input)
		switch dec.Effect {
		case EffectAllow:
			return next(ctx, input)
		case EffectDeny:
			return core.ToolResult{Content: "permission denied: " + dec.Reason, IsError: true}, nil
		case EffectAsk:
			inputStr := string(input)
			if p.resolver(ctx, name, inputStr) {
				return next(ctx, input)
			}
			return core.ToolResult{Content: "permission denied (not approved): " + dec.Reason, IsError: true}, nil
		default:
			// Unknown Effect: fail-closed, deny and log. New effects added in the future will not be accidentally allowed.
			return core.ToolResult{Content: "permission denied: unknown effect", IsError: true}, nil
		}
	}
}

// check returns the permission decision for a single invocation.
func (p *PermissionMiddleware) check(toolName string, input json.RawMessage) (Decision, error) {
	if toolName == "bash" {
		cmd := extractBashCommand(input)
		if cmd != "" {
			if isDangerous(cmd) {
				return Decision{Effect: EffectDeny, Reason: "dangerous command (built-in deny): " + cmd}, nil
			}
			subs := splitBashCommand(cmd)
			if len(subs) > 0 {
				return p.aggregate(toolName, subs), nil
			}
		}
	}
	// write/edit: extract the path for glob matching (so rules like "write:/tmp/*" match the path, not the raw JSON).
	if toolName == "write" || toolName == "edit" {
		if path := extractFilePath(input); path != "" {
			return p.matchOne(toolName, path), nil
		}
	}
	return p.matchOne(toolName, string(input)), nil
}

// aggregate takes the strictest decision across multiple bash subcommands (priority: deny > ask > allow).
// Any subcommand deny → overall deny; any ask (and no deny) → ask; all allow → allow.
// This prevents `ls && rm -rf /` from being allowed when only `bash:ls` is allowed (rm goes to default policy → Ask).
func (p *PermissionMiddleware) aggregate(toolName string, subs []string) Decision {
	var askReason string
	for _, sub := range subs {
		d := p.matchOne(toolName, sub)
		switch d.Effect {
		case EffectDeny:
			return d
		case EffectAsk:
			if askReason == "" {
				askReason = d.Reason
			}
		}
	}
	if askReason != "" {
		return Decision{Effect: EffectAsk, Reason: "compound has unapproved subcommand: " + askReason}
	}
	return Decision{Effect: EffectAllow, Reason: "all subcommands allowed"}
}

// matchOne matches a single spec against the rules.
func (p *PermissionMiddleware) matchOne(toolName, spec string) Decision {
	// Deny rules take priority in every mode (user's explicit "never" overrides everything).
	for _, rule := range p.cfg.Deny {
		if ruleMatches(rule, toolName, spec) {
			return Decision{Effect: EffectDeny, Reason: fmt.Sprintf("%s:%s matched deny rule %q", toolName, spec, rule)}
		}
	}
	if p.mode != nil {
		return p.decideByMode(toolName, spec)
	}
	// Legacy path (Mode not attached): allow rules, then DefaultPolicy.
	for _, rule := range p.cfg.Allow {
		if ruleMatches(rule, toolName, spec) {
			return Decision{Effect: EffectAllow, Reason: "matched allow rule " + rule}
		}
	}
	// No rule matched → apply DefaultPolicy
	switch p.cfg.DefaultPolicy {
	case PolicyAllowAll:
		return Decision{Effect: EffectAllow, Reason: "default allow_all"}
	case PolicyAskAll:
		return Decision{Effect: EffectAsk, Reason: "default ask_all"}
	default: // PolicyAskWrites (also handles empty value)
		if p.toolReadOnly[toolName] {
			return Decision{Effect: EffectAllow, Reason: "default ask_writes: readonly tool"}
		}
		return Decision{Effect: EffectAsk, Reason: "default ask_writes: write tool"}
	}
}

// decideByMode resolves an invocation under the active mode (cfg.Mode attached). Priority within the
// mode path: readonly write-block → catastrophic → allow rules → mode threshold. See PermissionMiddleware doc.
func (p *PermissionMiddleware) decideByMode(toolName, spec string) Decision {
	mode := p.mode.Get()
	risk := classifyRisk(toolName, spec, p.toolReadOnly[toolName], p.workspaceRoot)

	// readonly hard-block: a write/modify op is denied even if an allow rule would match — the user's
	// explicit "block all writes" intent overrides any standing allowlist.
	if mode == ModeReadonly && risk > RiskSafe {
		return Decision{Effect: EffectDeny, Reason: "readonly mode blocks write/modify operations"}
	}
	// Catastrophic ops are always denied regardless of mode.
	if risk == RiskCatastrophic {
		return Decision{Effect: EffectDeny, Reason: "catastrophic operation blocked"}
	}
	// Allow rules lift a would-be ask/deny (whitelist).
	for _, rule := range p.cfg.Allow {
		if ruleMatches(rule, toolName, spec) {
			return Decision{Effect: EffectAllow, Reason: "matched allow rule " + rule}
		}
	}
	effect, reason := modeDecision(mode, risk)
	return Decision{Effect: effect, Reason: reason}
}

// ruleMatches determines whether a "tool:spec" rule matches.
//
// Rule format: "tool:glob" or "tool" (no spec = matches any invocation of that tool).
// spec uses filepath.Match-style glob with * as wildcard. Tool name is exact match.
func ruleMatches(rule, toolName, spec string) bool {
	ruleTool, ruleSpec, found := strings.Cut(rule, ":")
	if !found {
		// No colon: match by tool name only, spec is not checked
		return ruleTool == toolName || rule == "*"
	}
	if ruleTool != toolName && ruleTool != "*" {
		return false
	}
	if ruleSpec == "*" || ruleSpec == "" {
		return true
	}
	// Use simple glob where * matches any characters including / (unlike filepath.Match, whose * does not cross path separators).
	// This matters for bash command matching where "/" is part of the command.
	return globMatch(ruleSpec, spec)
}

// globMatch is a simple glob: * matches any characters (including /), no other special syntax.
// Sufficient for rules like "git *" / "rm -rf *"; complex patterns can be added later.
func globMatch(pattern, s string) bool {
	pi, si := 0, 0
	star := -1
	starS := 0
	for si < len(s) {
		if pi < len(pattern) && pattern[pi] == '*' {
			star = pi
			starS = si
			pi++
		} else if pi < len(pattern) && pattern[pi] == s[si] {
			pi++
			si++
		} else if star >= 0 {
			pi = star + 1
			starS++
			si = starS
		} else {
			return false
		}
	}
	for pi < len(pattern) && pattern[pi] == '*' {
		pi++
	}
	return pi == len(pattern)
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

// extractFilePath extracts the path field from write/edit tool input JSON, for glob-based permission
// matching (so rules like "write:/tmp/*" match the actual path, not the raw JSON wrapper).
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
// Splits by shell control operators: &&  ||  |  ;  and newlines.
//
// This is a best-effort heuristic (does not invoke a real shell parser):
//   - Covers common compound forms (&& / || / | / ;);
//   - Does not recognize operators inside quotes, command substitution $(...) / backticks,
//     subshells (...), heredocs, eval, etc., so deny rules are ineffective against deliberate
//     evasion (e.g., `$(rm -rf /)`).
//   - Strong isolation (OS-level hard boundaries) should rely on builtins.Sandbox;
//     permission rules are only a best-effort auxiliary filter.
//
// Biased toward over-splitting (extra checks are harmless).
func splitBashCommand(cmd string) []string {
	s := cmd
	// Normalize multi-character operators to a single placeholder
	s = strings.ReplaceAll(s, "&&", "\x00")
	s = strings.ReplaceAll(s, "||", "\x00")
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

// ApproveKey generates a session-level authorization ("a") key, refining the allowSet granularity from pure tool name to tool+input:
//   - bash: by primary command (first segment's first token) → "bash:rm", "bash:git". Authorizing bash:mkdir still requires re-approval for bash:rm.
//   - write/edit: by path → "write:/tmp/x". Same path is allowed on repeat; different path requires re-approval.
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

// dangerousPatterns are built-in dangerous command patterns (deny priority, cannot be overridden by "a").
// Best-effort substring matching: cannot guard against $(rm -rf /) evasion — strong isolation requires Sandbox (opt-in).
// Note: rm -rf / (root/home directory) is handled by isRmRfRoot with exact boundary matching and is not listed here.
var dangerousPatterns = []string{
	"rm -rf $home",
	"> /dev/sd",
	"mkfs",
	"dd if=/dev/zero of=/dev/",
	":(){:|:&};:",
	"chmod -r 777 /",
}

// isDangerous checks whether a bash command matches built-in dangerous patterns (best-effort, case-insensitive).
// rm -rf / and rm -rf ~ use exact boundary matching (to avoid false positives on legitimate operations like rm -rf /tmp).
func isDangerous(cmd string) bool {
	lower := strings.ToLower(cmd)
	if isRmRfRoot(lower) {
		return true
	}
	for _, pat := range dangerousPatterns {
		if strings.Contains(lower, pat) {
			return true
		}
	}
	return false
}

// isRmRfRoot checks recursive+force rm against root or the whole home directory.
// It covers common flag spellings/orderings (-rf, -fr, -r -f, --recursive --force) while avoiding
// false positives for subpaths such as /tmp or ~/project.
// Still best-effort: shell expansions, command substitution, aliases and eval require Sandbox.
func isRmRfRoot(s string) bool {
	fields := strings.Fields(s)
	if len(fields) < 2 || fields[0] != "rm" {
		return false
	}
	recursive := false
	force := false
	for _, f := range fields[1:] {
		token := strings.Trim(f, `"'`)
		if token == "--" {
			continue
		}
		if strings.HasPrefix(token, "--") {
			switch token {
			case "--recursive":
				recursive = true
			case "--force":
				force = true
			}
			continue
		}
		if strings.HasPrefix(token, "-") && token != "-" {
			for _, r := range token[1:] {
				switch r {
				case 'r', 'R':
					recursive = true
				case 'f':
					force = true
				}
			}
			continue
		}
		if recursive && force && isRootOrWholeHomeTarget(token) {
			return true
		}
	}
	return false
}

func isRootOrWholeHomeTarget(target string) bool {
	t := strings.TrimSpace(strings.ToLower(target))
	t = strings.Trim(t, `"'`)
	// Cut at the first shell separator: rm's argv terminates here under real shell parsing, and the
	// remainder belongs to a chained command (e.g. "rm -rf /;echo" -> target "/"). Without this,
	// strings.Fields glues the separator onto the path and "/" is not recognized.
	if i := strings.IndexAny(t, ";|&\n\r"); i >= 0 {
		t = strings.TrimSpace(t[:i])
	}
	if t == "" {
		return false
	}
	if !strings.ContainsAny(t, "*?[") && pathpkg.Clean(t) == "/" {
		return true
	}
	switch t {
	case "/*", "~", "~/", "~/*", "$home", "$home/", "$home/*", "${home}", "${home}/", "${home}/*":
		return true
	default:
		return false
	}
}
