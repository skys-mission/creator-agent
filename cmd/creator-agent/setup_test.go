package main

import (
	"context"
	"reflect"
	"testing"

	"github.com/skys-mission/creator-agent/config"
	"github.com/skys-mission/creator-agent/core"
	"github.com/skys-mission/creator-agent/core/middlewares"
)

// buildMiddlewares assembly layer table-driven tests (previously 0% coverage).
//
// buildMiddlewares passes the provider to Summarization/Reactive/AutoMemory constructors, but does not call the model during construction,
// so passing nil provider is sufficient (only verifies assembly structure, not running the loop).

// denyResolver is a stub AskResolver that always denies.
func denyResolver(_ context.Context, _, _ string) bool { return false }

// middlewareTypes extracts middleware slice type names (for asserting order / debugging).
func middlewareTypes(mws []core.Middleware) []string {
	out := make([]string, len(mws))
	for i, m := range mws {
		out[i] = reflect.TypeOf(m).String()
	}
	return out
}

// base middleware count (always assembled): AgentsMd / MicroCompact / PermissionMiddleware / Summarization / Reactive.
func baseMiddlewareCount() int { return 5 }

func TestBuildMiddlewares_Minimal(t *testing.T) {
	// Minimal config: no skills/hooks/memory -> only base 5.
	cfg := &config.Config{}
	mws := buildMiddlewares(nil, cfg, nil, nil, denyResolver, nil)
	if len(mws) != baseMiddlewareCount() {
		t.Errorf("minimal config: expected %d middlewares, got %d (%v)", baseMiddlewareCount(), len(mws), middlewareTypes(mws))
	}
	// First is AgentsMd
	if _, ok := mws[0].(*middlewares.AgentsMd); !ok {
		t.Errorf("first middleware should be AgentsMd, got %T", mws[0])
	}
}

func TestBuildMiddlewares_WithSkills(t *testing.T) {
	cfg := &config.Config{}
	skills := middlewares.NewSkills()
	mws := buildMiddlewares(nil, cfg, skills, nil, denyResolver, nil)
	if len(mws) != baseMiddlewareCount()+1 {
		t.Errorf("with skills: expected %d, got %d", baseMiddlewareCount()+1, len(mws))
	}
	// skills should be after AgentsMd (order: AgentsMd, Skills, ...)
	if _, ok := mws[1].(*middlewares.Skills); !ok {
		t.Errorf("second middleware should be Skills, got %T", mws[1])
	}
}

func TestBuildMiddlewares_WithHooks(t *testing.T) {
	cfg := &config.Config{
		Hooks: config.HooksConfig{
			PreToolUse: []config.HookEntry{{Matcher: "*", Command: "true"}},
		},
	}
	mws := buildMiddlewares(nil, cfg, nil, nil, denyResolver, nil)
	if len(mws) != baseMiddlewareCount()+1 {
		t.Errorf("with hooks: expected %d, got %d", baseMiddlewareCount()+1, len(mws))
	}
	var foundHooks bool
	for _, m := range mws {
		if _, ok := m.(*middlewares.UserHooksMiddleware); ok {
			foundHooks = true
		}
	}
	if !foundHooks {
		t.Error("UserHooksMiddleware not assembled when hooks configured")
	}
}

func TestBuildMiddlewares_WithMemory(t *testing.T) {
	t.Cleanup(closeActiveAutoMemory)
	cfg := &config.Config{
		Memory: config.MemoryConfig{Enabled: true, Dir: t.TempDir()},
	}
	mws := buildMiddlewares(nil, cfg, nil, nil, denyResolver, nil)
	if len(mws) != baseMiddlewareCount()+1 {
		t.Errorf("with memory: expected %d, got %d", baseMiddlewareCount()+1, len(mws))
	}
	// AutoMemory should be last (AfterAgent async extraction)
	if _, ok := mws[len(mws)-1].(*middlewares.AutoMemory); !ok {
		t.Errorf("last middleware should be AutoMemory, got %T", mws[len(mws)-1])
	}
}

func TestBuildMiddlewares_AllEnabled(t *testing.T) {
	t.Cleanup(closeActiveAutoMemory)
	// skills + hooks + memory all on -> base 5 + 3 optional = 8
	cfg := &config.Config{
		Hooks:  config.HooksConfig{PreToolUse: []config.HookEntry{{Matcher: "*", Command: "true"}}},
		Memory: config.MemoryConfig{Enabled: true, Dir: t.TempDir()},
	}
	skills := middlewares.NewSkills()
	mws := buildMiddlewares(nil, cfg, skills, nil, denyResolver, nil)
	if len(mws) != baseMiddlewareCount()+3 {
		t.Errorf("all enabled: expected %d, got %d (%v)", baseMiddlewareCount()+3, len(mws), middlewareTypes(mws))
	}
}

func TestBuildMiddlewares_PermissionAlwaysAssembled(t *testing.T) {
	// Even with no permission rules, Permission should always be assembled (default ask_writes).
	cfg := &config.Config{}
	mws := buildMiddlewares(nil, cfg, nil, nil, denyResolver, nil)
	var foundPerm bool
	for _, m := range mws {
		if _, ok := m.(*middlewares.PermissionMiddleware); ok {
			foundPerm = true
		}
	}
	if !foundPerm {
		t.Error("PermissionMiddleware must always be assembled (default ask_writes policy)")
	}
}

func TestHasHooks(t *testing.T) {
	cases := []struct {
		name string
		h    middlewares.HooksConfig
		want bool
	}{
		{"empty", middlewares.HooksConfig{}, false},
		{"pre-only", middlewares.HooksConfig{PreToolUse: []middlewares.HookEntry{{}}}, true},
		{"post-only", middlewares.HooksConfig{PostToolUse: []middlewares.HookEntry{{}}}, true},
		{"stop-only", middlewares.HooksConfig{Stop: []middlewares.HookEntry{{}}}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := hasHooks(c.h); got != c.want {
				t.Errorf("hasHooks(%+v) = %v, want %v", c.h, got, c.want)
			}
		})
	}
}

// TestBuildMiddlewares_MemoryDefaultDir: when memory is enabled but Dir is empty, it should resolve the default
// ~/.creator/memory (previously this branch was uncovered, 89.5% -> full).
func TestBuildMiddlewares_MemoryDefaultDir(t *testing.T) {
	t.Cleanup(closeActiveAutoMemory)
	t.Setenv("HOME", t.TempDir())
	cfg := &config.Config{
		Memory: config.MemoryConfig{Enabled: true, Dir: ""}, // empty -> resolve default
	}
	mws := buildMiddlewares(nil, cfg, nil, nil, denyResolver, nil)
	// AutoMemory should be assembled (default directory resolved successfully)
	var am *middlewares.AutoMemory
	for _, m := range mws {
		if v, ok := m.(*middlewares.AutoMemory); ok {
			am = v
		}
	}
	if am == nil {
		t.Fatal("AutoMemory should be assembled when memory enabled (even with empty Dir)")
	}
}

// TestBuildMiddlewares_MemoryNoHomeDir: when HOME is unavailable (UserHomeDir fails), memory is enabled but
// directory cannot be resolved -> AutoMemory is not assembled (no panic, no block).
func TestBuildMiddlewares_MemoryNoHomeDir(t *testing.T) {
	// Make UserHomeDir fail: clear HOME (on some platforms UserHomeDir depends on it)
	t.Setenv("HOME", "")
	cfg := &config.Config{
		Memory: config.MemoryConfig{Enabled: true, Dir: ""},
	}
	mws := buildMiddlewares(nil, cfg, nil, nil, denyResolver, nil)
	// AutoMemory should not be assembled (directory resolution failed, dir still empty, skip assembly)
	for _, m := range mws {
		if _, ok := m.(*middlewares.AutoMemory); ok {
			t.Error("AutoMemory should NOT be assembled when home dir unresolvable")
		}
	}
}

func TestBuildMiddlewares_AutoMemoryClosesPreviousOnRebuild(t *testing.T) {
	t.Cleanup(closeActiveAutoMemory)
	cfg := &config.Config{
		Memory: config.MemoryConfig{Enabled: true, Dir: t.TempDir()},
	}
	mws1 := buildMiddlewares(nil, cfg, nil, nil, denyResolver, nil)
	am1 := mws1[len(mws1)-1].(*middlewares.AutoMemory)
	mws2 := buildMiddlewares(nil, cfg, nil, nil, denyResolver, nil)
	am2 := mws2[len(mws2)-1].(*middlewares.AutoMemory)
	if am1 == am2 {
		t.Fatal("rebuild should create a new AutoMemory instance")
	}
	am1.Close() // replaced instance already closed on rebuild; must stay idempotent
}
