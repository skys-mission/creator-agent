package tui

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	runewidth "github.com/mattn/go-runewidth"

	"github.com/skys-mission/creator-agent/cmd/creator-agent/tui/i18n"
	"github.com/skys-mission/creator-agent/config"
	"github.com/skys-mission/creator-agent/core"
	"github.com/skys-mission/creator-agent/core/middlewares"
)

// RunOption configures optional TUI assembly (session store, initial session, sidebar). Defaults
// use a MemoryStore and a freshly generated session id (a new session each launch). Using a
// variadic options tail keeps RunWithMiddleware's existing signature backward-compatible.
type RunOption func(*runConfig)

type runConfig struct {
	store               core.SessionStore
	initialSession      string
	resumeOnStart       bool
	profiles            map[string]config.Profile
	mcpManager          MCPManager
	rebuildToolsFactory func(resolver middlewares.AskResolver) (core.Agent, error)

	agents             []AgentSpec
	agentSwitchFactory func(resolver middlewares.AskResolver) func(string) (core.Agent, error)

	variants             map[string]config.Variant
	initialVariant       string
	variantSwitchFactory func(resolver middlewares.AskResolver) func(string) (core.Agent, error)

	// unavailable (the TUI skips it and keeps the default placeholder).
	titleGen TitleGenerator

	compactor Compactor

	modeCtl     *middlewares.ModeController
	initialMode string

	language string
}

// WithModeController injects the shared permission-mode controller (default/trust/auto/readonly)
// and the initial mode label, enabling /mode switching. The controller is shared with the permission
// middleware, so a switch takes effect on the next tool call with no agent rebuild. nil leaves mode
// features disabled (legacy path).
func WithModeController(m *middlewares.ModeController, initial string) RunOption {
	return func(rc *runConfig) {
		rc.modeCtl = m
		rc.initialMode = initial
	}
}

// WithSessionStore injects the session store used for multi-session switching (/new, /sessions).
// When omitted, a MemoryStore is used (sessions lost on restart).
func WithSessionStore(s core.SessionStore) RunOption {
	return func(c *runConfig) { c.store = s }
}

// WithInitialSession sets the session id the TUI opens into. When omitted, a fresh generated id
// is used (a new session each launch). Pass an existing session id to resume it on startup.
func WithInitialSession(id string) RunOption {
	return func(c *runConfig) { c.initialSession = id }
}

// WithInitialSessionPicker opens the session picker on the first frame so the user can choose which
// session to resume (TUI -r/--resume). No-op when the store has no sessions.
func WithInitialSessionPicker() RunOption {
	return func(c *runConfig) { c.resumeOnStart = true }
}

// WithInitialSessionPickerIf opens the session picker on the first frame only when enable is true.
// Lets callers wire the flag condition inline without constructing a RunOption literal in another
// package (runConfig is unexported).
func WithInitialSessionPickerIf(enable bool) RunOption {
	return func(c *runConfig) {
		if enable {
			c.resumeOnStart = true
		}
	}
}

// WithProfiles injects the configured model profiles so the /models picker can list and switch
// between them at runtime. When omitted, the picker reports no profiles configured.
func WithProfiles(p map[string]config.Profile) RunOption {
	return func(c *runConfig) { c.profiles = p }
}

// WithMCPManager injects the MCP server manager backing the /mcps picker. When omitted, /mcps
// reports MCP as unavailable.
func WithMCPManager(m MCPManager) RunOption {
	return func(c *runConfig) { c.mcpManager = m }
}

// WithRebuildToolsFactory injects a factory that rebuilds the agent against the current provider
// with the latest tool set. Called after a /mcps toggle so the agent picks up the new enabled MCP
// servers. run.go supplies the live approval resolver when invoking it.
func WithRebuildToolsFactory(f func(resolver middlewares.AskResolver) (core.Agent, error)) RunOption {
	return func(c *runConfig) { c.rebuildToolsFactory = f }
}

// WithAgents injects the selectable agent specs (build/plan + configured) backing the /agents
// picker. When omitted, /agents reports no agents available.
func WithAgents(a []AgentSpec) RunOption {
	return func(c *runConfig) { c.agents = a }
}

// WithAgentSwitchFactory injects the factory backing /agents runtime switching. Given the live
// resolver it returns a switch func that rebuilds the agent for a chosen agent name. When omitted,
// runtime agent switching is unavailable (the picker reports it).
func WithAgentSwitchFactory(f func(resolver middlewares.AskResolver) func(string) (core.Agent, error)) RunOption {
	return func(c *runConfig) { c.agentSwitchFactory = f }
}

// WithVariants injects the variant map for the current profile + the startup variant name, backing
// the /variants picker. When the map is empty/omitted, /variants reports no variants configured.
func WithVariants(v map[string]config.Variant, initialVariant string) RunOption {
	return func(c *runConfig) { c.variants = v; c.initialVariant = initialVariant }
}

// WithVariantSwitchFactory injects the factory backing /variants runtime switching. Given the live
// resolver it returns a switch func that rebuilds the provider with a chosen variant's overrides
// applied. When omitted, runtime variant switching is unavailable.
func WithVariantSwitchFactory(f func(resolver middlewares.AskResolver) func(string) (core.Agent, error)) RunOption {
	return func(c *runConfig) { c.variantSwitchFactory = f }
}

// WithTitleGenerator injects the async session-title generator. After a session's first user
// message, the TUI calls it in the background and persists the result via SaveWithMeta. When
// omitted, sessions keep their default (timestamped) title.
func WithTitleGenerator(g TitleGenerator) RunOption {
	return func(c *runConfig) { c.titleGen = g }
}

// WithCompactor injects the manual history compactor backing /compact. When omitted, /compact
// reports manual compaction unavailable (automatic compaction via the Summarization middleware is
// unaffected).
func WithCompactor(c2 Compactor) RunOption {
	return func(c *runConfig) { c.compactor = c2 }
}

// WithLanguage sets the TUI display language. Empty defaults to English. Accepted values: "en", "zh".
func WithLanguage(lang string) RunOption {
	return func(c *runConfig) { c.language = lang }
}

// RunWithMiddleware is the full assembly for TUI mode with approval (used by main.go).
//
// buildMW receives approver.approve as resolver and returns (agent, cancel).
// rebuild is the runtime /model <name> switching factory: rebuilds provider + agent, returns the
// new profile config. When nil, /model switching is unavailable.
func RunWithMiddleware(
	ctx context.Context,
	buildMW func(resolver middlewares.AskResolver) (core.Agent, context.CancelFunc, error),
	rebuild func(resolver middlewares.AskResolver, profileName string) (core.Agent, config.Profile, error),
	prof config.Profile,
	toolInfos []core.ToolInfo,
	profileName string,
	opts ...RunOption,
) error {
	ap := newAsyncApprover()

	ag, cancel, err := buildMW(ap.approve)
	if err != nil {
		return err
	}
	defer cancel()

	// Signals -> cancel (Ctrl+C is handled in raw-mode via KeyCtrlC; this also catches SIGINT
	// and SIGTERM from external signals or terminal UI so the cleanup path runs).
	sigCtx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	rc := runConfig{initialSession: core.GenerateSessionID()}
	for _, opt := range opts {
		opt(&rc)
	}
	if rc.store == nil {
		rc.store = core.NewMemoryStore()
	}
	i18n.SetLang(i18n.ParseLang(rc.language))

	a := &App{
		rt: runtimeState{
			ag:             ag,
			ctx:            sigCtx,
			cancel:         cancel,
			prof:           profile{Name: profileName, Model: prof.Model, BaseURL: prof.BaseURL},
			approver:       ap,
			toolInfos:      toolInfos,
			md:             newMarkdownCache(),
			store:          rc.store,
			sessionID:      rc.initialSession,
			profiles:       rc.profiles,
			mcpManager:     rc.mcpManager,
			agents:         rc.agents,
			currentAgent:   "build",
			variants:       rc.variants,
			currentVariant: rc.initialVariant,
			modeCtl:        rc.modeCtl,
			currentMode:    string(middlewares.NormalizeMode(middlewares.Mode(rc.initialMode))),
			titleGen:       rc.titleGen,
			compactor:      rc.compactor,
		},
		atBottom:      true,
		quitCh:        make(chan struct{}),
		events:        make(chan any, 64),
		history:       loadHistory(),
		resumeOnStart: rc.resumeOnStart,
	}
	a.rt.sender = func(msg any) {
		// Approval requests must never be dropped: if the event queue is full, the approver goroutine
		// blocks waiting for a reply that will never arrive, deadlocking the tool and leaving the UI
		// unresponsive. Other messages remain non-blocking so a slow renderer cannot back-pressure
		// the agent into an unbounded wait.
		switch msg.(type) {
		case askMsg:
			select {
			case a.events <- msg:
			case <-a.quitCh:
			}
		default:
			select {
			case a.events <- msg:
			default:
			}
		}
	}
	// synchronously. The rebuild/switch factories below run on the event-loop goroutine, so a plain
	// blocking send to a full buffer would self-deadlock (the loop is waiting for the sender).
	publishSwitchAgent := func(newAg core.Agent) {
		select {
		case a.events <- switchAgentMsg{ag: newAg}:
		default:
			a.rt.ag = newAg
			a.status = statusIdle
		}
	}

	if rebuild != nil {
		a.rt.rebuild = func(profileName string) (config.Profile, error) {
			newAg, newProf, rerr := rebuild(ap.approve, profileName)
			if rerr != nil {
				return config.Profile{}, rerr
			}
			publishSwitchAgent(newAg)
			return newProf, nil
		}
	}
	if rc.rebuildToolsFactory != nil {
		a.rt.rebuildTools = func() error {
			newAg, rerr := rc.rebuildToolsFactory(ap.approve)
			if rerr != nil {
				return rerr
			}
			publishSwitchAgent(newAg)
			return nil
		}
	}
	if rc.agentSwitchFactory != nil {
		switchAgent := rc.agentSwitchFactory(ap.approve)
		a.rt.agentSwitch = func(name string) error {
			newAg, serr := switchAgent(name)
			if serr != nil {
				return serr
			}
			publishSwitchAgent(newAg)
			return nil
		}
	}
	if rc.variantSwitchFactory != nil {
		switchVariant := rc.variantSwitchFactory(ap.approve)
		a.rt.variantSwitch = func(name string) error {
			newAg, verr := switchVariant(name)
			if verr != nil {
				return verr
			}
			publishSwitchAgent(newAg)
			return nil
		}
	}

	// Width model: the renderer sizes every cell with go-runewidth. Its DefaultCondition uses
	// EastAsianWidth=false unless RUNEWIDTH_EASTASIAN is present, but CJK terminals often render
	// ambiguous-width characters (box-drawing ─ │, symbols ● ○ □, etc.) at 2 columns. We align the
	// width model to the terminal: if the locale is CJK or RUNEWIDTH_EASTASIAN=1, treat ambiguous
	// chars as width 2. The condition is assigned to the package-level widthCond and restored on exit.
	prevCond := widthCond
	widthCond = configureRunewidth()
	defer func() { widthCond = prevCond }()

	// unrecoverable panic raised inside a library goroutine (e.g. net/http
	// every deferred recover() and getting wiped when the TUI leaves the alternate
	// screen. Pinning fd 2 to a file preserves the full stack for post-mortem
	_ = RedirectStderrToFile()

	// uses absolute per-cell cursor positioning (each emitted cell is preceded by a CUP), which
	// eliminates the CJK-terminal misalignment that cursor-advance-based renderers suffer.
	term, err := OpenTerminal()
	if err != nil {
		return fmt.Errorf("init terminal: %w", err)
	}
	defer term.Close()
	// Last-resort signal cleanup: if the app is stuck in a long render or a goroutine panic
	// prevents the main defer from running, the first SIGINT/SIGTERM waits briefly for the main
	// goroutine to run its deferred term.Close(), then cleans up as a fallback. The delay avoids
	// racing with the normal exit path, which would otherwise write to the main screen after alt
	// screen has been disabled.
	go func() {
		defer func() {
			if r := recover(); r != nil {
				logCrash(r)
			}
		}()
		<-sigCtx.Done()
		select {
		case <-time.After(100 * time.Millisecond):
			term.Close()
		}
	}()
	// Recover from any panic in the main goroutine after terminal cleanup runs, so the terminal
	// is always restored to a usable state even if render/input logic crashes.
	defer func() {
		if r := recover(); r != nil {
			logCrash(r)
		}
	}()
	w, h := term.Size()
	screen := NewScreen(term, w, h)
	a.screen = screen
	a.term = term
	ap.SetSender(a.rt.sender)

	// 5. run the event loop (blocks until quit).
	runLoop(a)
	return nil
}

// configureRunewidth aligns the cell-width model used by tcell with what CJK terminals actually
// paint. tcell sizes cells via runewidth.DefaultCondition, whose EastAsianWidth flag tcell's
// init() sets to false unless RUNEWIDTH_EASTASIAN is present. With that flag false, ambiguous-width
// characters (box-drawing ─ │, symbols ● ○ □ ■, etc.) are counted as 1 column. Many CJK terminal
// fonts render those same characters at 2 columns, so tcell's cursor bookkeeping drifts by one
// cell per ambiguous char and every following cell lands in the wrong place — the root cause of
// the persistent "misalignment" that layout code cannot fix.
//
// Resolution: when the locale is CJK (ja/ko/zh) or the user sets RUNEWIDTH_EASTASIAN=1, set the
// flag to true so tcell treats ambiguous chars as width 2, matching the terminal.
func configureRunewidth() *runewidth.Condition {
	cond := runewidth.DefaultCondition
	if v := os.Getenv("RUNEWIDTH_EASTASIAN"); v != "" {
		// Explicit override: honor the user's choice ("1"/"true" -> width 2, else width 1).
		cond.EastAsianWidth = (v == "1" || strings.EqualFold(v, "true"))
		return cond
	}
	// No explicit override: infer from locale. CJK locales render ambiguous chars at width 2.
	if isCJKLocale() {
		cond.EastAsianWidth = true
	}
	return cond
}

// isCJKLocale reports whether the current locale (LC_ALL/LC_CTYPE/LANG) is a CJK (Chinese,
// Japanese, Korean) locale, where terminals conventionally render ambiguous-width characters at
// 2 columns.
func isCJKLocale() bool {
	for _, key := range []string{"LC_ALL", "LC_CTYPE", "LANG"} {
		loc := strings.ToLower(os.Getenv(key))
		if loc == "" || loc == "c" || loc == "posix" {
			continue
		}
		if strings.HasPrefix(loc, "zh") || strings.HasPrefix(loc, "ja") || strings.HasPrefix(loc, "ko") {
			return true
		}
	}
	return false
}

type switchAgentMsg struct{ ag core.Agent }
