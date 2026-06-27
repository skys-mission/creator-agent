package core

import (
	"context"
	"strings"
)

// ModelProvider is the model abstraction (anti-corruption layer boundary).
//
// Core upper layers depend only on this interface; implementations live in core/adapters/.
// OpenAI-compatible endpoints go through adapters/openai with a custom BaseURL (zero adapter code);
// non-standard endpoints need a dedicated adapter.
type ModelProvider interface {
	Stream(ctx context.Context, req ModelRequest) (<-chan ModelEvent, error)
}

// ModelRequest is a single model call request.
type ModelRequest struct {
	Messages   []Message
	Tools      []ToolInfo // optional (omit to unbind tools)
	ToolChoice ToolChoice

	// General parameters (nil = use model defaults)
	Temperature *float32
	TopP        *float32
	MaxTokens   *int
	Stop        []string

	// Provider-specific capabilities (e.g., thinking, cache_control), recognized by adapters.
	// Adding new capabilities does not break the Core interface.
	Options map[string]any
}

// ToolChoice controls whether and how the model may call tools.
type ToolChoice int

const (
	ToolAuto     ToolChoice = iota // model decides autonomously
	ToolNone                       // tool calls disabled
	ToolRequired                   // tool call mandatory
)

// ModelEvent is a model streaming event (adapter converts the underlying stream into these).
// Sealed interface: external packages cannot extend the event types.
type ModelEvent interface{ isModelEvent() }

// MTextDelta is a text delta.
type MTextDelta struct{ Delta string }

// MThinkingDelta is a reasoning/thinking delta.
type MThinkingDelta struct{ Delta string }

// MToolUseDelta is a streaming delta of tool arguments (the model emits arguments incrementally).
// Name is present when the tool name appears in the stream (may be empty; the loop uses the last non-empty value).
type MToolUseDelta struct {
	ID        string
	Name      string
	DeltaJSON string
}

// MToolUseComplete signals a complete tool call (some providers emit the full argument JSON at once).
type MToolUseComplete struct {
	ID    string
	Name  string
	Input []byte // full argument JSON
}

// MUsage reports token usage for the current turn.
type MUsage struct{ Usage Usage }

// MFinish is the model's stop signal (informational only; the loop does not rely on it).
type MFinish struct{ Reason string }

// MError is a model stream error (mid-stream failure; the loop should abort and report).
type MError struct{ Err error }

func (MTextDelta) isModelEvent()       {}
func (MThinkingDelta) isModelEvent()   {}
func (MToolUseDelta) isModelEvent()    {}
func (MToolUseComplete) isModelEvent() {}
func (MUsage) isModelEvent()           {}
func (MFinish) isModelEvent()          {}
func (MError) isModelEvent()           {}

// CollectText calls the model once and concatenates all text deltas into a single string.
//
// Use case: summarization, auto-memory extraction, or other one-off model calls that bypass the agent loop.
// The caller is responsible for constructing appropriate messages (including system prompt or task instructions).
//
// Error handling: internal model stream errors (MError) are converted to returned errors; connection failures also return errors.
// Empty output returns "(empty)" (caller may decide how to handle).
func CollectText(ctx context.Context, model ModelProvider, msgs []Message) (string, error) {
	stream, err := model.Stream(ctx, ModelRequest{Messages: msgs})
	if err != nil {
		return "", err
	}
	var sb strings.Builder
	for ev := range stream {
		switch e := ev.(type) {
		case MTextDelta:
			sb.WriteString(e.Delta)
		case MError:
			if e.Err != nil {
				return "", e.Err
			}
		}
	}
	out := strings.TrimSpace(sb.String())
	if out == "" {
		return "(empty)", nil
	}
	return out, nil
}
