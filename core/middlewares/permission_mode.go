package middlewares

import (
	"path/filepath"
	"strings"
	"sync"
)

// Mode is a permission mode: a named trust level governing how much the agent may do without asking.
//
// The four modes form a trust gradient (least to most autonomous): readonly -> default -> trust -> auto.
// Each mode is a threshold over Risk (see modeDecision): below the threshold operations run without
// asking; at/above it they ask; catastrophic operations are always blocked regardless of mode.
type Mode string

const (
	// ModeDefault: read-only tools auto-allowed; every write/modify operation asks for approval.
	// Equivalent to the legacy ask_writes default policy.
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

// Risk classifies the potential impact of a single tool invocation, independent of the active mode.
type Risk int

const (
	// RiskSafe: read-only, no side effects (read/grep/glob/todo/skill/task).
	RiskSafe Risk = iota
	// RiskNormal: reversible local mutations (workspace writes/edits, benign commands).
	RiskNormal
	// RiskRisky: side effects or hard-to-reverse actions (delete, network write, sudo, package install,
	// writes outside the workspace, remote-pushing git ops).
	RiskRisky
	// RiskCatastrophic: disastrous, system-wide damage (rm -rf /, mkfs, dd to a device, fork bomb).
	RiskCatastrophic
)

// ModeController holds the live permission mode, safe for concurrent read (every tool check) and
// write (TUI /mode switch). The permission middleware reads it on each invocation; the next call
// after Set reflects the new mode with no agent rebuild.
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

// modeDecision returns the effect for an invocation that matched no rule, under the given mode.
// This is the final fallback after deny rules, the catastrophic guard, and allow rules.
// Pure function of (mode, risk).
func modeDecision(mode Mode, risk Risk) (Effect, string) {
	switch mode {
	case ModeReadonly:
		// Write/modify ops are already hard-blocked in decideByMode; only reads reach here.
		if risk <= RiskSafe {
			return EffectAllow, "readonly mode: read-only tool"
		}
		return EffectDeny, "readonly mode: write/modify operations are blocked"
	case ModeDefault:
		if risk <= RiskSafe {
			return EffectAllow, "default mode: read-only tool"
		}
		return EffectAsk, "default mode: write/modify operation"
	case ModeTrust:
		if risk <= RiskNormal {
			return EffectAllow, "trust mode: routine local operation"
		}
		return EffectAsk, "trust mode: risky operation"
	case ModeAuto:
		return EffectAllow, "auto mode: auto-allowed"
	}
	// Unknown mode: fail-safe toward asking.
	return EffectAsk, "unknown mode: fail-safe ask"
}

// classifyRisk returns the risk of a single invocation. isReadOnly reflects the tool's capability
// declaration; workspaceRoot (absolute) classifies write/edit paths as inside (normal) or outside
// (risky) the workspace.
func classifyRisk(toolName, spec string, isReadOnly bool, workspaceRoot string) Risk {
	if isReadOnly {
		return RiskSafe
	}
	switch toolName {
	case "bash":
		return bashRisk(spec)
	case "write", "edit":
		return writeRisk(spec, workspaceRoot)
	default:
		// Unknown non-readonly tool: treat as a normal write (conservative, not catastrophic).
		return RiskNormal
	}
}

// riskyBashFirstTokens are command verbs whose ordinary use carries side effects warranting an ask
// even in trust mode. Verbs with mixed risk depending on subcommand (git/npm/pip/cargo/docker/brew/
// kubectl/go) are refined in bashRisk and deliberately omitted here.
var riskyBashFirstTokens = map[string]bool{
	"rm": true, "rmdir": true, "unlink": true,
	"sudo": true, "su": true, "doas": true,
	"curl": true, "wget": true, "ssh": true, "scp": true, "rsync": true, "ftp": true,
	"kill": true, "pkill": true, "killall": true,
	"chmod": true, "chown": true, "chattr": true, "setfacl": true,
	"mkfs": true, "fdisk": true, "parted": true, "dd": true, "shred": true,
	"systemctl": true, "service": true, "launchctl": true, "rcctl": true,
	"apt": true, "apt-get": true, "yum": true, "dnf": true, "pacman": true, "snap": true,
	"crontab": true, "at": true,
	"tee":     true,
	"useradd": true, "usermod": true, "userdel": true, "passwd": true,
	"mount": true, "umount": true,
	"truncate": true,
}

// bashRisk classifies a single bash subcommand (already split from compound forms).
func bashRisk(subcmd string) Risk {
	s := strings.TrimSpace(subcmd)
	if s == "" {
		return RiskNormal
	}
	// Catastrophic built-in patterns (rm -rf /, mkfs, dd to device, fork bomb) — also denied at the
	// check entry, but classify defensively so modeDecision can never auto-allow them.
	if isDangerous(s) {
		return RiskCatastrophic
	}
	fields := strings.Fields(s)
	if len(fields) == 0 {
		return RiskNormal
	}
	first := strings.ToLower(fields[0])
	switch first {
	case "git":
		return gitRisk(fields[1:])
	case "npm", "yarn", "pnpm":
		if len(fields) > 1 && pkgInstallSub(fields[1]) {
			return RiskRisky
		}
		return RiskNormal // test/run/start/...
	case "npx":
		return RiskNormal // runs a package in an ephemeral store (no global install)
	case "pip", "pip3", "pipx":
		if len(fields) > 1 && (fields[1] == "install" || fields[1] == "uninstall") {
			return RiskRisky
		}
		return RiskNormal // list/show/...
	case "cargo":
		if len(fields) > 1 && (fields[1] == "install" || fields[1] == "uninstall") {
			return RiskRisky
		}
		return RiskNormal // build/test/run
	case "go":
		if len(fields) > 1 && fields[1] == "install" {
			return RiskRisky
		}
		return RiskNormal // build/test/vet/mod
	case "docker", "podman":
		if len(fields) > 1 && dockerReadonlySub(fields[1]) {
			return RiskNormal // ps/logs/images/...
		}
		return RiskRisky // run/exec/rm/push/build
	case "brew":
		if len(fields) > 1 && brewMutatingSub(fields[1]) {
			return RiskRisky
		}
		return RiskNormal // list/info
	case "kubectl":
		return RiskRisky // cluster-mutating by default
	}
	if riskyBashFirstTokens[first] {
		return RiskRisky
	}
	return RiskNormal
}

func pkgInstallSub(sub string) bool {
	switch sub {
	case "install", "i", "add", "remove", "uninstall", "rm", "update", "upgrade", "ci":
		return true
	}
	return false
}

func dockerReadonlySub(sub string) bool {
	switch sub {
	case "ps", "logs", "images", "version", "info", "stats", "inspect", "top", "diff", "history", "port":
		return true
	}
	return false
}

func brewMutatingSub(sub string) bool {
	switch sub {
	case "install", "reinstall", "uninstall", "remove", "upgrade", "tap", "untap", "link", "unlink":
		return true
	}
	return false
}

// gitRisk classifies a git invocation by subcommand. Remote or history-rewriting ops are risky;
// ordinary local ops (status/add/commit/checkout/diff/log/pull/fetch/branch) are normal.
func gitRisk(args []string) Risk {
	if len(args) == 0 {
		return RiskNormal
	}
	switch args[0] {
	case "push", "reset", "clean", "rebase", "filter-branch", "amend":
		return RiskRisky
	default:
		return RiskNormal
	}
}

// writeRisk classifies a write/edit by whether the target is inside the workspace.
// Empty workspaceRoot (unknown) degrades to normal; a path that cannot be resolved is risky.
func writeRisk(path, workspaceRoot string) Risk {
	if strings.TrimSpace(path) == "" {
		return RiskNormal
	}
	if workspaceRoot == "" {
		return RiskNormal
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return RiskRisky
	}
	rel, err := filepath.Rel(workspaceRoot, abs)
	if err != nil {
		return RiskRisky
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return RiskRisky
	}
	return RiskNormal
}

// permissionModeStart/End delimit the injected mode block in the system prompt (idempotent rewrite).
const (
	permissionModeStart = "<permission-mode>\n"
	permissionModeEnd   = "</permission-mode>\n"
)

// buildModeBlock renders the system-prompt block describing the active mode and the model's mode rules.
// Kept short to limit prompt-prefix cache impact; stable while the mode is unchanged (so the cache
// survives across turns) and only changes on a /mode switch.
func buildModeBlock(mode Mode) string {
	var b strings.Builder
	b.WriteString(permissionModeStart)
	b.WriteString("Permission mode: ")
	b.WriteString(string(mode))
	b.WriteString(modeBlurb(mode))
	b.WriteString("\nYou cannot switch the mode yourself; only the user can (via /mode).")
	switch mode {
	case ModeAuto:
		b.WriteString(" Do NOT suggest or ask about switching modes; proceed without prompts.")
	case ModeReadonly:
		b.WriteString(" Write/edit/bash-modify tools are blocked — do not call them; describe what you would do, or ask the user to switch modes.")
	default:
		b.WriteString(" If switching modes would help the task, you may suggest it and wait for the user to do it.")
	}
	b.WriteString("\n")
	b.WriteString(permissionModeEnd)
	return b.String()
}

func modeBlurb(mode Mode) string {
	switch mode {
	case ModeDefault:
		return " — every write/modify operation asks for approval."
	case ModeTrust:
		return " — routine local edits and benign commands run without asking; risky operations (delete, network write, sudo, package install, outside-workspace writes) still ask."
	case ModeAuto:
		return " — all operations run without asking (catastrophic ones such as rm -rf / are still blocked)."
	case ModeReadonly:
		return " — all write/modify operations are blocked."
	}
	return ""
}
