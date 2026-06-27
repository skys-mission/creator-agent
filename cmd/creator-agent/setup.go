package main

import (
	"sync"

	"github.com/skys-mission/creator-agent/config"
	"github.com/skys-mission/creator-agent/core"
	"github.com/skys-mission/creator-agent/core/middlewares"
	"github.com/skys-mission/creator-agent/paths"
)

var (
	autoMemoryMu               sync.Mutex
	autoMemoryActive           *middlewares.AutoMemory
	autoMemoryExitRegistered   bool
)

// installAutoMemory creates AutoMemory for the middleware chain. On agent rebuild it closes the
// previous instance immediately so extraction goroutines are not orphaned; exit cleanup is registered once.
func installAutoMemory(provider core.ModelProvider, dir string) *middlewares.AutoMemory {
	am := middlewares.NewAutoMemory(provider, dir)

	autoMemoryMu.Lock()
	if autoMemoryActive != nil {
		autoMemoryActive.Close()
	}
	autoMemoryActive = am
	if !autoMemoryExitRegistered {
		autoMemoryExitRegistered = true
		registerCleanup(closeActiveAutoMemory)
	}
	autoMemoryMu.Unlock()

	return am
}

func closeActiveAutoMemory() {
	autoMemoryMu.Lock()
	defer autoMemoryMu.Unlock()
	if autoMemoryActive != nil {
		autoMemoryActive.Close()
		autoMemoryActive = nil
	}
}

// buildMiddlewares assembles the built-in middleware chain.
//
// Parameters:
//   - skills: when non-nil (user enabled skills) it is added to the chain; the caller also uses it to construct SkillTool.
//   - toolInfos: tool capability declarations (used by Permission to classify ReadOnly/risk).
//   - resolver: write-operation approval resolver (REPL interactive / headless deny).
//   - modeCtl: live permission-mode controller (default/trust/auto/readonly). Shared with the TUI so
//     a /mode switch takes effect on the next tool call with no agent rebuild. Nil = legacy DefaultPolicy path.
//
// Order (outer→inner, agent loop wraps in reverse so the first is outermost):
//  1. AgentsMd: inject project memory (lowest cost, highest benefit)
//  2. Skills: inject skill frontmatter summaries (when user enabled)
//  3. MicroCompact: clear old tool_result
//  4. UserHooks: user hooks (before permission, sees the call first)
//  5. Permission: permission interception (mode-aware risk threshold; bash composite split) — always assembled
//  6. Summarization / Reactive: long conversation compression
//  7. AutoMemory: post-session memory extraction (when user enabled)
func buildMiddlewares(provider core.ModelProvider, cfg *config.Config, skills *middlewares.Skills, toolInfos []core.ToolInfo, resolver middlewares.AskResolver, modeCtl *middlewares.ModeController) []core.Middleware {
	mws := []core.Middleware{
		middlewares.NewAgentsMd(),
	}
	if skills != nil {
		mws = append(mws, skills)
	}
	mws = append(mws, middlewares.NewMicroCompact())
	if hooks := cfg.ToHooks(); hasHooks(hooks) {
		mws = append(mws, middlewares.NewUserHooks(hooks))
	}
	// Permission is always assembled. The mode controller (default/trust/auto/readonly) drives the
	// decision path; DefaultPolicy is the legacy fallback when modeCtl is nil.
	// todo_write is exempt from approval: it is session-level memory state (no file/command side effects), called frequently,
	// and requiring approval every time would be annoying. Merges user-configured allow + built-in exempt tools.
	allow := append([]string{"todo_write"}, cfg.Permissions.Allow...)
	mws = append(mws, middlewares.NewPermission(middlewares.PermissionConfig{
		Allow:         allow,
		Deny:          cfg.Permissions.Deny,
		Resolve:       resolver,
		DefaultPolicy: middlewares.PolicyAskWrites,
		ToolInfos:     toolInfos,
		Mode:          modeCtl,
	}))
	mws = append(mws,
		middlewares.NewSummarization(provider),
		middlewares.NewReactive(provider),
	)
	if cfg.Memory.Enabled {
		dir := cfg.Memory.Dir
		if dir == "" {
			if d, err := paths.MemoryDir(); err == nil {
				dir = d
			}
		}
		if dir != "" {
			mws = append(mws, installAutoMemory(provider, dir))
		}
	}
	return mws
}

// hasHooks reports whether any user hook configuration is present.
func hasHooks(h middlewares.HooksConfig) bool {
	return len(h.PreToolUse) > 0 || len(h.PostToolUse) > 0 || len(h.Stop) > 0
}
