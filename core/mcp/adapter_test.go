package mcp

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/skys-mission/creator-agent/core"
)

// startTestServer starts an in-memory MCP server, registers an echo tool, and returns a connected client.
// Tool behavior: returns args.echo as-is.
func startTestServer(t *testing.T, overrides map[string]ToolOverride) *Client {
	t.Helper()
	srv := server.NewMCPServer("test", "1.0.0")
	srv.AddTool(
		mcp.NewTool("echo",
			mcp.WithDescription("echo back input"),
			mcp.WithString("echo", mcp.Description("text to echo")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			echo := ""
			if args, ok := req.Params.Arguments.(map[string]any); ok {
				if v, ok := args["echo"].(string); ok {
					echo = v
				}
			}
			return &mcp.CallToolResult{
				Content: []mcp.Content{mcp.NewTextContent("echoed: " + echo)},
			}, nil
		},
	)

	mc, err := client.NewInProcessClient(srv)
	if err != nil {
		t.Fatalf("in-process client: %v", err)
	}
	req := mcp.InitializeRequest{}
	req.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	req.Params.ClientInfo = mcp.Implementation{Name: "test"}
	if _, err := mc.Initialize(context.Background(), req); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	return newWithClient("test", mc, overrides)
}

// Verify: ListTools fetches the registered echo tool.
func TestListTools(t *testing.T) {
	c := startTestServer(t, nil)
	defer c.Close()
	tools, err := c.ListTools(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != 1 || tools[0].Name != "echo" {
		t.Errorf("tools = %+v, want echo", tools)
	}
}

// Verify: AdaptTools produces core.Tool with correct Info fields.
func TestAdaptToolInfo(t *testing.T) {
	c := startTestServer(t, nil)
	defer c.Close()
	tools, _ := c.ListTools(context.Background())
	coreTools := c.AdaptTools(tools)
	if len(coreTools) != 1 {
		t.Fatalf("coreTools = %d, want 1", len(coreTools))
	}
	info := coreTools[0].Info()
	if info.Name != "echo" {
		t.Errorf("name = %q", info.Name)
	}
	if info.Description != "echo back input" {
		t.Errorf("desc = %q", info.Description)
	}
	// default fail-closed
	if info.ReadOnly || info.ConcurrencySafe {
		t.Errorf("capability not fail-closed: RO=%v CS=%v", info.ReadOnly, info.ConcurrencySafe)
	}
	// InputSchema non-empty
	if len(info.InputSchema) == 0 {
		t.Error("InputSchema empty")
	}
}

// Verify: capability override takes effect.
func TestAdaptToolOverride(t *testing.T) {
	c := startTestServer(t, map[string]ToolOverride{
		"echo": {ReadOnly: true, ConcurrencySafe: true, MaxResultChars: 5000},
	})
	defer c.Close()
	tools, _ := c.ListTools(context.Background())
	coreTools := c.AdaptTools(tools)
	info := coreTools[0].Info()
	if !info.ReadOnly || !info.ConcurrencySafe {
		t.Errorf("override not applied: RO=%v CS=%v", info.ReadOnly, info.ConcurrencySafe)
	}
	if info.MaxResultChars != 5000 {
		t.Errorf("MaxResultChars = %d, want 5000", info.MaxResultChars)
	}
}

// Verify: Exec calls the tool and returns the result.
func TestAdaptToolExec(t *testing.T) {
	c := startTestServer(t, nil)
	defer c.Close()
	tools, _ := c.ListTools(context.Background())
	coreTools := c.AdaptTools(tools)

	res, err := coreTools[0].Exec(context.Background(), json.RawMessage(`{"echo":"hello"}`))
	if err != nil {
		t.Fatal(err)
	}
	if res.Content != "echoed: hello" {
		t.Errorf("content = %q, want 'echoed: hello'", res.Content)
	}
	if res.IsError {
		t.Error("unexpected IsError")
	}
}

// Verify: Exec with empty args does not panic (no-parameter tools).
func TestAdaptToolExecEmptyInput(t *testing.T) {
	c := startTestServer(t, nil)
	defer c.Close()
	tools, _ := c.ListTools(context.Background())
	coreTools := c.AdaptTools(tools)
	res, err := coreTools[0].Exec(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Content != "echoed: " {
		t.Errorf("content = %q, want 'echoed: '", res.Content)
	}
}

// Verify: malformed JSON input is reported locally instead of being silently converted to empty args.
func TestAdaptToolExecInvalidInput(t *testing.T) {
	c := startTestServer(t, nil)
	defer c.Close()
	tools, _ := c.ListTools(context.Background())
	coreTools := c.AdaptTools(tools)

	res, err := coreTools[0].Exec(context.Background(), json.RawMessage(`{"echo":`))
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError || !strings.Contains(res.Content, "invalid input") {
		t.Errorf("invalid input should return tool error, got %+v", res)
	}
}

// ===== extractContent =====

func TestExtractContentText(t *testing.T) {
	res := &mcp.CallToolResult{
		Content: []mcp.Content{
			mcp.NewTextContent("line1"),
			mcp.NewTextContent("line2"),
		},
	}
	got := extractContent(res)
	if got != "line1\nline2" {
		t.Errorf("extract = %q", got)
	}
}

func TestExtractContentEmpty(t *testing.T) {
	if got := extractContent(nil); got != "" {
		t.Errorf("nil = %q", got)
	}
	if got := extractContent(&mcp.CallToolResult{}); got != "" {
		t.Errorf("empty = %q", got)
	}
}

func TestExtractContentMixed(t *testing.T) {
	// Only test text concatenation (image/audio constructors omitted, non-core path)
	res := &mcp.CallToolResult{
		Content: []mcp.Content{
			mcp.NewTextContent("a"),
			mcp.NewTextContent("b"),
			mcp.NewTextContent("c"),
		},
	}
	got := extractContent(res)
	if got != "a\nb\nc" {
		t.Errorf("extract = %q, want 'a\\nb\\nc'", got)
	}
}

// TestExtractContentImage covers the ImageContent placeholder branch (multimedia V2 placeholder).
func TestExtractContentImage(t *testing.T) {
	res := &mcp.CallToolResult{
		Content: []mcp.Content{
			mcp.ImageContent{Type: "image", Data: "base64data", MIMEType: "image/png"},
		},
	}
	got := extractContent(res)
	if !strings.Contains(got, "[image: image/png") || !strings.Contains(got, "bytes]") {
		t.Errorf("image placeholder missing, got %q", got)
	}
}

// TestExtractContentAudio covers the AudioContent placeholder branch.
func TestExtractContentAudio(t *testing.T) {
	res := &mcp.CallToolResult{
		Content: []mcp.Content{
			mcp.AudioContent{Type: "audio", Data: "base64data", MIMEType: "audio/wav"},
		},
	}
	got := extractContent(res)
	if !strings.Contains(got, "[audio: audio/wav") {
		t.Errorf("audio placeholder missing, got %q", got)
	}
}

// TestExtractContentUnknownType covers the unknown Content type JSON fallback branch.
func TestExtractContentUnknownType(t *testing.T) {
	// Use a Content that does not implement a known interface (custom type triggers default JSON fallback)
	type weird struct {
		mcp.TextContent // embedded but keeps its own type name, triggering default JSON fallback
		Foo             string
	}
	res := &mcp.CallToolResult{
		Content: []mcp.Content{weird{Foo: "bar"}},
	}
	got := extractContent(res)
	// default branch will JSON marshal, should contain Foo field
	if !strings.Contains(got, "bar") {
		t.Errorf("unknown type JSON fallback missing content, got %q", got)
	}
}

// ===== NewClient error paths (no real server needed) =====

// TestNewClientUnknownType covers the unknown type error branch.
func TestNewClientUnknownType(t *testing.T) {
	_, err := NewClient(context.Background(), ServerConfig{
		Name: "bad",
		Type: "bogus-transport",
	})
	if err == nil {
		t.Fatal("unknown type should error")
	}
	if !strings.Contains(err.Error(), "unknown mcp server type") {
		t.Errorf("error should mention unknown type, got: %v", err)
	}
}

// TestNewClientStdioBadCmd covers stdio startup with a nonexistent command -> error (no panic, no hang).
func TestNewClientStdioBadCmd(t *testing.T) {
	_, err := NewClient(context.Background(), ServerConfig{
		Name: "missing",
		Type: "stdio",
		Cmd:  "/nonexistent/mcp-server-binary-xyz",
		Args: []string{},
	})
	if err == nil {
		t.Fatal("bad command should error")
	}
}

// ===== LoadAll =====

// TestLoadAllEmpty: empty config -> nil tools + nil error + callable close.
func TestLoadAllEmpty(t *testing.T) {
	tools, closeFn, err := LoadAll(context.Background(), nil)
	if err != nil {
		t.Errorf("empty config should not error, got %v", err)
	}
	if len(tools) != 0 {
		t.Errorf("expected 0 tools, got %d", len(tools))
	}
	// close should be callable without panic
	closeFn()
}

// TestLoadAllCollectsError: one bad server -> empty tools + error (best-effort), close callable.
func TestLoadAllCollectsError(t *testing.T) {
	tools, closeFn, err := LoadAll(context.Background(), []ServerConfig{
		{Name: "bad", Type: "bogus-transport"},
	})
	if err == nil {
		t.Error("bad server should produce error")
	}
	if !strings.Contains(err.Error(), "bad") {
		t.Errorf("error should name the failing server, got: %v", err)
	}
	if len(tools) != 0 {
		t.Errorf("expected 0 tools from bad server, got %d", len(tools))
	}
	closeFn() // even if all fail, close should not panic
}

// ===== core.Tool interface check (compile-time) =====

var _ core.Tool = (*adaptedTool)(nil)

// ===== Real transport connection paths via NewClient =====

// TestNewClientHTTPStreamableConnect exercises the HTTP/streamable transport branch of NewClient
// end-to-end: it starts a real streamable-http MCP server (with an echo tool) on an httptest.Server,
// connects via NewClient(type=http), and verifies Initialize + ListTools + CallTool over the real
// transport. This closes the gap that only the error path (TestNewClientStdioBadCmd) and the
// in-process path (startTestServer) were covered.
func TestNewClientHTTPStreamableConnect(t *testing.T) {
	srv := server.NewMCPServer("http-test", "1.0.0")
	srv.AddTool(
		mcp.NewTool("echo", mcp.WithDescription("echo back input")),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return &mcp.CallToolResult{Content: []mcp.Content{mcp.NewTextContent("hi from http")}}, nil
		},
	)
	// Serve the streamable-http MCP server on a test HTTP server. The handler serves at /mcp by
	// default; we point the client at the full URL.
	httpSrv := server.NewStreamableHTTPServer(srv)
	ts := httptest.NewServer(httpSrv)
	defer ts.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	c, err := NewClient(ctx, ServerConfig{Name: "http-server", Type: "http", URL: ts.URL + "/mcp"})
	if err != nil {
		t.Fatalf("NewClient(http): %v", err)
	}
	defer c.Close()

	tools, err := c.ListTools(ctx)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(tools) != 1 || tools[0].Name != "echo" {
		t.Errorf("ListTools = %+v, want one tool 'echo'", tools)
	}

	// CallTool round-trip over the HTTP transport.
	res, err := c.CallTool(ctx, "echo", map[string]any{"echo": "x"})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if len(res.Content) == 0 {
		t.Errorf("CallTool returned no content")
	}
}

// TestNewClientHTTPBadURL verifies the HTTP transport errors cleanly on an unreachable URL (no
// hang, no panic). The handshake fails fast because nothing is listening.
func TestNewClientHTTPBadURL(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := NewClient(ctx, ServerConfig{Name: "dead", Type: "http", URL: "http://127.0.0.1:1/mcp"})
	if err == nil {
		t.Fatal("unreachable HTTP server should error")
	}
}

// TestLoadAllHTTPMultiple verifies LoadAll connects to two real streamable-http servers and
// aggregates their tools (the http transport path through the LoadAll loop, not just the error
// path).
func TestLoadAllHTTPMultiple(t *testing.T) {
	start := func(name, toolName string) string {
		srv := server.NewMCPServer(name, "1.0.0")
		srv.AddTool(
			mcp.NewTool(toolName, mcp.WithDescription("test tool")),
			func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				return &mcp.CallToolResult{Content: []mcp.Content{mcp.NewTextContent("ok")}}, nil
			},
		)
		h := server.NewStreamableHTTPServer(srv)
		ts := httptest.NewServer(h)
		t.Cleanup(ts.Close)
		return ts.URL + "/mcp"
	}
	urlA := start("alpha", "tool_a")
	urlB := start("beta", "tool_b")

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	tools, closeFn, err := LoadAll(ctx, []ServerConfig{
		{Name: "alpha", Type: "http", URL: urlA},
		{Name: "beta", Type: "http", URL: urlB},
	})
	defer closeFn()
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	names := map[string]bool{}
	for _, tl := range tools {
		names[tl.Info().Name] = true
	}
	if !names["tool_a"] || !names["tool_b"] {
		t.Errorf("LoadAll aggregated tools = %v, want tool_a and tool_b", names)
	}
}
