// Package core is the open-source core library of creator-agent.
//
// It defines the core abstractions and domain model of an AI agent:
//   - Agent: the core executor (Stream / ClearSession).
//   - Tool: the tool interface with fail-closed capability declarations.
//   - ModelProvider: model abstraction (anti-corruption layer boundary; implementations live in core/adapters/).
//   - Middleware: interception / rewriting hooks (used for compaction, permissions, and memory).
//   - Message / Event: domain models owned by Core, independent of any underlying SDK.
//
// Design principles:
//   - Anti-corruption layer: Core's public API does not expose any underlying SDK types.
//   - Single source of truth: the CLI is a thin entry point; all agent logic lives in core.
//   - Fail-closed: a tool that does not declare capabilities is treated as a write + non-concurrent operation.
package core
