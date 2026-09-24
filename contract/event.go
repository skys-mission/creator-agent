package contract

// Event is the agent's streaming output event (tagged union / sealed interface).
//
// The TUI's event loop switches on the concrete types below; a new core must emit exactly these.
type Event interface {
	isEvent()
}

type TextEvent struct {
	Delta string
}

type ThinkingEvent struct {
	Delta string
}

type ToolUseStartEvent struct {
	ID   string
	Name string
}

type ToolUseDeltaEvent struct {
	ID        string
	DeltaJSON string
}

type ToolResultEvent struct {
	ID     string
	Result ToolResult
	Err    error
}

type UsageEvent struct {
	Usage Usage
}

type FinishEvent struct {
	Reason FinishReason
}

type ErrorEvent struct {
	Err error
}

// FinishReason reports why a run ended. The loop decides this from whether the turn produced tool
// use, never from a provider's stop_reason field (those are untrustworthy across providers).
type FinishReason string

const (
	FinishStop      FinishReason = "stop"
	FinishStepLimit FinishReason = "step_limit"
	FinishCanceled  FinishReason = "canceled"
	FinishError     FinishReason = "error"
)

func (TextEvent) isEvent()         {}
func (ThinkingEvent) isEvent()     {}
func (ToolUseStartEvent) isEvent() {}
func (ToolUseDeltaEvent) isEvent() {}
func (ToolResultEvent) isEvent()   {}
func (UsageEvent) isEvent()        {}
func (FinishEvent) isEvent()       {}
func (ErrorEvent) isEvent()        {}
