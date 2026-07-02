package tui

import (
	"context"
	"fmt"

	"github.com/skys-mission/creator-agent/cmd/creator-agent/tui/i18n"
)

type MCPManager interface {
	Status() []MCPManagerServerStatus
	SetEnabled(ctx context.Context, name string, enabled bool) error
	SetToolEnabled(ctx context.Context, server, tool string, enabled bool) error
	HasServers() bool
}

type MCPManagerServerStatus struct {
	Name       string
	Enabled    bool
	Connected  bool
	Connecting bool
	ToolCount  int
	LastErr    string
	Tools      []MCPManagerToolStatus
}

type MCPManagerToolStatus struct {
	Name    string
	Enabled bool
}

type mcpPickerEntryKind int

const (
	mcpEntryServer mcpPickerEntryKind = iota
	mcpEntryTool
)

type mcpPickerEntry struct {
	kind       mcpPickerEntryKind
	server     string
	tool       string
	toolOn     bool
	serverOn   bool
	connected  bool
	connecting bool
	toolCount  int
	lastErr    string
	expanded   bool
}

type mcpPickerState struct {
	picker[mcpPickerEntry]

	toggling     string
	toggleCancel context.CancelFunc
	lastErr      string
	collapsed    map[string]bool
}

func openMCPPicker(a *App) {
	if a.rt.mcpManager == nil {
		a.addSystem(i18n.T("picker.mcp.no_manager"))
		return
	}
	if !a.rt.mcpManager.HasServers() {
		a.addSystem(i18n.T("picker.mcp.no_servers"))
		return
	}
	p := &a.mcpPicker
	closeAllOtherPickers(a, &a.mcpPicker.open)
	p.initPicker(a.width)
	a.mcpPicker.toggling = ""
	a.mcpPicker.lastErr = ""
	if a.mcpPicker.collapsed == nil {
		a.mcpPicker.collapsed = make(map[string]bool)
	}
	refreshMCPPickerEntries(a)
	a.forceRender = true
}

func closeMCPPicker(a *App) {
	a.mcpPicker.close(a)
}

func buildMCPEntries(a *App) []mcpPickerEntry {
	if a.rt.mcpManager == nil {
		return nil
	}
	statuses := a.rt.mcpManager.Status()
	p := &a.mcpPicker
	var entries []mcpPickerEntry
	for _, st := range statuses {
		expanded := !p.collapsed[st.Name]
		connecting := st.Connecting || p.toggling == st.Name
		entries = append(entries, mcpPickerEntry{
			kind:       mcpEntryServer,
			server:     st.Name,
			serverOn:   st.Enabled,
			connected:  st.Connected,
			connecting: connecting,
			toolCount:  st.ToolCount,
			lastErr:    st.LastErr,
			expanded:   expanded && st.Connected,
		})
		if expanded && st.Connected {
			for _, ts := range st.Tools {
				entries = append(entries, mcpPickerEntry{
					kind:     mcpEntryTool,
					server:   st.Name,
					tool:     ts.Name,
					toolOn:   ts.Enabled,
					serverOn: st.Enabled,
				})
			}
		}
	}
	return entries
}

func refreshMCPPickerEntries(a *App) {
	a.mcpPicker.refilter(a, func() []mcpPickerEntry { return buildMCPEntries(a) }, filterMCPEntries)
}

func filterMCPEntries(entries []mcpPickerEntry, query string) []mcpPickerEntry {
	q := normalizeLower(query)
	if q == "" {
		return entries
	}
	var out []mcpPickerEntry
	for _, e := range entries {
		var hit bool
		if e.kind == mcpEntryServer {
			if score, ok := fuzzyScore(e.server, "", q); ok && score > 0 {
				hit = true
			}
		} else {
			if score, ok := fuzzyScore(e.tool, e.server, q); ok && score > 0 {
				hit = true
			}
		}
		if hit {
			out = append(out, e)
		}
	}
	return out
}

func toggleMCPServer(a *App) {
	p := &a.mcpPicker
	if len(p.entries) == 0 || a.rt.mcpManager == nil {
		return
	}
	if p.selIdx >= len(p.entries) || p.entries[p.selIdx].kind != mcpEntryServer {
		return
	}
	if a.status != statusIdle {
		a.addSystem(i18n.T("picker.mcp.busy_server"))
		return
	}
	if p.toggling != "" {
		return
	}
	sel := p.entries[p.selIdx]
	newState := !sel.serverOn
	parent := a.rt.ctx
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithCancel(parent)
	p.toggling = sel.server
	p.toggleCancel = cancel
	p.lastErr = ""
	refreshMCPPickerEntries(a)
	a.forceRender = true
	mgr := a.rt.mcpManager
	sender := a.rt.sender
	server := sel.server
	// Only the network I/O (SetEnabled) runs off the event loop. The agent rebuild — which mutates
	// App state via publishSwitchAgent — is deferred to applyMCPToggleDone on the event-loop
	// goroutine, preserving the single-owner invariant for App state (no cross-goroutine mutation).
	go func() {
		err := mgr.SetEnabled(ctx, server, newState)
		if sender != nil {
			sender(mcpToggleDoneMsg{server: server, enabled: newState, err: err})
		}
	}()
}

type mcpToggleDoneMsg struct {
	server  string
	enabled bool
	err     error
}

func applyMCPToggleDone(a *App, msg mcpToggleDoneMsg) {
	p := &a.mcpPicker
	if p.toggling == msg.server {
		p.toggling = ""
		p.toggleCancel = nil
	}
	if msg.err != nil {
		p.lastErr = fmt.Sprintf(i18n.T("picker.mcp.server_err"), msg.server, msg.err)
		a.addSystem(fmt.Sprintf(i18n.T("picker.mcp.toggle_failed"), msg.server, msg.err))
	} else {
		// Rebuild the agent's tool set here on the event-loop goroutine (publishSwitchAgent mutates
		// App state and must not run from the toggle goroutine). Runs for both enable and disable:
		// EnabledTools() is only consulted at rebuild time, so without this a disabled server's tools
		// would linger in the running agent.
		if a.rt.rebuildTools != nil {
			if rerr := a.rt.rebuildTools(); rerr != nil {
				p.lastErr = fmt.Sprintf(i18n.T("picker.mcp.rebuild_err"), rerr)
				a.addSystem(fmt.Sprintf(i18n.T("picker.mcp.rebuild_failed"), rerr))
			}
		}
		if msg.enabled {
			a.addSystem(fmt.Sprintf(i18n.T("picker.mcp.server_enabled"), msg.server))
		} else {
			a.addSystem(fmt.Sprintf(i18n.T("picker.mcp.server_disabled"), msg.server))
		}
	}
	refreshMCPPickerEntries(a)
	a.forceRender = true
}

func cancelMCPToggle(a *App) {
	p := &a.mcpPicker
	if p.toggleCancel != nil {
		p.toggleCancel()
	}
}

func toggleMCPTool(a *App) {
	p := &a.mcpPicker
	if len(p.entries) == 0 || a.rt.mcpManager == nil {
		return
	}
	if p.selIdx >= len(p.entries) || p.entries[p.selIdx].kind != mcpEntryTool {
		return
	}
	if a.status != statusIdle {
		a.addSystem(i18n.T("picker.mcp.busy_tool"))
		return
	}
	sel := p.entries[p.selIdx]
	if !sel.serverOn {
		return
	}
	newState := !sel.toolOn
	if err := a.rt.mcpManager.SetToolEnabled(context.Background(), sel.server, sel.tool, newState); err != nil {
		p.lastErr = fmt.Sprintf(i18n.T("picker.mcp.tool_err"), sel.server, sel.tool, err)
		a.addSystem(fmt.Sprintf(i18n.T("picker.mcp.tool_toggle_failed"), sel.server, sel.tool, err))
		refreshMCPPickerEntries(a)
		a.forceRender = true
		return
	}
	if a.rt.rebuildTools != nil {
		if rerr := a.rt.rebuildTools(); rerr != nil {
			p.lastErr = fmt.Sprintf(i18n.T("picker.mcp.rebuild_err"), rerr)
			a.addSystem(fmt.Sprintf(i18n.T("picker.mcp.rebuild_failed"), rerr))
		}
	}
	if newState {
		a.addSystem(fmt.Sprintf(i18n.T("picker.mcp.tool_enabled"), sel.server, sel.tool))
	} else {
		a.addSystem(fmt.Sprintf(i18n.T("picker.mcp.tool_disabled"), sel.server, sel.tool))
	}
	refreshMCPPickerEntries(a)
	a.forceRender = true
}

func toggleMCPCollapse(a *App) {
	p := &a.mcpPicker
	if len(p.entries) == 0 || p.selIdx >= len(p.entries) {
		return
	}
	sel := p.entries[p.selIdx]
	if sel.kind != mcpEntryServer {
		return
	}
	if p.collapsed == nil {
		p.collapsed = make(map[string]bool)
	}
	if p.collapsed[sel.server] {
		delete(p.collapsed, sel.server)
	} else {
		p.collapsed[sel.server] = true
	}
	refreshMCPPickerEntries(a)
	a.forceRender = true
}

func (*mcpPickerState) onKey(a *App, k Key, ch rune) {
	p := &a.mcpPicker
	switch k {
	case KeyEsc, KeyCtrlP:
		if p.toggling != "" {
			cancelMCPToggle(a)
			return
		}
		closeMCPPicker(a)
		return
	case KeyCtrlC:
		if p.toggling != "" {
			cancelMCPToggle(a)
			return
		}
		closeMCPPicker(a)
		return
	case KeyEnter:
		toggleMCPSelection(a)
		return
	case KeyTab:
		toggleMCPCollapse(a)
		return
	}
	if k == KeyRune && ch == ' ' {
		toggleMCPSelection(a)
		return
	}
	p.handleKey(a, k, ch, nil, func() { refreshMCPPickerEntries(a) })
}

func toggleMCPSelection(a *App) {
	p := &a.mcpPicker
	if len(p.entries) == 0 || p.selIdx >= len(p.entries) {
		return
	}
	if p.entries[p.selIdx].kind == mcpEntryTool {
		toggleMCPTool(a)
		return
	}
	toggleMCPServer(a)
}

func (*mcpPickerState) render(a *App) {
	a.mcpPicker.draw(a, mcpRenderer{})
}

type mcpRenderer struct{}

func (mcpRenderer) title() string       { return i18n.T("picker.mcp.title") }
func (mcpRenderer) placeholder() string { return i18n.T("picker.mcp.placeholder") }
func (mcpRenderer) footer(a *App) string {
	p := &a.mcpPicker
	if p.lastErr != "" {
		return p.lastErr
	}
	if p.toggling != "" {
		return i18n.T("picker.mcp.footer.connecting")
	}
	return i18n.T("picker.mcp.footer.idle")
}
func (mcpRenderer) queryPrompt(a *App) (string, int) { return "> ", 2 }
func (mcpRenderer) cursor(a *App, innerX, queryY, promptWidth int) (int, int, bool) {
	return pickerQueryCursor(&a.mcpPicker.query, innerX, queryY, promptWidth)
}
func (mcpRenderer) drawItem(a *App, it mcpPickerEntry, selected bool, innerX, ry, innerW int) {
	const nameCol = 16
	statusW := innerW - 2 - nameCol - 1
	if statusW < 8 {
		statusW = 8
	}
	nameStyle := stylePaletteItem()
	statusStyle := stylePaletteItemDesc()
	if selected {
		nameStyle = stylePaletteSelected()
		statusStyle = stylePaletteSelectedDesc()
	}
	marker := "  "
	if selected {
		marker = "> "
	}
	drawTextRaw(a.screen, a, innerX, ry, 2, marker, nameStyle)
	if it.kind == mcpEntryServer {
		collapse := "-"
		if !it.expanded {
			collapse = "+"
		}
		drawTextRaw(a.screen, a, innerX+2, ry, nameCol, truncateStr(collapse+" "+it.server, nameCol), nameStyle)
		status := mcpStatusText(it, a.mcpPicker.toggling == it.server, a.spinnerFrame)
		stX := innerX + 2 + nameCol + 1
		drawTextRaw(a.screen, a, stX, ry, statusW, truncateStr(status, statusW), statusStyle)
	} else {
		toolStyle := nameStyle
		if !it.serverOn {
			toolStyle = styleToolDim()
		}
		toggle := "○"
		if it.toolOn {
			toggle = "✓"
		}
		drawTextRaw(a.screen, a, innerX+2, ry, innerW-2, truncateStr("  "+toggle+" "+it.tool, innerW-2), toolStyle)
	}
}

func mcpStatusText(it mcpPickerEntry, toggling bool, spinnerFrame int) string {
	if toggling || it.connecting {
		return spinnerFrameStr(spinnerFrame) + " " + i18n.T("picker.mcp.connecting")
	}
	if it.lastErr != "" {
		return "! " + fmt.Sprintf(i18n.T("picker.mcp.failed_n_tools"), it.toolCount)
	}
	if !it.connected {
		return "○ " + i18n.T("picker.mcp.disabled")
	}
	if it.serverOn {
		return "✓ " + fmt.Sprintf(i18n.T("picker.mcp.enabled_n_tools"), it.toolCount)
	}
	return "○ " + fmt.Sprintf(i18n.T("picker.mcp.disabled_n_tools"), it.toolCount)
}
