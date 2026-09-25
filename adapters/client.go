package adapters

import (
	"context"

	"github.com/skys-mission/creator-agent/contract"
)

// ModelClient is the narrow boundary between the agent runtime and a model endpoint: swapping
// protocols or SDKs must never ripple past this interface. Provider SDK types must never appear
// in its signature.
//
// Event stream protocol: Stream returns a channel the receiver must drain until it closes. The
// adapter emits zero or more content events (ThinkingEvent / TextEvent / ToolUseStartEvent /
// ToolUseDeltaEvent), an optional UsageEvent, then exactly one terminal sequence:
//
//	normal end:        FinishEvent{FinishStop}
//	canceled via ctx:  FinishEvent{FinishCanceled}
//	mid-stream fault:  ErrorEvent{Err} then FinishEvent{FinishError}
//
// Failures known before any output (unreachable host, HTTP error status, invalid request) are
// returned as the error result instead. FinishEvent never carries provider stop semantics: the
// loop derives the real finish reason from whether the turn produced tool use.
//
// ToolUseDeltaEvent.DeltaJSON is a raw JSON fragment; accumulating fragments per call ID yields
// the complete arguments JSON, which the tool itself decodes.
type ModelClient interface {
	Stream(ctx context.Context, req *contract.ModelRequest) (<-chan contract.Event, error)
	Name() string
}
