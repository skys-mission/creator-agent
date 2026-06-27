// Package mcp is the MCP (Model Context Protocol) client adapter layer.
//
// Position (anti-corruption layer boundary): a standalone adapter package alongside core/adapters/openai/.
// Only imports core + mark3labs/mcp-go (does not depend on the LLM anti-corruption layer). Adapts MCP server tools into core.Tool.
//
// capability: the MCP protocol does not carry ReadOnly/ConcurrencySafe, so defaults are fail-closed
// (not read-only, not concurrency-safe); config can declare per-tool overrides.
package mcp

import (
	"context"
	"fmt"
	"time"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
)

// mcpHandshakeTimeout is the client timeout for Initialize/ListTools.
// Prevents hanging MCP servers (e.g. TCP unresponsive) from blocking agent startup indefinitely.
// CallTool uses the caller's ctx -- tool execution duration is variable, so no fixed upper limit is enforced.
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

// Client wraps an MCP server connection + adapted tools.
type Client struct {
	name      string
	mc        *client.Client
	overrides map[string]ToolOverride // tool name -> override
}

// newWithClient constructs from an already-created underlying client (for testing: in-process server).
// Does not call Initialize (caller is responsible).
func newWithClient(name string, mc *client.Client, overrides map[string]ToolOverride) *Client {
	return &Client{name: name, mc: mc, overrides: overrides}
}

// NewClient establishes a connection + performs the initialize handshake. Caller is responsible for Close.
//
// Empty type defaults to "stdio".
func NewClient(ctx context.Context, cfg ServerConfig) (*Client, error) {
	typ := cfg.Type
	if typ == "" {
		typ = "stdio"
	}
	var mc *client.Client
	var err error
	switch typ {
	case "stdio":
		mc, err = client.NewStdioMCPClient(cfg.Cmd, cfg.Env, cfg.Args...)
	case "http", "streamable", "streamable-http":
		mc, err = client.NewStreamableHttpClient(cfg.URL)
	case "sse":
		mc, err = client.NewSSEMCPClient(cfg.URL)
	default:
		return nil, fmt.Errorf("unknown mcp server type %q", typ)
	}
	if err != nil {
		return nil, fmt.Errorf("create mcp client: %w", err)
	}

	// initialize handshake (timeout guard against hanging servers blocking startup)
	req := mcp.InitializeRequest{}
	req.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	req.Params.ClientInfo = mcp.Implementation{Name: "creator-agent", Version: "0.1"}
	initCtx, initCancel := context.WithTimeout(ctx, mcpHandshakeTimeout)
	defer initCancel()
	if _, err := mc.Initialize(initCtx, req); err != nil {
		_ = mc.Close()
		return nil, fmt.Errorf("mcp initialize: %w", err)
	}

	overrides := make(map[string]ToolOverride, len(cfg.Tools))
	for _, o := range cfg.Tools {
		overrides[o.Name] = o
	}
	return &Client{name: cfg.Name, mc: mc, overrides: overrides}, nil
}

// Close closes the underlying connection.
func (c *Client) Close() error {
	if c.mc == nil {
		return nil
	}
	return c.mc.Close()
}

// ListTools fetches the tool list from the server (mcp.Tool). Timeout guard.
func (c *Client) ListTools(ctx context.Context) ([]mcp.Tool, error) {
	listCtx, listCancel := context.WithTimeout(ctx, mcpHandshakeTimeout)
	defer listCancel()
	resp, err := c.mc.ListTools(listCtx, mcp.ListToolsRequest{})
	if err != nil {
		return nil, fmt.Errorf("list tools: %w", err)
	}
	return resp.Tools, nil
}

// CallTool invokes a tool.
func (c *Client) CallTool(ctx context.Context, name string, args map[string]any) (*mcp.CallToolResult, error) {
	req := mcp.CallToolRequest{}
	req.Params.Name = name
	req.Params.Arguments = args
	res, err := c.mc.CallTool(ctx, req)
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
