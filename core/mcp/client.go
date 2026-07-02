package mcp

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// ErrClientClosed is returned when ListTools or CallTool is invoked after Close.
var ErrClientClosed = errors.New("mcp client closed")

// mcpHandshakeTimeout is the client timeout for connect (initialize) and list-tools.
// Prevents hanging MCP servers (e.g. an unresponsive TCP endpoint) from blocking agent startup
// indefinitely. CallTool uses the caller's ctx -- tool execution duration is variable, so no fixed
// upper limit is enforced there.
const mcpHandshakeTimeout = 30 * time.Second

// ServerConfig describes the connection config for one MCP server. It is a plain value type with no
// serialization tags: the config package owns parsing and converts into this at the boundary.
type ServerConfig struct {
	Name    string            // display name (logs / errors)
	Type    string            // "stdio" / "http" / "sse", default stdio
	Cmd     string            // stdio: executable
	Args    []string          // stdio: command arguments
	Env     []string          // stdio: environment variables (KEY=VAL)
	Cwd     string            // stdio: working directory for the spawned process (empty = inherit)
	URL     string            // http/sse: server URL
	Headers map[string]string // http/sse: extra HTTP headers (e.g. Authorization) added to every request
	Tools   []ToolOverride    // capability overrides per tool name (the MCP protocol does not carry these)
}

// ToolOverride declares capability overrides per MCP tool name (executeTools concurrency depends on this).
type ToolOverride struct {
	Name            string
	ReadOnly        bool
	ConcurrencySafe bool
	MaxResultChars  int
}

// Client wraps an MCP server session + adapted tools.
//
// Concurrency: mu guards the session pointer so that a reconnect (triggered when a call detects a
// dropped connection) can atomically swap in a fresh session while other goroutines are calling
// tools. Calls read the session under a short lock, then invoke it without holding mu (the SDK
// session multiplexes concurrent requests over one connection).
type Client struct {
	name      string
	mu        sync.Mutex
	session   *mcp.ClientSession
	overrides map[string]ToolOverride // tool name -> override

	// cfg is retained so a broken connection can be re-established transparently. canReconnect is
	// false for in-process test sessions (no real transport to rebuild).
	cfg          ServerConfig
	canReconnect bool
	closed       bool

	// closer is optional extra cleanup (e.g. the paired in-process server session in tests); nil in
	// production. Invoked by Close after the session is closed.
	closer func() error
}

// newWithSession constructs a Client from an already-connected session (used by tests: an in-process
// server). The caller is responsible for having completed the connect handshake. closer, if non-nil,
// is released by Close.
func newWithSession(name string, session *mcp.ClientSession, overrides map[string]ToolOverride, closer func() error) *Client {
	return &Client{name: name, session: session, overrides: overrides, closer: closer}
}

// buildTransport constructs the MCP transport for the config. HTTP-family transports inject any
// configured headers via a wrapping RoundTripper; stdio inherits the parent environment plus the
// configured overrides and honors an optional working directory.
func buildTransport(cfg ServerConfig) (mcp.Transport, error) {
	typ := cfg.Type
	if typ == "" {
		typ = "stdio"
	}
	switch typ {
	case "stdio":
		if cfg.Cmd == "" {
			return nil, fmt.Errorf("mcp stdio server %q: empty command", cfg.Name)
		}
		cmd := exec.Command(cfg.Cmd, cfg.Args...)
		// Inherit the parent environment and apply the configured overrides. Setting only cfg.Env would
		// wipe PATH and other essentials, breaking the spawned server.
		cmd.Env = append(os.Environ(), cfg.Env...)
		if cfg.Cwd != "" {
			cmd.Dir = cfg.Cwd
		}
		return &mcp.CommandTransport{Command: cmd}, nil
	case "http", "streamable", "streamable-http":
		return &mcp.StreamableClientTransport{Endpoint: cfg.URL, HTTPClient: httpClientWithHeaders(cfg.Headers)}, nil
	case "sse":
		return &mcp.SSEClientTransport{Endpoint: cfg.URL, HTTPClient: httpClientWithHeaders(cfg.Headers)}, nil
	default:
		return nil, fmt.Errorf("unknown mcp server type %q", typ)
	}
}

// httpClientWithHeaders returns an *http.Client that injects the given headers on every request, or
// nil when there are none (the SDK then uses its default client).
func httpClientWithHeaders(headers map[string]string) *http.Client {
	if len(headers) == 0 {
		return nil
	}
	return &http.Client{Transport: &headerRoundTripper{headers: headers, base: http.DefaultTransport}}
}

// headerRoundTripper adds a fixed set of headers to each outgoing request before delegating to base.
type headerRoundTripper struct {
	headers map[string]string
	base    http.RoundTripper
}

func (h *headerRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	// Clone so the shared header map is not mutated and the original request is untouched.
	clone := req.Clone(req.Context())
	for k, v := range h.headers {
		clone.Header.Set(k, v)
	}
	return h.base.RoundTrip(clone)
}

// connectSession builds the transport and completes the MCP initialize handshake, returning a live
// session. A timeout guards against a hanging server blocking startup or a reconnect.
func connectSession(ctx context.Context, cfg ServerConfig) (*mcp.ClientSession, error) {
	transport, err := buildTransport(cfg)
	if err != nil {
		return nil, err
	}
	impl := &mcp.Implementation{Name: "creator-agent", Version: "0.1"}
	mc := mcp.NewClient(impl, nil)
	connectCtx, cancel := context.WithTimeout(ctx, mcpHandshakeTimeout)
	defer cancel()
	cs, err := mc.Connect(connectCtx, transport, nil)
	if err != nil {
		return nil, fmt.Errorf("mcp connect %q: %w", cfg.Name, err)
	}
	return cs, nil
}

// NewClient establishes a transport connection and completes the MCP initialize handshake (the SDK
// runs the handshake inside Connect). Caller is responsible for Close.
//
// Empty type defaults to "stdio".
func NewClient(ctx context.Context, cfg ServerConfig) (*Client, error) {
	cs, err := connectSession(ctx, cfg)
	if err != nil {
		return nil, err
	}
	overrides := make(map[string]ToolOverride, len(cfg.Tools))
	for _, o := range cfg.Tools {
		overrides[o.Name] = o
	}
	return &Client{name: cfg.Name, session: cs, overrides: overrides, cfg: cfg, canReconnect: true}, nil
}

// Close closes the session (and the optional extra resources in tests). Safe to call multiple times.
func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed = true
	var first error
	if c.session != nil {
		first = c.session.Close()
		c.session = nil
	}
	if c.closer != nil {
		if err := c.closer(); first == nil {
			first = err
		}
		c.closer = nil
	}
	return first
}

// currentSession returns the live session pointer under a short lock, or ErrClientClosed.
func (c *Client) currentSession() (*mcp.ClientSession, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed || c.session == nil {
		return nil, ErrClientClosed
	}
	return c.session, nil
}

// reconnect re-establishes the session after a dropped connection. broken is the session pointer the
// caller observed failing; if another goroutine already swapped in a fresh session, that one is
// returned instead of connecting again (dampens reconnect storms under concurrent calls).
func (c *Client) reconnect(ctx context.Context, broken *mcp.ClientSession) (*mcp.ClientSession, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed || !c.canReconnect {
		return nil, ErrClientClosed
	}
	if c.session != nil && c.session != broken {
		return c.session, nil // another goroutine already reconnected
	}
	if c.session != nil {
		_ = c.session.Close()
		c.session = nil
	}
	cs, err := connectSession(ctx, c.cfg)
	if err != nil {
		return nil, err
	}
	c.session = cs
	return cs, nil
}

// isConnectionError heuristically reports whether err indicates a dropped/closed transport (as
// opposed to a normal tool-level error), so callers know a reconnect is worth attempting. The MCP
// SDK does not expose a typed sentinel for this, so string matching on the common underlying causes
// is used; a false positive merely triggers one extra reconnect attempt.
func isConnectionError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, ErrClientClosed) {
		return true
	}
	msg := strings.ToLower(err.Error())
	for _, kw := range []string{
		"eof", "broken pipe", "connection reset", "connection refused",
		"connection closed", "session closed", "use of closed", "transport closed",
		"process exited", "file already closed", "no such process",
	} {
		if strings.Contains(msg, kw) {
			return true
		}
	}
	return false
}

// ListTools fetches the tool list from the server (*mcp.Tool). Timeout guard.
func (c *Client) ListTools(ctx context.Context) ([]*mcp.Tool, error) {
	session, err := c.currentSession()
	if err != nil {
		return nil, err
	}
	listCtx, listCancel := context.WithTimeout(ctx, mcpHandshakeTimeout)
	defer listCancel()
	resp, err := session.ListTools(listCtx, nil)
	if err != nil {
		return nil, fmt.Errorf("list tools: %w", err)
	}
	return resp.Tools, nil
}

// CallTool invokes a tool. On a dropped connection it transparently reconnects once and retries, so
// a server restart (or an idle-timed-out stdio process) does not surface a one-off failure to the model.
func (c *Client) CallTool(ctx context.Context, name string, args map[string]any) (*mcp.CallToolResult, error) {
	session, err := c.currentSession()
	if err != nil {
		return nil, fmt.Errorf("call tool %q: %w", name, err)
	}
	res, err := session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil && c.canReconnect && ctx.Err() == nil && isConnectionError(err) {
		if fresh, rerr := c.reconnect(ctx, session); rerr == nil {
			res, err = fresh.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
		}
	}
	if err != nil {
		return nil, fmt.Errorf("call tool %q: %w", name, err)
	}
	return res, nil
}

// override looks up the capability override for a tool.
func (c *Client) override(name string) (ToolOverride, bool) {
	o, ok := c.overrides[name]
	return o, ok
}
