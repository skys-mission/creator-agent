// Command creator-agent is an interactive coding agent CLI.
//
// Usage:
//
//	creator-agent                  # interactive mode (multi-turn REPL)
//	creator-agent "your prompt"    # headless one-shot
//	creator-agent -profile deepseek "..."
//
// Config: TOML at $XDG_CONFIG_HOME/creator/config.toml or ~/.creator/config.toml
// (first run launches an interactive setup wizard on a TTY); env overrides; flag overrides.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"runtime/debug"
	"strings"
	"syscall"
	"time"

	"github.com/skys-mission/creator-agent/cmd/creator-agent/diag"
	"github.com/skys-mission/creator-agent/cmd/creator-agent/tui"
	"github.com/skys-mission/creator-agent/config"
	"github.com/skys-mission/creator-agent/core"
	"github.com/skys-mission/creator-agent/core/adapters/openai"
	"github.com/skys-mission/creator-agent/core/builtins"
	"github.com/skys-mission/creator-agent/core/mcp"
	"github.com/skys-mission/creator-agent/core/middlewares"
)

const systemPrompt = `You are creator-agent, an open-source coding agent (Go).

You help with software tasks in the user's current project. You can call tools to read/edit/run code.

Available tools:
- read: read a file's content
- write: create or overwrite a file
- edit: edit a file (replace a unique old_string with new_string)
- bash: run a shell command
- grep: search file contents (regex)
- glob: find files by name pattern (supports **)
- task: delegate a sub-task to a read-only sub-agent (keeps the main context clean by returning only a conclusion)
- skill: load a named skill's full body when its frontmatter indicates it fits the task
- todo_write: manage the task progress list (planned/pending/in_progress/completed)

When to use tools vs just reply:
- If the user's message is a clear, actionable request (e.g. "refactor X", "fix the bug in Y", "add a test for Z", "what does file W do"), act on it with tools.
- If the message is ambiguous, a bare value (e.g. "111", "yes", "ok"), or a conversational reply, DO NOT invent a task. Ask for clarification or respond in conversation.
- Never fabricate file paths, content, or commands. If you don't know what the user wants, ask.

Rules:
- Be concise; no preamble before acting.
- Read before editing; never guess file contents.
- When done with a task, give a brief summary of what you did.

Using todo_write (task tracking) — status semantics decide whether you PLAN or EXECUTE:
- "planned" = proposed but NOT committed (you are only listing ideas/options, e.g. "find something to do", "evaluate options"). Do NOT start working on planned items.
- "pending" = confirmed to do, queued. The plan is locked in and you will execute it.
- "in_progress" = doing it right now. "completed" = done.

Two modes — pick by what the user asked:
1. EXECUTE mode (user gave a clear, actionable task like "refactor X", "fix bug Y"): FIRST call todo_write with the full plan, first item "in_progress" and the rest "pending". Then immediately start executing. Update the list as each step finishes (mark "completed", next "in_progress"). Keep EXACTLY ONE "in_progress" at a time. Never leave work with zero "in_progress" while items remain.
2. PLAN mode (user asked to "list ideas / find something to do / evaluate / propose options" with no instruction to act): call todo_write with all items "planned". Then STOP and present the plan to the user. Do not start executing until the user picks something. Transitioning "planned" -> "pending" happens only when the user (or you, after they confirm) commits to doing it.

Rules: pass the FULL list each time (replacement, not incremental). Do NOT use todo_write for simple tasks (1-2 steps) or conversational replies — it adds noise. Never mark something "completed" that you did not actually do.`

func main() {
	defer recoverMain()
	useColor = isTTY(os.Stdout)
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "creator-agent — open-source, multi-model-neutral AI coding agent\n\n")
		fmt.Fprintf(os.Stderr, "Usage:\n")
		fmt.Fprintf(os.Stderr, "  creator-agent                  interactive mode (starts a new session each launch)\n")
		fmt.Fprintf(os.Stderr, "  creator-agent \"your prompt\"    headless one-shot (write operations are blocked)\n")
		fmt.Fprintf(os.Stderr, "  creator-agent -profile openai  use the specified profile\n")
		fmt.Fprintf(os.Stderr, "  creator-agent -c               continue the most recent session\n")
		fmt.Fprintf(os.Stderr, "  creator-agent -r               resume a session via the picker on startup\n\n")
		fmt.Fprintf(os.Stderr, "First run: if no config and no OPENAI_API_KEY, an interactive setup wizard runs on a TTY (XDG_CONFIG_HOME/creator or ~/.creator); non-TTY prints a text guide.\n\n")
		fmt.Fprintf(os.Stderr, "Config priority: flag > env var > ./.creator/config.toml > global config.toml (XDG or ~/.creator)\n")
		fmt.Fprintf(os.Stderr, "Env vars: OPENAI_API_KEY / OPENAI_BASE_URL / OPENAI_MODEL (optional: CREATOR_AGENT_TYPE / CREATOR_AGENT_REQUEST_TIMEOUT)\n\n")
		fmt.Fprintf(os.Stderr, "Options:\n")
		flag.PrintDefaults()
	}
	profileName := flag.String("profile", "", "model profile name (from config)")
	providerType := flag.String("type", "", "provider type (openai/anthropic); default openai")
	baseURL := flag.String("base-url", "", "OpenAI-compatible base URL")
	apiKey := flag.String("api-key", "", "API key")
	modelName := flag.String("model", "", "model name")
	requestTimeout := flag.String("request-timeout", "", "stream request timeout (e.g. 10m, 600s); default 10m")
	// Session resume flags (TUI mode only). -c/--continue opens the most recent session;
	// -r/--resume pops the session picker on startup. Standard library flag needs two registrations
	// sharing one variable to provide both the short and long form.
	var continueLast, resumePick bool
	flag.BoolVar(&continueLast, "c", false, "continue the most recent session (TUI mode)")
	flag.BoolVar(&continueLast, "continue", false, "continue the most recent session (TUI mode)")
	flag.BoolVar(&resumePick, "r", false, "resume a session via the picker on startup (TUI mode)")
	flag.BoolVar(&resumePick, "resume", false, "resume a session via the picker on startup (TUI mode)")
	flag.Parse()

	// Crash-safety supervisor (interactive TUI only): run the real TUI as a child process so the
	// terminal is always restored even on Go fatal runtime errors that bypass every deferred cleanup.
	// No-op as the supervised child, when opted out, or for headless / non-TTY modes. Runs before any
	// heavy init so the supervisor stays a minimal, healthy process.
	if superviseTTY() {
		return
	}

	// Always-on crash diagnostics: route the runtime's fatal crash report to ~/.creator/fatal.log
	// and start the breadcrumb trace (~/.creator/trace.log). This is the only way to post-mortem
	// the memory-corruption fatal errors that bypass every defer/recover, so it runs in the normal
	// (non-race) build too. Cheap and best-effort; degrades to off if the log dir is unavailable.
	diag.Setup()
	diag.Trace("main start args=%v", flag.Args())

	cfg, err := config.Load()
	if err != nil {
		die(err)
	}
	for _, warn := range cfg.Warnings() {
		fmt.Fprintf(os.Stderr, "warn: %s\n", warn)
	}

	// First-run setup: no config file + no env key + no flag key. On a TTY we run the interactive
	// full-screen wizard and persist its result, then reload; otherwise we print the text guide and
	// exit (a non-interactive environment cannot complete the wizard).
	if config.NeedsSetup(cfg.LoadedPath(), *apiKey) {
		if isTTY(os.Stdin) && isTTY(os.Stdout) {
			res, werr := tui.RunSetupWizard(cfg.Appearance.NormalizedLanguage())
			if werr != nil {
				die(werr)
			}
			if !res.Confirmed {
				gp, _ := config.GlobalConfigPath()
				fmt.Print(firstRunGuide(gp))
				return
			}
			path, werr := config.WriteInitialConfig(res.Profile, res.ProfileName)
			if werr != nil {
				die(werr)
			}
			fmt.Fprintf(os.Stderr, "Config written to %s\n", path)
			if cfg, err = config.Load(); err != nil {
				die(err)
			}
		} else {
			if path, _ := config.EnsureConfigFile(); path != "" {
				fmt.Print(firstRunGuide(path))
			}
			return
		}
	}

	prof, err := cfg.Resolve(*profileName, config.Profile{
		Type:           *providerType,
		BaseURL:        *baseURL,
		APIKey:         *apiKey,
		Model:          *modelName,
		RequestTimeout: *requestTimeout,
	})
	if err != nil {
		die(err)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM)
	defer cancel()
	defer runCleanups() // on normal main return, run cleanups before cancel (LIFO)

	var provider core.ModelProvider
	// Parse request_timeout (empty = adapter default)
	var reqTimeout time.Duration
	if prof.RequestTimeout != "" {
		reqTimeout, err = time.ParseDuration(prof.RequestTimeout)
		if err != nil {
			die(fmt.Errorf("invalid request_timeout %q: %w", prof.RequestTimeout, err))
		}
	}
	switch prof.NormalizedType() {
	case "openai":
		oc, varErr := resolveVariantConfig(prof, reqTimeout, normalizeVariantName(prof.Variant))
		if varErr != nil {
			fmt.Fprintf(os.Stderr, "warn: default variant %q not found, using base config: %v\n", prof.Variant, varErr)
			oc, _ = resolveVariantConfig(prof, reqTimeout, "")
		}
		provider, err = openai.NewProvider(ctx, oc)
	case "anthropic":
		err = fmt.Errorf("anthropic provider not implemented in v0.1")
	default:
		err = fmt.Errorf("unknown provider type %q", prof.Type)
	}
	if err != nil {
		die(err)
	}

	// sandbox (opt-in): when enabled, choose sandbox-exec/bwrap by GOOS; missing binary fails closed.
	sandbox, sandboxErr := builtins.NewSandbox(cfg.Sandbox.Enabled, cfg.Sandbox.AllowDirs)
	if sandboxErr != nil {
		die(sandboxErr)
	}

	// MCP servers are connected once at startup via a Manager that tracks per-server state, so the
	// TUI /mcps picker can enable/disable individual servers at runtime. Successfully connected
	// servers start enabled; failed ones are recorded but skipped (best-effort, like LoadAll).
	mcpServers, mcpWarnings, mcpErr := config.LoadMCPServers()
	if mcpErr != nil {
		fmt.Fprintf(os.Stderr, "warn: mcp config: %v\n", mcpErr)
	}
	for _, w := range mcpWarnings {
		fmt.Fprintf(os.Stderr, "warn: %s\n", w)
	}
	mcpManager := mcp.NewManager(ctx, mcpServers)
	for _, st := range mcpManager.Status() {
		if st.LastErr != "" {
			fmt.Fprintf(os.Stderr, "warn: mcp server %q failed: %s\n", st.Name, st.LastErr)
		}
	}
	registerCleanup(mcpManager.Close) // replaces defer: also effective on os.Exit paths
	// mcpToolsProvider reads the current enabled set on each call so a /mcps toggle takes effect on
	// the next agent rebuild without reconnecting here.
	mcpToolsProvider := mcpManager.EnabledTools
	registerCleanup(func() { _ = core.CleanupSpills() }) // clean up temp files from large tool results

	// Skills system (when user enabled): construct skills middleware (injects frontmatter summaries) + SkillTool (loads body on demand).
	// The same skills instance is shared between middleware (injects summaries) and tool (looks up body).
	var skillsMW *middlewares.Skills
	if cfg.Skills.Enabled {
		skillsMW = middlewares.NewSkills()
		if cfg.Skills.Budget > 0 {
			skillsMW.Budget = cfg.Skills.Budget
		}
		skillsMW = skillsMW.WithDirs(cfg.Skills.Dirs...)
	}

	// Shared session store for the process: ensures /model switching and concurrent access
	// are serialized by the same in-process mutex.
	var sessionStore core.SessionStore = core.NewMemoryStore()
	if store, err := core.NewJSONFileStore(""); err == nil {
		sessionStore = store
	} else {
		fmt.Fprintln(os.Stderr, "warn: session persistence disabled:", err)
	}

	// Assemble tools: built-ins + MCP + task + skill. Rebuilt on /model switch so the
	// task sub-agent inherits the new provider.
	allTools := buildAllToolsWithProvider(provider, cfg, sandbox, mcpToolsProvider, skillsMW)

	// Tool capability declarations (used by Permission default policy ask_writes to determine ReadOnly)
	toolInfos := make([]core.ToolInfo, len(allTools))
	for i, t := range allTools {
		toolInfos[i] = t.Info()
	}

	// Permission mode controller (default/trust/auto/readonly): seeded from config, shared with the
	// permission middleware and the TUI so a runtime /mode switch takes effect on the next tool call
	// without rebuilding the agent.
	modeCtl := middlewares.NewModeController(middlewares.Mode(cfg.Permissions.NormalizedMode()))

	// Assemble agent closure (different modes use different resolvers).
	// Extracted because TUI mode needs asyncApprover (channel handshake), while dumb/headless use synchronous resolvers.
	buildAgent := func(resolver middlewares.AskResolver) core.Agent {
		return core.NewAgent(provider,
			core.WithTools(allTools...),
			core.WithSystemPrompt(systemPrompt),
			core.WithMiddlewares(buildMiddlewares(provider, cfg, skillsMW, toolInfos, resolver, modeCtl)...),
			core.WithSessionStore(sessionStore),
		)
	}

	// currentProvider is the live provider, updated by each /model switch (and by /variants, which
	// rebuilds the provider to apply a variant's request overrides). Declared here so the rebuild
	// closures (rebuildAgent, rebuildAgentForTools, rebuildAgentForAgent, rebuildAgentForVariant)
	// can all close over it. currentAgentName is the live agent name so any rebuild preserves the
	// active persona's system prompt + tool whitelist. currentVariantName is the live variant so a
	// /model switch carries it onto the new profile's provider.
	currentProvider := provider
	currentAgentName := "build"
	currentVariantName := normalizeVariantName(prof.Variant)
	agentSpecs := builtinAgents()

	// toolsForCurrentAgent rebuilds the tool list + ToolInfos for the active persona over a given
	// provider; newAgentFor assembles the agent with the persona's system prompt. Shared by the
	// /model (rebuildAgent) and /mcps (rebuildAgentForTools) rebuild paths.
	toolsForCurrentAgent := func(p core.ModelProvider) ([]core.Tool, []core.ToolInfo) {
		tools := filterToolsByAgent(buildAllToolsWithProvider(p, cfg, sandbox, mcpToolsProvider, skillsMW), currentAgentName, agentSpecs)
		infos := make([]core.ToolInfo, len(tools))
		for i, t := range tools {
			infos[i] = t.Info()
		}
		return tools, infos
	}
	newAgentFor := func(p core.ModelProvider, tools []core.Tool, infos []core.ToolInfo, resolver middlewares.AskResolver) core.Agent {
		return core.NewAgent(p,
			core.WithTools(tools...),
			core.WithSystemPrompt(systemPromptForAgent(currentAgentName, agentSpecs)),
			core.WithMiddlewares(buildMiddlewares(p, cfg, skillsMW, infos, resolver, modeCtl)...),
			core.WithSessionStore(sessionStore),
		)
	}

	// resolveCurrentTimeout parses the request timeout of the currently-active profile. Returns the
	// default (0) when unset. Used by the /variants rebuild so the new provider inherits the active
	// profile's timeout rather than the startup profile's.
	resolveCurrentTimeout := func() time.Duration {
		if prof.RequestTimeout == "" {
			return 0
		}
		nt, err := time.ParseDuration(prof.RequestTimeout)
		if err != nil {
			return 0
		}
		return nt
	}

	// rebuildAgent is the runtime /model switch rebuild factory: rebuilds provider + tools + agent by profile name.
	// Session history is preserved by the shared SessionStore — the new agent loads the same sessionID's history.
	// Returns the new profile config (for TUI status bar update).
	rebuildAgent := func(resolver middlewares.AskResolver, profileName string) (core.Agent, config.Profile, error) {
		newProf, rerr := cfg.Resolve(profileName, config.Profile{})
		if rerr != nil {
			return nil, config.Profile{}, fmt.Errorf("resolve profile %q: %w", profileName, rerr)
		}
		var nt time.Duration
		if newProf.RequestTimeout != "" {
			nt, rerr = time.ParseDuration(newProf.RequestTimeout)
			if rerr != nil {
				return nil, config.Profile{}, fmt.Errorf("invalid request_timeout %q: %w", newProf.RequestTimeout, rerr)
			}
		}
		if newProf.NormalizedType() != "openai" {
			return nil, config.Profile{}, fmt.Errorf("runtime switch only supports openai-compatible providers; profile %q is %s", profileName, newProf.NormalizedType())
		}
		// Preserve the current variant across a /model switch: the new provider carries the active
		// variant's overrides so the user's last variant choice is not silently dropped.
		// When the variant doesn't exist on the target profile, fall back to the profile's
		// default config with a warning (instead of passing a zero-value Config to NewProvider).
		var oc openai.Config
		oc, varErr := resolveVariantConfig(newProf, nt, currentVariantName)
		if varErr != nil {
			fmt.Fprintf(os.Stderr, "warn: variant %q not found on profile %q, using default: %v\n", currentVariantName, profileName, varErr)
			currentVariantName = "default"
			oc, _ = resolveVariantConfig(newProf, nt, "")
		}
		newProvider, perr := openai.NewProvider(ctx, oc)
		if perr != nil {
			return nil, config.Profile{}, fmt.Errorf("build provider: %w", perr)
		}
		newTools, newToolInfos := toolsForCurrentAgent(newProvider)
		newAg := newAgentFor(newProvider, newTools, newToolInfos, resolver)
		currentProvider = newProvider // remember for /mcps rebuilds (which reuse the current provider)
		prof = newProf                // remember for /variants rebuilds (which read the current profile)
		return newAg, newProf, nil
	}

	// rebuildAgentForTools rebuilds the agent using the current provider + the latest MCP enabled
	// set (read fresh via mcpToolsProvider). Used by the /mcps picker after a server toggle so the
	// agent's tool list reflects the new enabled set. Session history is preserved by the store.
	// Preserves the active agent's system prompt + tool whitelist.
	rebuildAgentForTools := func(resolver middlewares.AskResolver) (core.Agent, error) {
		newTools, newToolInfos := toolsForCurrentAgent(currentProvider)
		return newAgentFor(currentProvider, newTools, newToolInfos, resolver), nil
	}

	// currentVariantName tracks the live variant so /model and /variants switches compose correctly
	// (a /model switch carries the current variant onto the new profile's provider; "" = default).
	// Declared above (near currentProvider) so all rebuild closures can close over it.

	// switchAgentFactory backs /agents: rebuilds the agent with the chosen agent's system prompt +
	// tool whitelist over the current provider. Returns the new agent; the caller (run.go) enqueues
	// a switchAgentMsg to apply it on the event loop. Updates currentAgentName so subsequent /model
	// and /mcps rebuilds preserve the new persona.
	switchAgentFactory := func(resolver middlewares.AskResolver, agentName string) (core.Agent, error) {
		newAg, aerr := rebuildAgentForAgent(agentSpecs, currentProvider, cfg, sandbox, mcpToolsProvider, skillsMW, sessionStore, resolver, modeCtl, agentName)
		if aerr != nil {
			return nil, aerr
		}
		currentAgentName = agentName
		return newAg, nil
	}

	// switchVariantFactory backs /variants: rebuilds the provider with the named variant's request
	// overrides applied, then rebuilds the agent against it. Updates currentProvider (so /model and
	// /mcps rebuilds reuse it) and currentVariantName (so /model carries it onto a new profile).
	switchVariantFactory := func(resolver middlewares.AskResolver, variantName string) (core.Agent, error) {
		np, newAg, verr := rebuildAgentForVariant(agentSpecs, currentProvider, prof, resolveCurrentTimeout(), currentAgentName, cfg, sandbox, mcpToolsProvider, skillsMW, sessionStore, resolver, modeCtl, ctx, variantName)
		if verr != nil {
			return nil, verr
		}
		currentProvider = np
		currentVariantName = normalizeVariantName(variantName)
		return newAg, nil
	}

	args := flag.Args()
	// -c/--continue and -r/--resume only apply to the TUI; in headless mode sessions are stateless,
	// so flag them and ignore rather than silently doing nothing.
	if len(args) > 0 && (continueLast || resumePick) {
		fmt.Fprintln(os.Stderr, "warn: -c/--continue and -r/--resume are ignored in headless mode")
		continueLast = false
		resumePick = false
	}
	switch {
	case len(args) > 0:
		// headless: deny resolver + raw output (unchanged)
		ag := buildAgent(headlessApprover{}.approve)
		runHeadless(ctx, ag, strings.Join(args, " "))

	case isTTY(os.Stdin) && isTTY(os.Stdout):
		// REPL + real TTY -> self-built full-screen TUI (asyncApprover + channel handshake approval)
		// Apply the configured theme before the first render so startup uses the user's choice.
		tui.ApplyTheme(cfg.Appearance.NormalizedTheme())
		// Decide which session the TUI opens into:
		//   -c/--continue -> the most recent stored session (falls back to a fresh id when none);
		//   default       -> a fresh session each launch (no automatic resume);
		//   -r/--resume   -> fresh id too, but the picker pops on the first frame (see WithInitialSessionPickerIf).
		initialSession := core.GenerateSessionID()
		if continueLast {
			initialSession = recentSessionID(sessionStore, initialSession)
		}
		if err := tui.RunWithMiddleware(ctx, func(resolver middlewares.AskResolver) (core.Agent, context.CancelFunc, error) {
			return buildAgent(resolver), func() {}, nil
		}, rebuildAgent, prof, toolInfos, *profileName,
			// Multi-session: inject the persistent store so /new and /sessions survive restart.
			tui.WithSessionStore(sessionStore),
			tui.WithInitialSession(initialSession),
			// -r/--resume: pop the session picker on the first frame (no-op when resumePick is false).
			tui.WithInitialSessionPickerIf(resumePick),
			// Model picker: inject the configured profiles so /models can list and switch them.
			tui.WithProfiles(cfg.Profiles),
			// MCP picker: inject the manager + a tool-rebuild factory so /mcps can toggle servers at
			// runtime. The factory rebuilds the agent against the current provider with the latest
			// enabled MCP set; run.go supplies the live resolver when invoking it.
			tui.WithMCPManager(mcpManagerAdapter{mcpManager}),
			tui.WithRebuildToolsFactory(func(resolver middlewares.AskResolver) (core.Agent, error) {
				return rebuildAgentForTools(resolver)
			}),
			// Agent picker (/agents): inject the built-in agent specs (build/plan) and a switch
			// factory that rebuilds the agent with the chosen persona's system prompt + tool whitelist.
			tui.WithAgents(toTUIAgents(agentSpecs)),
			tui.WithAgentSwitchFactory(func(resolver middlewares.AskResolver) func(string) (core.Agent, error) {
				return func(name string) (core.Agent, error) { return switchAgentFactory(resolver, name) }
			}),
			// Variant picker (/variants): inject the current profile's declared variants + a switch
			// factory that rebuilds the provider with the chosen variant's request overrides applied.
			tui.WithVariants(prof.Variants, currentVariantName),
			tui.WithVariantSwitchFactory(func(resolver middlewares.AskResolver) func(string) (core.Agent, error) {
				return func(name string) (core.Agent, error) { return switchVariantFactory(resolver, name) }
			}),
			// Permission mode (default/trust/auto/readonly): the controller is shared with the
			// permission middleware, so /mode switching takes effect on the next tool call.
			tui.WithModeController(modeCtl, cfg.Permissions.NormalizedMode()),
			// Auto session title: inject a generator that reads the current provider through a closure
			// (so /model and /variants switches, which rebuild the provider, are picked up with no
			// re-injection). nil when no provider — auto-titling is then disabled gracefully.
			tui.WithTitleGenerator(newTitleGenerator(func() core.ModelProvider { return currentProvider })),
			// Manual /compact: compact the current session's history via the current provider.
			tui.WithCompactor(newCompactFactory(func() core.ModelProvider { return currentProvider }, sessionStore)),
			// UI language: config-driven or locale auto-detected.
			tui.WithLanguage(cfg.Appearance.NormalizedLanguage()),
		); err != nil {
			fmt.Fprintln(os.Stderr, "tui error:", err)
			exitWith(1) // go through cleanup, not bare os.Exit
		}

	default:
		// REPL + non-TTY (pipes/testing) -> dumb readline path (unchanged)
		ap := newReplApprover(os.Stdin, os.Stdout)
		ag := buildAgent(ap.approve)
		runREPL(ctx, ag, prof)
	}
}

// recentSessionID returns the id of the most recently updated session in the store (List is sorted
// by UpdatedAt descending), or fallback when the store is empty or unreadable. Used by -c/--continue
// to resume the last session without prompting.
func recentSessionID(store core.SessionStore, fallback string) string {
	if store == nil {
		return fallback
	}
	infos, err := store.List()
	if err != nil || len(infos) == 0 {
		return fallback
	}
	return infos[0].ID
}

// buildAllTools assembles the complete tool list for a given provider.
// It is called both at startup and on runtime /model switches so the task sub-agent
// always inherits the current provider. MCP tools and the skills instance are reused
// (they are not provider-specific).
func buildAllTools(provider core.ModelProvider, cfg *config.Config, sandbox builtins.Sandbox, mcpTools []core.Tool, skillsMW *middlewares.Skills) []core.Tool {
	return buildAllToolsWithProvider(provider, cfg, sandbox, func() []core.Tool { return mcpTools }, skillsMW)
}

// buildAllToolsWithProvider is the runtime form: the MCP tool set is read lazily via the provider
// closure so the latest enabled set (after a /mcps toggle) is picked up on every rebuild.
func buildAllToolsWithProvider(provider core.ModelProvider, cfg *config.Config, sandbox builtins.Sandbox, mcpToolsProvider func() []core.Tool, skillsMW *middlewares.Skills) []core.Tool {
	// Base built-ins. Per-tool settings come from the three-layer inheritance
	// (tools.<name> > tools_defaults > builtin).
	baseTools := []core.Tool{
		builtins.NewReadTool(),
		builtins.NewWriteTool(),
		builtins.NewEditTool(),
		builtins.NewBashTool(
			builtins.WithSandbox(sandbox),
			builtins.WithBashTimeout(resolveToolSettings("bash", cfg, builtins.BashDefaultsInput()).Timeout),
		),
		builtins.NewGrepTool(builtins.WithGrepSettings(resolveToolSettings("grep", cfg, builtins.GrepDefaultsInput()))),
		builtins.NewGlobTool(builtins.WithGlobSettings(resolveToolSettings("glob", cfg, builtins.GlobDefaultsInput()))),
		builtins.NewTodoTool(builtins.NewTodoStore()), // todo list (session-level, exempt from approval)
	}
	mcpTools := mcpToolsProvider()
	allTools := append(baseTools, mcpTools...)

	// task tool: subagent delegation. Child session uses read-only tools (read/grep/glob) + derived parent cfg.
	// maxTaskDepth comes from the tool settings inheritance (configurable, capped by the hard limit).
	parentCfg := core.AgentConfig{
		Model:        provider,
		SystemPrompt: systemPrompt,
	}
	grepChild := builtins.NewGrepTool(builtins.WithGrepSettings(resolveToolSettings("grep", cfg, builtins.GrepDefaultsInput())))
	globChild := builtins.NewGlobTool(builtins.WithGlobSettings(resolveToolSettings("glob", cfg, builtins.GlobDefaultsInput())))
	readOnlyTools := []core.Tool{builtins.NewReadTool(), grepChild, globChild}
	taskMaxDepth := resolveToolSettings("task", cfg, builtins.TaskDefaultsInput()).MaxDepth
	taskTool := builtins.NewTaskTool(parentCfg, readOnlyTools, 10, builtins.TaskWithMaxDepth(taskMaxDepth))
	allTools = append(allTools, taskTool)

	if skillsMW != nil {
		allTools = append(allTools, builtins.NewSkillTool(skillsMW))
	}
	return allTools
}

// recoverMain is the process-level panic safety net. It catches any panic that propagates to the
// main goroutine past the per-goroutine recovers (event loop, stream pump, tool execution, provider
// adapter), persists the cause + stack to the crash log, restores the terminal defensively in case
// the TUI's own deferred Close did not run, and exits non-zero. Without it, an unexpected panic
// would kill the process with only a raw stack that the alternate screen wipes on exit.
func recoverMain() {
	if r := recover(); r != nil {
		tui.LogCrashToDisk(r, debug.Stack())
		// Defensive terminal restore: must mirror Terminal.Close() exactly. Omitting the mouse
		// tracking disable leaves the terminal emitting SGR mouse reports into the parent shell
		// after a crash, which renders as raw CSI <btn;col;row M/m garbage. No-op when the process
		// never entered the alt screen (headless / REPL paths).
		_ = tui.RestoreTerminal(os.Stdout)
		fmt.Fprintf(os.Stderr, "creator-agent crashed: %v\n(full stack: ~/.creator/tui-crash.log)\n", r)
		exitWith(1)
	}
}
