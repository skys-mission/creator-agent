package main

// agents.go defines the built-in agents (named bundles of system prompt + tool whitelist) and the
// runtime rebuild factories backing the /agents and /variants pickers.
//
// An "agent" (opencode-parity) selects a persona: its system prompt and the subset of tools it may
// use. A "variant" is orthogonal: a named preset of request overrides (headers/body/generation
// params) for the current provider's model, applied at LLM-call time. Switching either rebuilds the
// core.Agent so the next turn uses the new configuration; conversation history is preserved by the
// shared SessionStore.

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/skys-mission/creator-agent/cmd/creator-agent/tui"
	"github.com/skys-mission/creator-agent/config"
	"github.com/skys-mission/creator-agent/core"
	"github.com/skys-mission/creator-agent/core/adapters/anthropic"
	openaichat "github.com/skys-mission/creator-agent/core/adapters/openai-chat"
	openairesponses "github.com/skys-mission/creator-agent/core/adapters/openai-responses"
	"github.com/skys-mission/creator-agent/core/adapters/shared"
	"github.com/skys-mission/creator-agent/core/builtins"
	"github.com/skys-mission/creator-agent/core/middlewares"
)

// agentSpec is one selectable agent: a name, short description, and the system prompt + tool
// whitelist applied when this agent is active. An empty whitelist means "all tools"; otherwise only
// tools whose Info().Name appears in the whitelist are retained.
type agentSpec struct {
	Name         string
	Description  string
	SystemPrompt string
	// ToolWhitelist filters the full tool set. Empty = all tools (the build agent).
	// A nil/empty slice is distinct from a sentinel: read-only agents enumerate their tools.
	ToolWhitelist []string
	// ReadOnly flags read-only agents (e.g. plan) so the TUI can badge them.
	ReadOnly bool
}

// planSystemPrompt is the read-only (plan) agent's system prompt. Derived from the default build
// prompt with the editing/acting rules replaced: plan agents never mutate files or run write tools.
const planSystemPrompt = `You are creator-agent in PLAN mode (read-only).

You help the user understand, investigate, and plan software tasks in the current project. You may
read files, search code, and explore — but you DO NOT edit, write, or run commands that change state.
Produce a clear, concrete plan the user can approve before any execution.

Available tools (read-only):
- read: read a file's content
- grep: search file contents (regex)
- glob: find files by name pattern (supports **)
- task: delegate an exploration sub-task to a read-only sub-agent (keeps the main context clean)
- skill: load a named skill's full body when its frontmatter indicates it fits the task
- todo_write: manage the task progress list (planning aid)

Rules:
- Be concise; no preamble before answering.
- Read before concluding; never guess file contents.
- When asked to change something, respond with a step-by-step plan and the exact edits/commands that
  WOULD be made — but do not execute them. The user will switch to the build agent to act.
- Never fabricate file paths, content, or commands.

todo_write usage in plan mode: use status "planned" for proposed steps (do NOT mark "in_progress" or
"completed" — those belong to the build/execute agent). Pass the FULL list each time.`

// builtinAgents returns the agent specs surfaced in the /agents picker. Order is display order;
// "build" is first so it is the default selection (matches opencode's build-agent-first convention).
func builtinAgents() []agentSpec {
	return []agentSpec{
		{
			Name:         "build",
			Description:  "Default agent. Executes tools based on configured permissions.",
			SystemPrompt: systemPrompt,
			// Empty whitelist = all tools.
			ReadOnly: false,
		},
		{
			Name:         "plan",
			Description:  "Plan mode. Read-only: investigate and propose steps, never edit.",
			SystemPrompt: planSystemPrompt,
			// Read-only whitelist: excludes write/edit/bash/mcp write paths. task + skill retained
			// (task is read-only by construction; skill is informational).
			ToolWhitelist: []string{"read", "grep", "glob", "task", "skill", "todo_write"},
			ReadOnly:      true,
		},
	}
}

// filterToolsByAgent returns the subset of tools allowed for the named agent. An unknown agent name
// or an agent with an empty whitelist yields the full tool set unchanged.
func filterToolsByAgent(allTools []core.Tool, agentName string, specs []agentSpec) []core.Tool {
	var spec *agentSpec
	for i := range specs {
		if specs[i].Name == agentName {
			spec = &specs[i]
			break
		}
	}
	if spec == nil || len(spec.ToolWhitelist) == 0 {
		return allTools
	}
	allow := make(map[string]bool, len(spec.ToolWhitelist))
	for _, n := range spec.ToolWhitelist {
		allow[n] = true
	}
	out := make([]core.Tool, 0, len(allTools))
	for _, t := range allTools {
		if allow[t.Info().Name] {
			out = append(out, t)
		}
	}
	return out
}

// systemPromptForAgent resolves the system prompt for the named agent (falls back to the default
// build prompt when the name is unknown).
func systemPromptForAgent(agentName string, specs []agentSpec) string {
	for i := range specs {
		if specs[i].Name == agentName {
			return specs[i].SystemPrompt
		}
	}
	return systemPrompt
}

// resolveVariantConfig builds a shared.ProviderConfig for the named variant of the given profile. A
// variantName of "" or "default" yields the profile's plain config (no overrides). An unknown
// variant name yields an error.
func resolveVariantConfig(prof config.Profile, reqTimeout time.Duration, variantName string) (shared.ProviderConfig, error) {
	vName := normalizeVariantName(variantName)
	cfg := shared.ProviderConfig{
		BaseURL:        prof.BaseURL,
		APIKey:         prof.APIKey,
		Model:          prof.Model,
		RequestTimeout: reqTimeout,
	}
	if vName == "" || vName == "default" {
		return cfg, nil
	}
	v, ok := prof.Variants[vName]
	if !ok {
		known := strings.Join(prof.VariantNames(), ", ")
		return shared.ProviderConfig{}, fmt.Errorf("variant %q not found on profile (have: %s)", variantName, known)
	}
	cfg.ExtraHeaders = v.Headers
	cfg.ExtraBody = v.Body
	if v.Temperature != nil {
		tv := *v.Temperature
		cfg.Temperature = &tv
	}
	if v.TopP != nil {
		tpv := *v.TopP
		cfg.TopP = &tpv
	}
	if v.MaxTokens != nil {
		mt := int64(*v.MaxTokens)
		cfg.MaxTokens = &mt
	}
	return cfg, nil
}

// normalizeVariantName canonicalizes a variant name for comparison (lowercase, trimmed). The empty
// string and "default" both mean "no variant" and are treated equivalently by callers.
func normalizeVariantName(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}

// buildProvider resolves the variant config and constructs the provider for the profile's type.
// When the named variant is not found on the profile, it falls back to the base config with a
// warning instead of failing. This is the single construction path used by initial startup and
// the /model and /variants runtime rebuilds.
func buildProvider(ctx context.Context, prof config.Profile, reqTimeout time.Duration, variant string) (core.ModelProvider, error) {
	pc, varErr := resolveVariantConfig(prof, reqTimeout, variant)
	if varErr != nil {
		fmt.Fprintf(os.Stderr, "warn: variant %q not found, using base config: %v\n", variant, varErr)
		pc, _ = resolveVariantConfig(prof, reqTimeout, "")
	}
	switch prof.NormalizedType() {
	case "", "openai":
		return openaichat.NewProvider(ctx, pc)
	case "openai-responses":
		return openairesponses.NewProvider(ctx, pc)
	case "anthropic":
		return anthropic.NewProvider(ctx, pc)
	default:
		return nil, fmt.Errorf("unknown provider type %q", prof.Type)
	}
}

// variantSummary returns a one-line description of a variant for TUI display (the override keys).
func variantSummary(v config.Variant) string {
	var parts []string
	if len(v.Headers) > 0 {
		parts = append(parts, fmt.Sprintf("%d header", len(v.Headers)))
	}
	if len(v.Body) > 0 {
		parts = append(parts, fmt.Sprintf("%d body", len(v.Body)))
	}
	if v.Temperature != nil {
		parts = append(parts, fmt.Sprintf("temp=%.1f", *v.Temperature))
	}
	if v.TopP != nil {
		parts = append(parts, fmt.Sprintf("top_p=%.2f", *v.TopP))
	}
	if v.MaxTokens != nil {
		parts = append(parts, fmt.Sprintf("max_tokens=%d", *v.MaxTokens))
	}
	if len(parts) == 0 {
		return "no overrides"
	}
	return strings.Join(parts, ", ")
}

// rebuildAgentForAgent rebuilds the core.Agent for the named agent: applies the agent's system
// prompt and tool whitelist over the current provider. Used by the /agents picker. Session history
// is preserved by the store.
func rebuildAgentForAgent(
	specs []agentSpec,
	currentProvider core.ModelProvider,
	cfg *config.Config,
	sandbox builtins.Sandbox,
	mcpToolsProvider func() []core.Tool,
	skillsMW *middlewares.Skills,
	sessionStore core.SessionStore,
	resolver middlewares.AskResolver,
	modeCtl *middlewares.ModeController,
	agentName string,
) (core.Agent, error) {
	newTools := filterToolsByAgent(buildAllToolsWithProvider(currentProvider, cfg, sandbox, mcpToolsProvider, skillsMW), agentName, specs)
	newToolInfos := make([]core.ToolInfo, len(newTools))
	for i, t := range newTools {
		newToolInfos[i] = t.Info()
	}
	prompt := systemPromptForAgent(agentName, specs)
	return core.NewAgent(currentProvider,
		core.WithTools(newTools...),
		core.WithSystemPrompt(prompt),
		core.WithMiddlewares(buildMiddlewares(currentProvider, cfg, skillsMW, newToolInfos, resolver, modeCtl)...),
		core.WithSessionStore(sessionStore),
	), nil
}

// rebuildAgentForVariant rebuilds the provider with the named variant's overrides applied,
// then rebuilds the core.Agent against the new provider (tools/system prompt unchanged). Used by the
// /variants picker. Returns the new provider (so /model + /mcps rebuilds pick it up) and the agent.
func rebuildAgentForVariant(
	specs []agentSpec,
	prevProvider core.ModelProvider,
	prof config.Profile,
	reqTimeout time.Duration,
	agentName string,
	cfg *config.Config,
	sandbox builtins.Sandbox,
	mcpToolsProvider func() []core.Tool,
	skillsMW *middlewares.Skills,
	sessionStore core.SessionStore,
	resolver middlewares.AskResolver,
	modeCtl *middlewares.ModeController,
	ctx context.Context,
	variantName string,
) (core.ModelProvider, core.Agent, error) {
	newProvider, perr := buildProvider(ctx, prof, reqTimeout, variantName)
	if perr != nil {
		return nil, nil, fmt.Errorf("build provider for variant %q: %w", variantName, perr)
	}
	newTools := filterToolsByAgent(buildAllToolsWithProvider(newProvider, cfg, sandbox, mcpToolsProvider, skillsMW), agentName, specs)
	newToolInfos := make([]core.ToolInfo, len(newTools))
	for i, t := range newTools {
		newToolInfos[i] = t.Info()
	}
	prompt := systemPromptForAgent(agentName, specs)
	newAg := core.NewAgent(newProvider,
		core.WithTools(newTools...),
		core.WithSystemPrompt(prompt),
		core.WithMiddlewares(buildMiddlewares(newProvider, cfg, skillsMW, newToolInfos, resolver, modeCtl)...),
		core.WithSessionStore(sessionStore),
	)
	return newProvider, newAg, nil
}

// toTUIAgents converts internal agentSpecs into the TUI-side AgentSpec (separate type to avoid a
// tui->main import edge).
func toTUIAgents(specs []agentSpec) []tui.AgentSpec {
	out := make([]tui.AgentSpec, len(specs))
	for i, s := range specs {
		out[i] = tui.AgentSpec{Name: s.Name, Description: s.Description, ReadOnly: s.ReadOnly}
	}
	return out
}
