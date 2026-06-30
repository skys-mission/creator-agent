package mcp

// manager.go provides a per-server MCP lifecycle manager: it tracks each configured server's
// connection, its adapted tools, an enabled flag, and any connection error. Callers (the TUI
// /mcps picker) can enable/disable individual servers at runtime — disabling filters a server's
// tools out of the active set without dropping the connection (cheap), while enabling a
// not-yet-connected server performs the full NewClient+ListTools handshake (reconnect).
//
// This replaces the flat LoadAll result with a structure that remembers which tools came from
// which server, which is what runtime enable/disable requires.

import (
	"context"
	"fmt"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/skys-mission/creator-agent/core"
)

// ServerStatus is a snapshot of one managed server's state, for display in the /mcps picker.
type ServerStatus struct {
	Name       string // server display name
	Enabled    bool   // currently contributing tools (server-level)
	Connected  bool   // has a live client + tools loaded
	Connecting bool   // a connect/reconnect handshake is in flight (e.g. after an enable request)
	ToolCount  int    // number of tools contributed
	LastErr    string // last connection/list error (empty when healthy)
	Configured bool   // appears in config (always true for Manager-built entries)
	// Tools lists the server's individual tools with their per-tool enabled flag, for the /mcps
	// picker's per-tool rows. Populated only when the server is connected (has tools).
	Tools []ToolStatus
}

// ToolStatus is one tool's name + per-tool enabled flag within a server's status snapshot.
type ToolStatus struct {
	Name    string
	Enabled bool // false = individually disabled (hidden from the agent's tool set)
}

// managedServer is the per-server state held under Manager.mu.
type managedServer struct {
	config        ServerConfig
	client        *Client // nil when never connected or closed
	tools         []core.Tool
	enabled       bool
	connecting    bool // a connect handshake is in flight (SetEnabled released mu for I/O)
	lastErr       error
	disabledTools map[string]bool // tool names individually disabled (hidden from EnabledTools)
}

// Manager owns the lifecycle of all configured MCP servers and exposes a runtime-toggleable
// enabled set. All methods are safe for concurrent use; the mutex serializes state reads/writes.
// The connect handshake runs OUTSIDE the lock (SetEnabled releases mu during network I/O) so that
// Status() can report a "connecting" state and other servers' toggles are not serialized behind a
// slow one.
type Manager struct {
	mu      sync.Mutex
	ctx     context.Context
	servers map[string]*managedServer // keyed by config name (first wins on duplicates)
	order   []string                  // config order, for stable display

	// newClient builds a Client for a config. Defaults to NewClient (real transport); tests inject
	// an in-process factory to avoid spawning subprocesses.
	newClient func(ctx context.Context, cfg ServerConfig) (*Client, error)
}

// clientFactory is the type of the per-server connect function, overridable for tests.
type clientFactory = func(ctx context.Context, cfg ServerConfig) (*Client, error)

// NewManager connects every configured server (best-effort: failures are recorded per server and
// do not abort the others). Successfully connected servers start enabled; failed ones start
// disabled with lastErr set. The returned Manager's Close must be called at process exit.
func NewManager(ctx context.Context, configs []ServerConfig) *Manager {
	return newManagerWithFactory(ctx, configs, NewClient)
}

// newManagerWithFactory is the testable constructor that accepts a custom client factory.
func newManagerWithFactory(ctx context.Context, configs []ServerConfig, factory clientFactory) *Manager {
	m := &Manager{
		ctx:       ctx,
		servers:   make(map[string]*managedServer, len(configs)),
		newClient: factory,
	}
	for _, cfg := range configs {
		// Skip duplicate names (keep the first config, in declaration order).
		if _, exists := m.servers[cfg.Name]; exists {
			continue
		}
		ms := &managedServer{config: cfg}
		m.servers[cfg.Name] = ms
		m.order = append(m.order, cfg.Name)
		// Best-effort initial connect: on failure the server stays present (so the picker can show
		// it + its error) but disabled.
		if c, err := factory(ctx, cfg); err != nil {
			ms.lastErr = err
			ms.enabled = false
		} else {
			ms.client = c
			if tools, err := c.ListTools(ctx); err != nil {
				_ = c.Close()
				ms.client = nil
				ms.lastErr = fmt.Errorf("list tools: %w", err)
				ms.enabled = false
			} else {
				ms.tools = c.AdaptTools(tools)
				ms.enabled = true
			}
		}
	}
	return m
}

// EnabledTools returns the flattened tools of all currently enabled servers, excluding any
// individually-disabled tools. The slice is freshly allocated so callers may retain it without
// holding the lock.
func (m *Manager) EnabledTools() []core.Tool {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []core.Tool
	for _, name := range m.order {
		ms := m.servers[name]
		if ms == nil || !ms.enabled {
			continue
		}
		for _, t := range ms.tools {
			if ms.disabledTools[t.Info().Name] {
				continue
			}
			out = append(out, t)
		}
	}
	return out
}

// SetEnabled toggles a server's contribution to the active tool set.
//
// Disabling is cheap: the client connection is retained and only the enabled flag flips, so the
// tools are filtered out on the next EnabledTools call.
//
// Enabling a server that is already connected just flips the flag; enabling a server with no live
// client performs a full connect+list handshake. The handshake runs OUTSIDE m.mu so that Status()
// can report a "connecting" state (and other servers' toggles are not serialized behind a slow
// connect). The connecting flag is set before the I/O and cleared after, both under the lock.
//
// The ctx carries cancellation: aborting it (e.g. the user pressing Esc in the /mcps picker)
// interrupts an in-flight handshake. Returns an error only when a connect is required and fails or
// is cancelled — in that case the server stays disabled and its lastErr is updated.
func (m *Manager) SetEnabled(ctx context.Context, name string, enabled bool) error {
	// Fast path / state setup under the lock.
	cfg, needsConnect := func() (ServerConfig, bool) {
		m.mu.Lock()
		defer m.mu.Unlock()
		ms, ok := m.servers[name]
		if !ok {
			return ServerConfig{}, false // unknown — signaled via a sentinel below
		}
		if !enabled {
			ms.enabled = false
			return ServerConfig{}, false
		}
		// Enable: if already connected with tools, just flip the flag (no I/O).
		if ms.client != nil && len(ms.tools) > 0 {
			ms.enabled = true
			ms.lastErr = nil
			return ServerConfig{}, false
		}
		// Need a connect handshake. Mark connecting + optimistically off until success, then release
		// the lock for the network I/O.
		ms.connecting = true
		ms.enabled = false
		return ms.config, true
	}()
	if !needsConnect {
		// Distinguish "unknown server" from "no I/O needed": re-check existence. An unknown name is an
		// error; a no-I/O enable already succeeded.
		m.mu.Lock()
		_, ok := m.servers[name]
		m.mu.Unlock()
		if !ok {
			return fmt.Errorf("unknown mcp server %q", name)
		}
		return nil
	}

	// Slow path: connect + list tools WITHOUT holding m.mu (so Status() observes connecting=true and
	// other toggles are not blocked). The ctx carries cancellation.
	c, err := m.newClient(ctx, cfg)
	if err != nil {
		m.applyConnectResult(name, nil, nil, fmt.Errorf("connect mcp server %q: %w", name, err))
		return fmt.Errorf("connect mcp server %q: %w", name, err)
	}
	tools, lerr := c.ListTools(ctx)
	if lerr != nil {
		_ = c.Close()
		m.applyConnectResult(name, nil, nil, fmt.Errorf("list tools for %q: %w", name, lerr))
		return fmt.Errorf("list tools for %q: %w", name, lerr)
	}
	m.applyConnectResult(name, c, tools, nil)
	return nil
}

// applyConnectResult finalizes a connect handshake under m.mu: clears connecting, and on success
// installs the client+tools (adapted to core.Tool) + enables the server; on failure records lastErr
// + keeps it disabled. rawTools is the MCP-SDK tool list from ListTools; the client adapts it.
func (m *Manager) applyConnectResult(name string, c *Client, rawTools []*mcp.Tool, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	ms, ok := m.servers[name]
	if !ok {
		if c != nil {
			_ = c.Close()
		}
		return
	}
	ms.connecting = false
	if err != nil {
		ms.lastErr = err
		ms.enabled = false
		return
	}
	// Replace the client + tools (close any prior client defensively).
	if ms.client != nil {
		_ = ms.client.Close()
	}
	ms.client = c
	ms.tools = c.AdaptTools(rawTools)
	ms.enabled = true
	ms.lastErr = nil
}

// SetToolEnabled toggles a single tool's contribution within a server's active set, independent of
// the server-level enabled flag. Disabling a tool filters it out of EnabledTools (so the agent no
// longer sees/calls it) without dropping the connection; enabling re-includes it. The per-tool flag
// is preserved across a server-level disable/enable cycle. Unknown server or tool names are an
// error; a server that is not connected (no tools) is also an error.
func (m *Manager) SetToolEnabled(_ context.Context, server, tool string, enabled bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	ms, ok := m.servers[server]
	if !ok {
		return fmt.Errorf("unknown mcp server %q", server)
	}
	if len(ms.tools) == 0 {
		return fmt.Errorf("mcp server %q has no tools loaded", server)
	}
	// Validate the tool name exists (defensive: ignore stray names silently would hide typos).
	known := false
	for _, t := range ms.tools {
		if t.Info().Name == tool {
			known = true
			break
		}
	}
	if !known {
		return fmt.Errorf("unknown tool %q on mcp server %q", tool, server)
	}
	if ms.disabledTools == nil {
		ms.disabledTools = make(map[string]bool)
	}
	if enabled {
		delete(ms.disabledTools, tool)
	} else {
		ms.disabledTools[tool] = true
	}
	return nil
}

// Status returns a snapshot of every configured server's state, in config order.
func (m *Manager) Status() []ServerStatus {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]ServerStatus, 0, len(m.order))
	for _, name := range m.order {
		ms := m.servers[name]
		st := ServerStatus{
			Name:       name,
			Enabled:    ms.enabled,
			Connected:  ms.client != nil && len(ms.tools) > 0,
			Connecting: ms.connecting,
			ToolCount:  len(ms.tools),
			Configured: true,
		}
		if ms.lastErr != nil {
			st.LastErr = ms.lastErr.Error()
		}
		// Per-tool enabled flags for the picker's per-tool rows.
		if len(ms.tools) > 0 {
			st.Tools = make([]ToolStatus, 0, len(ms.tools))
			for _, t := range ms.tools {
				st.Tools = append(st.Tools, ToolStatus{
					Name:    t.Info().Name,
					Enabled: !ms.disabledTools[t.Info().Name],
				})
			}
		}
		out = append(out, st)
	}
	return out
}

// Close disconnects every server. Safe to call multiple times.
func (m *Manager) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, ms := range m.servers {
		if ms.client != nil {
			_ = ms.client.Close()
			ms.client = nil
		}
	}
}

// HasServers reports whether any MCP server is configured. Used by the TUI to decide whether to
// bother opening the picker.
func (m *Manager) HasServers() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.order) > 0
}
