// Package contract is the seam between the TUI and the agent core.
//
// This repository is mid-rebuild: the previous core, config, adapters, middlewares and tool
// implementations have been removed, and only the TUI has been kept (it is where the hard-won
// rendering lessons live — see docs/tui.md). The TUI is preserved as live, tested code, so it needs
// the API surface it consumes to still exist. That surface is exactly this package.
//
// What lives here:
//   - The domain model: Message / Event / ToolInfo / ToolResult / SessionStore / Agent. These are
//     complete and unmodified — they define the shape the new core must produce and consume.
//   - The runtime control surfaces the TUI drives: Mode / ModeController / AllowSet /
//     SandboxController / AskResolver.
//   - A handful of leaf helpers the TUI calls directly (session id + title derivation, the
//     approve-key grouping rules, user-facing error hints, on-disk locations).
//
// What deliberately does NOT live here: any agent logic. No run loop, no tool execution, no model
// adapters, no middleware pipeline, no config loading. Adding those here would defeat the purpose —
// they belong in the new core, implementing this contract.
package contract
