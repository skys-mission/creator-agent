package config

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"

	"github.com/skys-mission/creator-agent/core/mcp"
	"github.com/skys-mission/creator-agent/paths"
)

// MCP servers are configured in a dedicated JSON file, following the de-facto industry convention
// ({"mcpServers": {...}}) used by Claude Desktop, Cursor, and others. Keeping MCP out of the main
// TOML config means users can copy/paste server snippets from upstream docs verbatim.
//
// Two files are layered (project deep-merges over global), each optional:
//
//   - Global:  $XDG_CONFIG_HOME/creator/mcp.json (or ~/.creator/mcp.json)
//   - Project: ./.creator/mcp.json
//
// Servers are keyed by name and merged per field, so a project file can override or extend a single
// field of a global server (e.g. flip "disabled") without re-declaring the whole entry. A server with
// "disabled": true is dropped from the result, which lets a project switch off a globally-defined
// server. Unknown keys are tolerated (forward-compatible) so future capability fields do not break
// older binaries.

// mcpFileSchema is the on-disk JSON envelope.
type mcpFileSchema struct {
	MCPServers map[string]mcpServerJSON `json:"mcpServers"`
}

// mcpServerJSON is the config-layer DTO for one MCP server. It carries JSON tags; the core/mcp layer
// stays serialization-free and receives mcp.ServerConfig at the boundary (see toServerConfig).
type mcpServerJSON struct {
	Type     string                         `json:"type"`    // "stdio" / "http" / "sse"; default stdio
	Command  string                         `json:"command"` // stdio: executable
	Args     []string                       `json:"args"`    // stdio: command arguments
	Env      map[string]string              `json:"env"`     // stdio: environment variables
	URL      string                         `json:"url"`     // http/sse: server URL
	Disabled bool                           `json:"disabled"`
	Tools    map[string]mcpToolOverrideJSON `json:"tools"` // per-tool capability overrides keyed by tool name
}

// mcpToolOverrideJSON declares capability overrides for one MCP tool (the protocol does not carry
// these, so they are fail-closed unless set here).
type mcpToolOverrideJSON struct {
	ReadOnly        bool `json:"readOnly"`
	ConcurrencySafe bool `json:"concurrencySafe"`
	MaxResultChars  int  `json:"maxResultChars"`
}

// LoadMCPServers reads the global and project mcp.json files, deep-merges them by server name (project
// overrides/extends global), drops disabled servers, and converts the result into mcp.ServerConfig
// values ready for mcp.NewManager. Missing files are not an error. It returns the servers (sorted by
// name for deterministic startup), any non-fatal warnings, and a fatal error only on malformed JSON.
func LoadMCPServers() ([]mcp.ServerConfig, []string, error) {
	var warnings []string

	filePaths, files := mcpFilePaths()
	merged := map[string]map[string]any{}
	for i, data := range files {
		if data == nil {
			continue
		}
		var raw map[string]json.RawMessage
		if err := json.Unmarshal(data, &raw); err != nil {
			return nil, warnings, fmt.Errorf("parse %s: %w", filePaths[i], err)
		}
		serversRaw, ok := raw["mcpServers"]
		if !ok {
			warnings = append(warnings, fmt.Sprintf("%s: missing \"mcpServers\" object; ignored", filePaths[i]))
			continue
		}
		var servers map[string]map[string]any
		if err := json.Unmarshal(serversRaw, &servers); err != nil {
			return nil, warnings, fmt.Errorf("parse %s mcpServers: %w", filePaths[i], err)
		}
		for name, fields := range servers {
			if dst, exists := merged[name]; exists {
				deepMergeMap(dst, fields)
			} else {
				merged[name] = fields
			}
		}
	}

	names := make([]string, 0, len(merged))
	for name := range merged {
		names = append(names, name)
	}
	sort.Strings(names)

	out := make([]mcp.ServerConfig, 0, len(names))
	for _, name := range names {
		fields := merged[name]
		dto, err := decodeMCPServer(fields)
		if err != nil {
			return nil, warnings, fmt.Errorf("decode mcp server %q: %w", name, err)
		}
		if dto.Disabled {
			continue
		}
		out = append(out, dto.toServerConfig(name))
	}
	return out, warnings, nil
}

// mcpFilePaths returns the candidate file paths (global, project) and their contents (nil when the
// file is absent). A read error other than "not exist" yields nil content with no failure, since MCP
// is optional and must never block startup.
func mcpFilePaths() (filePaths []string, contents [][]byte) {
	global, err := paths.MCPFile()
	if err == nil {
		filePaths = append(filePaths, global)
		contents = append(contents, readIfExists(global))
	}
	filePaths = append(filePaths, paths.ProjectMCPFile)
	contents = append(contents, readIfExists(paths.ProjectMCPFile))
	return filePaths, contents
}

// readIfExists returns the file content, or nil when the file is absent or unreadable.
func readIfExists(path string) []byte {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	return data
}

// decodeMCPServer converts a merged field map back into the typed DTO via a JSON round-trip, so the
// deep-merge stays generic (map-based) while the final shape is strongly typed.
func decodeMCPServer(fields map[string]any) (mcpServerJSON, error) {
	var dto mcpServerJSON
	buf, err := json.Marshal(fields)
	if err != nil {
		return dto, err
	}
	if err := json.Unmarshal(buf, &dto); err != nil {
		return dto, err
	}
	return dto, nil
}

// toServerConfig converts the DTO into the core mcp.ServerConfig value type.
func (s mcpServerJSON) toServerConfig(name string) mcp.ServerConfig {
	return mcp.ServerConfig{
		Name:  name,
		Type:  s.Type,
		Cmd:   s.Command,
		Args:  s.Args,
		Env:   envPairs(s.Env),
		URL:   s.URL,
		Tools: toMCPToolOverrides(s.Tools),
	}
}

// envPairs flattens an env map into sorted "KEY=VAL" entries (sorted for deterministic process env).
func envPairs(env map[string]string) []string {
	if len(env) == 0 {
		return nil
	}
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(env))
	for _, k := range keys {
		out = append(out, k+"="+env[k])
	}
	return out
}

// toMCPToolOverrides converts the per-tool override map into sorted mcp.ToolOverride values.
func toMCPToolOverrides(in map[string]mcpToolOverrideJSON) []mcp.ToolOverride {
	if len(in) == 0 {
		return nil
	}
	names := make([]string, 0, len(in))
	for name := range in {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]mcp.ToolOverride, 0, len(in))
	for _, name := range names {
		t := in[name]
		out = append(out, mcp.ToolOverride{
			Name:            name,
			ReadOnly:        t.ReadOnly,
			ConcurrencySafe: t.ConcurrencySafe,
			MaxResultChars:  t.MaxResultChars,
		})
	}
	return out
}
