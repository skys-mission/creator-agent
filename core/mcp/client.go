package mcp

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
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
	Name  string         // display name (logs / errors)
	Type  string         // "stdio" / "http" / "sse", default stdio
	Cmd   string         // stdio: executable
	Args  []string       // stdio: command arguments
	Env   []string       // stdio: environment variables (KEY=VAL)
	URL   string         // http/sse: server URL
	Tools []ToolOverride // capability overrides per tool name (the MCP protocol does not carry these)
}

// ToolOverride declares capability overrides per MCP tool name (executeTools concurrency depends on this).
type ToolOverride struct {
	Name            string
	ReadOnly        bool
	ConcurrencySafe bool
	MaxResultChars  int
}

// Client wraps an MCP server session + adapted tools.
type Client struct {
	name      string
	session   *mcp.ClientSession
	overrides map[string]ToolOverride // tool name -> override
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

// NewClient establishes a transport connection and completes the MCP initialize handshake (the SDK
// runs the handshake inside Connect). Caller is responsible for Close.
//
// Empty type defaults to "stdio".
func NewClient(ctx context.Context, cfg ServerConfig) (*Client, error) {
	typ := cfg.Type
	if typ == "" {
		typ = "stdio"
	}
	var transport mcp.Transport
	switch typ {
	case "stdio":
		if cfg.Cmd == "" {
			return nil, fmt.Errorf("mcp stdio server %q: empty command", cfg.Name)
		}
		cmd := exec.Command(cfg.Cmd, cfg.Args...)
		// Inherit the parent environment and apply the configured overrides. Setting only cfg.Env would
		// wipe PATH and other essentials, breaking the spawned server.
		cmd.Env = append(os.Environ(), cfg.Env...)
		transport = &mcp.CommandTransport{Command: cmd}
	case "http", "streamable", "streamable-http":
		transport = &mcp.StreamableClientTransport{Endpoint: cfg.URL}
	case "sse":
		transport = &mcp.SSEClientTransport{Endpoint: cfg.URL}
	default:
		return nil, fmt.Errorf("unknown mcp server type %q", typ)
	}

	impl := &mcp.Implementation{Name: "creator-agent", Version: "0.1"}
	mc := mcp.NewClient(impl, nil)

	// Connect performs the initialize handshake. Timeout guard against hanging servers blocking startup.
	connectCtx, cancel := context.WithTimeout(ctx, mcpHandshakeTimeout)
	defer cancel()
	cs, err := mc.Connect(connectCtx, transport, nil)
	if err != nil {
		return nil, fmt.Errorf("mcp connect %q: %w", cfg.Name, err)
	}

	overrides := make(map[string]ToolOverride, len(cfg.Tools))
	for _, o := range cfg.Tools {
		overrides[o.Name] = o
	}
	return &Client{name: cfg.Name, session: cs, overrides: overrides}, nil
}

// Close closes the session (and the optional extra resources in tests). Safe to call multiple times.
func (c *Client) Close() error {
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

func (c *Client) requireSession() (*mcp.ClientSession, error) {
	if c.session == nil {
		return nil, ErrClientClosed
	}
	return c.session, nil
}

// ListTools fetches the tool list from the server (*mcp.Tool). Timeout guard.
func (c *Client) ListTools(ctx context.Context) ([]*mcp.Tool, error) {
	session, err := c.requireSession()
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

// CallTool invokes a tool.
func (c *Client) CallTool(ctx context.Context, name string, args map[string]any) (*mcp.CallToolResult, error) {
	session, err := c.requireSession()
	if err != nil {
		return nil, fmt.Errorf("call tool %q: %w", name, err)
	}
	res, err := session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
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
