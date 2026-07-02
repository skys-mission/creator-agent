package mcp

// Package mcp is the MCP (Model Context Protocol) client adapter layer.
//
// Position (anti-corruption layer boundary): a standalone adapter package alongside core/adapters/openai/.
// Only imports core + github.com/modelcontextprotocol/go-sdk (does not depend on the LLM anti-corruption
// layer). Adapts MCP server tools into core.Tool.
//
// capability: the MCP protocol does not carry ReadOnly/ConcurrencySafe, so defaults are fail-closed
// (not read-only, not concurrency-safe); config can declare per-tool overrides.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/skys-mission/creator-agent/core"
)

// AdaptTools adapts a list of *mcp.Tool into a list of core.Tool.
//
// capability: defaults to fail-closed (not read-only, not concurrency-safe, MaxResultChars=20000);
// overrides declared in client.overrides take precedence.
func (c *Client) AdaptTools(tools []*mcp.Tool) []core.Tool {
	out := make([]core.Tool, 0, len(tools))
	for _, t := range tools {
		out = append(out, &adaptedTool{client: c, tool: t, schema: resolveInputSchema(t)})
	}
	return out
}

// renamedTool overrides the display name of a wrapped tool while delegating execution unchanged.
// Used to namespace colliding tool names (server__tool) across MCP servers: the model sees the
// namespaced name, but Exec still targets the original server-side tool name.
type renamedTool struct {
	core.Tool
	name string
}

func (r renamedTool) Info() core.ToolInfo {
	info := r.Tool.Info()
	info.Name = r.name
	return info
}

// namespacedName builds a collision-free tool name by prefixing the server name, sanitized to the
// characters providers accept in function names ([A-Za-z0-9_-]); other runes become underscores.
func namespacedName(server, tool string) string {
	return sanitizeToolNamePart(server) + "__" + tool
}

func sanitizeToolNamePart(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	return b.String()
}

// serverTool pairs a tool with the server it came from, so the flattening step can namespace
// colliding names deterministically.
type serverTool struct {
	server string
	tool   core.Tool
}

// namespaceCollisions returns the flattened tools with any name that appears on more than one server
// rewritten to server__tool. Names unique across the whole set are left untouched, so the common
// (non-colliding) case keeps short, stable names and does not disturb the prompt-prefix cache.
func namespaceCollisions(items []serverTool) []core.Tool {
	counts := make(map[string]int, len(items))
	for _, it := range items {
		counts[it.tool.Info().Name]++
	}
	out := make([]core.Tool, 0, len(items))
	for _, it := range items {
		name := it.tool.Info().Name
		if counts[name] > 1 {
			out = append(out, renamedTool{Tool: it.tool, name: namespacedName(it.server, name)})
		} else {
			out = append(out, it.tool)
		}
	}
	return out
}

// resolveInputSchema converts a tool's input schema to JSON once, at adaptation time. The SDK exposes
// InputSchema as any (the default JSON marshaling of the server's schema, typically map[string]any);
// we marshal it back to JSON Schema bytes. A server that returns no usable schema falls back to a
// permissive default; the warning is emitted here (once per tool) rather than in Info(), which the
// agent loop calls repeatedly.
func resolveInputSchema(t *mcp.Tool) json.RawMessage {
	const defaultSchema = `{"type":"object"}`
	name := ""
	if t != nil {
		name = t.Name
	}
	if t == nil || t.InputSchema == nil {
		core.Warnf("mcp tool %q returned no usable input schema; using permissive default {\"type\":\"object\"}", name)
		return json.RawMessage(defaultSchema)
	}
	schema, err := json.Marshal(t.InputSchema)
	if err != nil || len(schema) == 0 || string(schema) == "null" {
		core.Warnf("mcp tool %q returned no usable input schema; using permissive default {\"type\":\"object\"}", name)
		return json.RawMessage(defaultSchema)
	}
	return json.RawMessage(schema)
}

// adaptedTool wraps a single *mcp.Tool as a core.Tool.
type adaptedTool struct {
	client *Client
	tool   *mcp.Tool
	schema json.RawMessage // input schema resolved once at adaptation (see resolveInputSchema)
}

func (a *adaptedTool) Info() core.ToolInfo {
	info := core.ToolInfo{
		Name:        a.tool.Name,
		Description: a.tool.Description,
		// fail-closed defaults: not read-only, not concurrency-safe
		MaxResultChars:    20000,
		InterruptBehavior: core.InterruptCancel,
		InputSchema:       a.schema,
	}
	// capability override
	if o, ok := a.client.override(a.tool.Name); ok {
		info.ReadOnly = o.ReadOnly
		info.ConcurrencySafe = o.ConcurrencySafe
		if o.MaxResultChars > 0 {
			info.MaxResultChars = o.MaxResultChars
		}
	}
	return info
}

func (a *adaptedTool) Exec(ctx context.Context, input json.RawMessage) (core.ToolResult, error) {
	// input is the JSON generated by the model (tool parameters); unmarshal into a map for MCP (MCP expects an args object)
	args := map[string]any{}
	if len(input) > 0 {
		if err := json.Unmarshal(input, &args); err != nil {
			return core.ToolResult{Content: fmt.Sprintf("invalid input: %v", err), IsError: true}, nil
		}
	}

	res, err := a.client.CallTool(ctx, a.tool.Name, args)
	if err != nil {
		return core.ToolResult{}, err
	}

	content := extractContent(res)
	result := core.ToolResult{Content: content, IsError: res.IsError}
	return result, nil
}

// extractContent concatenates CallToolResult.Content ([]mcp.Content) into a string.
//
// The SDK decodes every content element as a pointer (*TextContent, *ImageContent, ...), so the type
// switch matches pointer types. Text is concatenated; non-text uses a placeholder (core.ToolResult.Content
// is a string; multimedia left for V2).
func extractContent(res *mcp.CallToolResult) string {
	if res == nil || len(res.Content) == 0 {
		return ""
	}
	var sb strings.Builder
	for _, c := range res.Content {
		switch v := c.(type) {
		case *mcp.TextContent:
			sb.WriteString(v.Text)
		case *mcp.ImageContent:
			fmt.Fprintf(&sb, "[image: %s, %d bytes]", v.MIMEType, len(v.Data))
		case *mcp.AudioContent:
			fmt.Fprintf(&sb, "[audio: %s, %d bytes]", v.MIMEType, len(v.Data))
		default:
			// EmbeddedResource or unknown type: JSON fallback
			if data, err := json.Marshal(c); err == nil {
				sb.Write(data)
			} else {
				sb.WriteString(fmt.Sprintf("[unknown content: %T]", c))
			}
		}
		sb.WriteString("\n")
	}
	return strings.TrimRight(sb.String(), "\n")
}

// LoadAll connects all configured servers, fetches and adapts tools, and returns a core.Tool slice + a close function.
//
// Callers should invoke close() on program exit to clean up all connections.
// If any server connection fails: returns already-collected tools + error (best-effort, does not discard everything because one server failed).
func LoadAll(ctx context.Context, configs []ServerConfig) (tools []core.Tool, close func(), err error) {
	var clients []*Client
	closeFn := func() {
		for _, c := range clients {
			_ = c.Close()
		}
	}
	var firstErr error
	var collected []serverTool
	for _, cfg := range configs {
		c, e := NewClient(ctx, cfg)
		if e != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("mcp server %q: %w", cfg.Name, e)
			}
			continue
		}
		clients = append(clients, c)
		mcpTools, e := c.ListTools(ctx)
		if e != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("mcp server %q list tools: %w", cfg.Name, e)
			}
			continue
		}
		for _, t := range c.AdaptTools(mcpTools) {
			collected = append(collected, serverTool{server: cfg.Name, tool: t})
		}
	}
	// Namespace names that collide across servers (server__tool); unique names are left untouched.
	tools = namespaceCollisions(collected)
	return tools, closeFn, firstErr
}
