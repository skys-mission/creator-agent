package main

// mcp.go adapts the concrete *mcp.Manager to the TUI's MCPManager interface. The adapter lives in
// package main (which already imports core/mcp) so the tui package itself stays free of a
// core/mcp import — the boundary is the tui.MCPManager interface + tui.MCPManagerServerStatus.

import (
	"context"

	"github.com/skys-mission/creator-agent/cmd/creator-agent/tui"
	"github.com/skys-mission/creator-agent/core/mcp"
)

// mcpManagerAdapter wraps *mcp.Manager to satisfy tui.MCPManager. It translates the
// core/mcp.ServerStatus type into tui.MCPManagerServerStatus at the interface boundary.
type mcpManagerAdapter struct {
	*mcp.Manager
}

// Status adapts mcp.ServerStatus -> tui.MCPManagerServerStatus (including per-tool sub-status).
func (a mcpManagerAdapter) Status() []tui.MCPManagerServerStatus {
	raw := a.Manager.Status()
	out := make([]tui.MCPManagerServerStatus, 0, len(raw))
	for _, r := range raw {
		st := tui.MCPManagerServerStatus{
			Name:       r.Name,
			Enabled:    r.Enabled,
			Connected:  r.Connected,
			Connecting: r.Connecting,
			ToolCount:  r.ToolCount,
			LastErr:    r.LastErr,
		}
		if len(r.Tools) > 0 {
			st.Tools = make([]tui.MCPManagerToolStatus, 0, len(r.Tools))
			for _, ts := range r.Tools {
				st.Tools = append(st.Tools, tui.MCPManagerToolStatus{Name: ts.Name, Enabled: ts.Enabled})
			}
		}
		out = append(out, st)
	}
	return out
}

// SetEnabled, SetToolEnabled, and HasServers are passed through unchanged (signatures match the
// interface).
func (a mcpManagerAdapter) SetEnabled(ctx context.Context, name string, enabled bool) error {
	return a.Manager.SetEnabled(ctx, name, enabled)
}

func (a mcpManagerAdapter) SetToolEnabled(ctx context.Context, server, tool string, enabled bool) error {
	return a.Manager.SetToolEnabled(ctx, server, tool, enabled)
}

func (a mcpManagerAdapter) HasServers() bool { return a.Manager.HasServers() }
