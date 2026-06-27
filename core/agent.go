package core

import (
	"context"
	"fmt"
	"runtime/debug"
	"sync"
)

// Agent is the core executor.
//
// Stream runs with session-aware multi-turn memory; ClearSession wipes history (used by REPL /clear).
type Agent interface {
	Stream(ctx context.Context, in StreamInput) (<-chan Event, error)
	ClearSession(sessionID string)
}

// StreamInput is the input for a single execution.
type StreamInput struct {
	Messages  []Message
	SessionID string
}

// PromptInput is a convenience constructor for a single prompt (simple cases).
func PromptInput(sessionID, prompt string) StreamInput {
	return StreamInput{SessionID: sessionID, Messages: []Message{UserMessage(prompt)}}
}

// AgentConfig configures an Agent.
type AgentConfig struct {
	Model          ModelProvider
	Tools          []Tool
	SystemPrompt   string
	Middlewares    []Middleware
	MaxSteps       int          // default 25
	MaxConcurrency int          // max concurrent tool executions, default 10
	SessionStore   SessionStore // nil = NewMemoryStore
	depth          int          // subagent recursion depth (top 0, +1 per fork)
}

func (c AgentConfig) Depth() int { return c.depth }

type Option func(*AgentConfig)

func WithTools(tools ...Tool) Option {
	return func(c *AgentConfig) { c.Tools = append(c.Tools, tools...) }
}

func WithSystemPrompt(p string) Option {
	return func(c *AgentConfig) { c.SystemPrompt = p }
}

func WithMiddlewares(m ...Middleware) Option {
	return func(c *AgentConfig) { c.Middlewares = append(c.Middlewares, m...) }
}

func WithMaxSteps(n int) Option {
	return func(c *AgentConfig) { c.MaxSteps = n }
}

func WithSessionStore(s SessionStore) Option {
	return func(c *AgentConfig) { c.SessionStore = s }
}

func NewAgent(model ModelProvider, opts ...Option) Agent {
	cfg := AgentConfig{Model: model, MaxSteps: 25, MaxConcurrency: 10}
	for _, o := range opts {
		o(&cfg)
	}
	normalizeConfig(&cfg)
	return &agent{cfg: cfg, store: cfg.SessionStore}
}

func normalizeConfig(cfg *AgentConfig) {
	if cfg.MaxSteps <= 0 {
		cfg.MaxSteps = 25
	}
	if cfg.MaxConcurrency <= 0 {
		cfg.MaxConcurrency = 10
	}
	if cfg.SessionStore == nil {
		cfg.SessionStore = NewMemoryStore()
	}
	cfg.Tools = dedupeTools(cfg.Tools)
}

// dedupeTools drops tools whose Info().Name collides with an earlier tool, keeping the first
// registration. Built-ins are registered before MCP tools, so a built-in always wins a clash with an
// MCP tool, and the first MCP server wins over a later one. Without this, the model would be shown
// duplicate tool entries while execution silently bound the name to the last registration (the
// tool->func map keeps only one), making tool dispatch nondeterministic.
func dedupeTools(tools []Tool) []Tool {
	if len(tools) < 2 {
		return tools
	}
	seen := make(map[string]struct{}, len(tools))
	out := make([]Tool, 0, len(tools))
	for _, t := range tools {
		name := t.Info().Name
		if _, dup := seen[name]; dup {
			Warnf("duplicate tool name %q ignored (keeping the first registration)", name)
			continue
		}
		seen[name] = struct{}{}
		out = append(out, t)
	}
	return out
}

type agent struct {
	cfg       AgentConfig
	store     SessionStore           // multi-turn memory store (default MemoryStore)
	sessionMu map[string]*sync.Mutex // per-session execution lock (same session serial, different sessions concurrent)
	mu        sync.Mutex             // protects sessionMu map
}

// sessionLock returns the execution lock for the given session (created on demand).
// Stream and ClearSession for the same session are serialized so load->run->save transactions do not overlap.
func (a *agent) sessionLock(id string) *sync.Mutex {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.sessionMu == nil {
		a.sessionMu = make(map[string]*sync.Mutex)
	}
	if m, ok := a.sessionMu[id]; ok {
		return m
	}
	m := &sync.Mutex{}
	a.sessionMu[id] = m
	return m
}

// Stream starts an agent execution and returns the event stream.
//
// Session: if in.SessionID is non-empty, the session history is loaded before execution and saved afterward,
// so multi-turn REPL conversations keep context. An empty SessionID means stateless (headless mode).
//
// Concurrency safety: Stream and ClearSession for the same sessionID are serialized by a per-session lock.
// The core guarantees load->append->run->save transactions do not overlap. Different sessionIDs may run concurrently.
func (a *agent) Stream(ctx context.Context, in StreamInput) (<-chan Event, error) {
	state := &RunState{Tools: toolInfos(a.cfg.Tools)}

	// Lock the session so the entire load->run->save sequence is serialized.
	// On synchronous failure the lock is released here; on success it is released by the goroutine.
	var sessLock *sync.Mutex
	if in.SessionID != "" {
		sessLock = a.sessionLock(in.SessionID)
		sessLock.Lock()
	}
	releaseIfLocked := func() {
		if sessLock != nil {
			sessLock.Unlock()
			sessLock = nil
		}
	}
	// Until the worker goroutine launches and takes ownership of the lock, any early return OR panic
	// in the synchronous setup below must release it. Without this, a panic in a BeforeAgent
	// middleware (or the store) would leak the per-session lock and deadlock that session forever.
	handedOff := false
	defer func() {
		if !handedOff {
			releaseIfLocked()
		}
	}()

	if in.SessionID != "" {
		hist, err := a.store.Load(in.SessionID)
		if err != nil {
			return nil, fmt.Errorf("load session %q: %w", in.SessionID, err)
		}
		state.Messages = append(state.Messages, hist...)
	}
	// System prompt: only prepend if history is empty or the first message is not system, to avoid duplication each turn.
	if a.cfg.SystemPrompt != "" && (len(state.Messages) == 0 || state.Messages[0].Role != RoleSystem) {
		state.Messages = append([]Message{SystemMessage(a.cfg.SystemPrompt)}, state.Messages...)
	}
	state.Messages = append(state.Messages, in.Messages...)

	for _, m := range a.cfg.Middlewares {
		if err := m.BeforeAgent(ctx, state); err != nil {
			return nil, fmt.Errorf("before agent: %w", err)
		}
	}

	ch := make(chan Event, 8)
	go func() {
		defer close(ch)
		// Final safety net: panics in the loop, hooks, provider, or tool chain become an ErrorEvent.
		defer func() {
			if r := recover(); r != nil {
				trySendEvent(ch, ErrorEvent{Err: fmt.Errorf("agent panic recovered: %v\n%s", r, debug.Stack())})
			}
		}()
		defer releaseIfLocked()
		RunForked(ctx, a.cfg, state, ch)
		// Save session history (multi-turn memory). RunForked uses the same store as a.cfg.SessionStore.
		if in.SessionID != "" {
			if err := a.store.Save(in.SessionID, state.Messages); err != nil {
				// Persistence failure is best-effort: do not block execution, but make it diagnosable
				// (silent history loss on disk-full or permission errors is the hardest to debug).
				Warnf("session %q save failed: %v", in.SessionID, err)
			}
		}
		// AfterAgent (runs regardless of success or failure, useful for memory extraction)
		for _, m := range a.cfg.Middlewares {
			if err := m.AfterAgent(ctx, state); err != nil {
				Warnf("after agent middleware %T failed: %v", m, err)
			}
		}
	}()
	// The worker goroutine now owns the lock (released via its deferred releaseIfLocked); the
	// synchronous guard above must no longer release it.
	handedOff = true
	return ch, nil
}

// ClearSession clears the history for the given session (used by REPL /clear).
//
// Shares the per-session lock with Stream to prevent a clear/save race that could resurrect history.
func (a *agent) ClearSession(sessionID string) {
	lock := a.sessionLock(sessionID)
	lock.Lock()
	defer lock.Unlock()
	if err := a.store.Clear(sessionID); err != nil {
		Warnf("session %q clear failed: %v", sessionID, err)
	}
}

// ForkOption configures a forked (derived) agent.
type ForkOption func(*AgentConfig)

func ForkWithTools(tools ...Tool) ForkOption {
	return func(c *AgentConfig) { c.Tools = tools }
}

func ForkWithMiddlewares(mw ...Middleware) ForkOption {
	return func(c *AgentConfig) { c.Middlewares = mw }
}

func ForkWithMaxSteps(n int) ForkOption {
	return func(c *AgentConfig) { c.MaxSteps = n }
}

func ForkWithSystemPrompt(p string) ForkOption {
	return func(c *AgentConfig) { c.SystemPrompt = p }
}

func ForkWithDepth(d int) ForkOption {
	return func(c *AgentConfig) { c.depth = d }
}

// NewForkedConfig derives a child session configuration from a parent.
func NewForkedConfig(parent AgentConfig, opts ...ForkOption) AgentConfig {
	child := AgentConfig{
		Model:        parent.Model,
		SystemPrompt: parent.SystemPrompt,
		Middlewares:  append([]Middleware(nil), parent.Middlewares...),
		MaxSteps:     parent.MaxSteps,
		Tools:        nil,
		SessionStore: NewMemoryStore(),
		depth:        parent.depth + 1,
	}
	for _, o := range opts {
		o(&child)
	}
	return child
}

// RunForked runs the agent loop on a derived configuration.
func RunForked(ctx context.Context, cfg AgentConfig, state *RunState, ch chan<- Event) {
	normalizeConfig(&cfg)
	if len(state.Tools) == 0 && len(cfg.Tools) > 0 {
		state.Tools = toolInfos(cfg.Tools)
	}
	a := &agent{cfg: cfg, store: cfg.SessionStore}
	a.runLoop(ctx, state, ch)
}
