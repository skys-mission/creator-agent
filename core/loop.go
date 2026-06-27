package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"
)

// runLoop is the agent main loop (self-built, iterative, single state).
//
// Exit criteria: no tool_use this turn -> FinishStop; reached MaxSteps -> FinishStepLimit;
// user interrupt (ctx cancel) -> FinishCanceled.
// The model's stop_reason is not trusted.
//
// Event sending:
//   - Normal/error events use sendEvent (with ctx select): blocks until the consumer takes them unless ctx is canceled.
//   - Only FinishCanceled after ctx cancel uses trySendEvent (non-blocking best-effort): at this point ctx.Done is ready,
//     so sendEvent would immediately drop, but the consumer may still be ranging the channel, so a non-blocking send can reach it.
func (a *agent) runLoop(ctx context.Context, state *RunState, ch chan<- Event) {
	for state.Step = 0; state.Step < a.cfg.MaxSteps; state.Step++ {
		// Interrupt check: at the start of each step, test ctx (user Ctrl+C cancels ctx).
		if errors.Is(ctx.Err(), context.Canceled) {
			trySendEvent(ch, FinishEvent{Reason: FinishCanceled})
			return
		}

		for _, m := range a.cfg.Middlewares {
			if err := m.BeforeModel(ctx, state); err != nil {
				_ = sendEvent(ctx, ch, ErrorEvent{Err: fmt.Errorf("before model: %w", err)})
				return
			}
		}

		toolUses, err := a.runModelTurn(ctx, state, ch)
		if err != nil {
			// Distinguish interrupt vs real error: interrupt finishes gracefully, error is reported.
			if errors.Is(err, context.Canceled) {
				trySendEvent(ch, FinishEvent{Reason: FinishCanceled})
				return
			}
			// OnError chain: middleware gets a chance to handle (e.g., reactive compact then retry).
			// The first middleware returning nil wins (handled -> retry this turn); all returning error -> abort.
			handled := false
			for _, m := range a.cfg.Middlewares {
				e := m.OnError(ctx, state, err)
				if e == nil {
					handled = true
					break
				}
				// A non-nil OnError return must wrap the original err; otherwise the
				// middleware broke the contract — keep the original so error types survive.
				if !errors.Is(e, err) {
					Warnf("OnError middleware %T returned an error that does not wrap the original (contract violation); keeping original error", m)
				} else {
					err = e
				}
			}
			if handled {
				// Handled (e.g. compacted): retry this turn. Retries consume step quota
				// (MaxSteps bounds them), since the loop's post-increment still runs.
				continue
			}
			_ = sendEvent(ctx, ch, ErrorEvent{Err: fmt.Errorf("model stream: %w", err)})
			return
		}

		for _, m := range a.cfg.Middlewares {
			if err := m.AfterModel(ctx, state); err != nil {
				_ = sendEvent(ctx, ch, ErrorEvent{Err: fmt.Errorf("after model: %w", err)})
				return
			}
		}

		// Post-model ctx cancel check: the provider may cleanly close the stream on cancel without returning an error.
		// Without this check, runModelTurn returns nil and the loop would mistakenly emit FinishStop.
		if errors.Is(ctx.Err(), context.Canceled) {
			trySendEvent(ch, FinishEvent{Reason: FinishCanceled})
			return
		}

		if len(toolUses) == 0 {
			_ = sendEvent(ctx, ch, FinishEvent{Reason: FinishStop})
			return
		}

		results := a.executeTools(ctx, toolUses, ch)

		state.Messages = append(state.Messages, AssistantMessage("", toolUses...))
		for _, r := range results {
			state.Messages = append(state.Messages, ToolMessage(r.contentForModel(), r.callID, r.name))
		}
	}
	_ = sendEvent(ctx, ch, FinishEvent{Reason: FinishStepLimit})
}

func (a *agent) runModelTurn(ctx context.Context, state *RunState, ch chan<- Event) ([]ToolCall, error) {
	req := ModelRequest{Messages: state.Messages, Tools: state.Tools}
	debugLogModelRequest(state.Step, req)
	// Retry transient connection errors (429/5xx) before the stream starts; mid-stream MError
	// is not retried (safe resumption is impossible).
	stream, err := a.openStreamWithRetry(ctx, req)
	if err != nil {
		return nil, err
	}

	type acc struct {
		name  string
		parts []byte
	}
	accs := map[string]*acc{}
	var order []string // preserves tool call order
	// so providers that send both delta and complete do not produce duplicate start events.
	started := map[string]struct{}{}

	sawAny := false

	for ev := range stream {
		switch e := ev.(type) {
		case MTextDelta:
			sawAny = true
			if err := sendEvent(ctx, ch, TextEvent{Delta: e.Delta}); err != nil {
				return nil, err
			}
		case MThinkingDelta:
			sawAny = true
			if err := sendEvent(ctx, ch, ThinkingEvent{Delta: e.Delta}); err != nil {
				return nil, err
			}
		case MToolUseDelta:
			sawAny = true
			x, ok := accs[e.ID]
			isNew := !ok
			if isNew {
				x = &acc{}
				accs[e.ID] = x
				order = append(order, e.ID)
			}
			if e.Name != "" {
				x.name = e.Name
			}
			// do not emit MToolUseComplete, so the Start event must be sent on the first delta.
			if _, emitted := started[e.ID]; !emitted && e.Name != "" {
				started[e.ID] = struct{}{}
				if err := sendEvent(ctx, ch, ToolUseStartEvent{ID: e.ID, Name: e.Name}); err != nil {
					return nil, err
				}
			}
			x.parts = append(x.parts, e.DeltaJSON...)
			if err := sendEvent(ctx, ch, ToolUseDeltaEvent{ID: e.ID, DeltaJSON: e.DeltaJSON}); err != nil {
				return nil, err
			}
		case MToolUseComplete:
			sawAny = true
			x, ok := accs[e.ID]
			if !ok {
				x = &acc{}
				accs[e.ID] = x
				order = append(order, e.ID)
			}
			if e.Name != "" {
				x.name = e.Name
			}
			if len(e.Input) > 0 {
				x.parts = e.Input
			}
			if _, emitted := started[e.ID]; !emitted && e.Name != "" {
				started[e.ID] = struct{}{}
				if err := sendEvent(ctx, ch, ToolUseStartEvent{ID: e.ID, Name: e.Name}); err != nil {
					return nil, err
				}
			}
		case MUsage:
			if err := sendEvent(ctx, ch, UsageEvent{Usage: e.Usage}); err != nil {
				return nil, err
			}
		case MFinish:
			sawAny = true // receiving a finish reason counts as non-empty (some providers only send finish)
		case MError:
			return nil, e.Err
		}
	}

	// If the stream was canceled by ctx, return the cancel error so the loop emits FinishCanceled.
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !sawAny {
		return nil, fmt.Errorf("model returned empty response (no content, no tool calls, no finish)")
	}

	calls := make([]ToolCall, 0, len(order))
	for _, id := range order {
		x := accs[id]
		// Drop tool calls with empty names to prevent downstream unknown-tool noise and empty-name collapse issues.
		// Normal providers do not produce empty names; this is a defense against malformed/non-standard endpoints.
		if x.name == "" {
			Warnf("dropping tool call with empty name (id=%s, %d bytes input)", id, len(x.parts))
			continue
		}
		calls = append(calls, ToolCall{ID: id, Name: x.name, Input: json.RawMessage(x.parts)})
	}
	debugLogToolCalls(state.Step, calls)
	return calls, nil
}

// transientRetryMax is the application-level retry limit for transient errors (429/5xx).
// Complements SDK-level connection retries (typically 3): SDK handles quick connection issues;
// persistent rate limiting or server fluctuations reach here and need longer backoff intervals.
const transientRetryMax = 2

// openStreamWithRetry calls Model.Stream and retries transient errors (RateLimitedError/ServerError)
// with limited exponential backoff before the stream starts.
//
// Not retried (returned directly for OnError or abort handling):
//   - ClientError (4xx excluding 429, e.g., auth failure / bad request) -- retrying is pointless, it's a config issue.
//   - context.Canceled / context.DeadlineExceeded -- respect user interrupt or timeout.
//   - Unclassified errors (network blips as bare errors) -- conservative: SDK already retried these.
//
// Backoff: 1s -> 3s (with transientRetryMax=2). Backoff checks ctx so user interrupt exits immediately.
func (a *agent) openStreamWithRetry(ctx context.Context, req ModelRequest) (<-chan ModelEvent, error) {
	var lastErr error
	backoff := time.Second
	for attempt := 0; attempt <= transientRetryMax; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, err // interrupt before backoff
		}
		stream, err := a.cfg.Model.Stream(ctx, req)
		if err == nil {
			return stream, nil
		}
		lastErr = err
		if !isTransientError(err) {
			return nil, err
		}
		if attempt < transientRetryMax {
			Warnf("transient model error (attempt %d/%d), retrying in %v: %v",
				attempt+1, transientRetryMax+1, backoff, err)
			select {
			case <-time.After(backoff):
			case <-ctx.Done():
				return nil, ctx.Err()
			}
			backoff += 2 * time.Second // 1s -> 3s
		}
	}
	return nil, lastErr
}

func isTransientError(err error) bool {
	var rl *RateLimitedError
	var se *ServerError
	return errors.As(err, &rl) || errors.As(err, &se)
}

// sendEvent sends ev to ch; if ctx is canceled before or during the send, it returns ctx.Err().
//
// Used by the agent loop and executeTools to forward model and tool events to the outer event channel.
// If the outer channel is unbuffered and the consumer disconnects (TUI crash, early test close, user exit),
// a bare `ch <- ev` would block forever and not respond to ctx cancellation, causing goroutine leaks / deadlocks.
// The select on ctx.Done guarantees cancellation can unblock (M2).
//
// A non-nil return error causes the loop to return early (runModelTurn treats it as a turn error);
// executeTools ignores it because results are already recorded, and a disconnected consumer is just a UI issue.
func sendEvent(ctx context.Context, ch chan<- Event, ev Event) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case ch <- ev:
		return nil
	}
}

// trySendEvent is a non-blocking send: delivers if the consumer is reading, otherwise drops (never blocks).
//
// Used for terminal/error events (Finish*/Error*): these should still be delivered even when ctx is already
// canceled (e.g., FinishCanceled after ctx cancel — the consumer is still ranging the channel, so a non-blocking
// send can succeed), while guaranteeing no block or leak if the consumer is fully gone.
// Complements sendEvent (streaming events, interruptible by ctx).
func trySendEvent(ch chan<- Event, ev Event) {
	select {
	case ch <- ev:
	default:
	}
}

// sendEventPreferDelivery sends ev to ch, preferring delivery over ctx cancellation.
// It first tries a non-blocking send; if the channel is full, it waits for either ctx.Done() or ch to accept.
// This is used by InterruptBlock tools: the work itself cannot be canceled, so if the consumer is still
// reading we want the result to reach it, but we must not block forever if the consumer has left.
func sendEventPreferDelivery(ctx context.Context, ch chan<- Event, ev Event) {
	select {
	case ch <- ev:
		return
	default:
	}
	select {
	case <-ctx.Done():
	case ch <- ev:
	}
}

var (
	debugOnce    sync.Once
	debugEnabled bool
)

func debugOn() bool {
	debugOnce.Do(func() {
		v := strings.ToLower(strings.TrimSpace(os.Getenv("CREATOR_AGENT_DEBUG")))
		debugEnabled = v == "1" || v == "true"
	})
	return debugEnabled
}

func debugLogModelRequest(step int, req ModelRequest) {
	if !debugOn() {
		return
	}
	fmt.Fprintf(os.Stderr, "\n[debug] === step %d: sending %d messages to model ===\n", step, len(req.Messages))
	for i, m := range req.Messages {
		role := string(m.Role)
		content := truncateRunes(m.Content, 120)
		extra := ""
		if len(m.ToolCalls) > 0 {
			ids := make([]string, 0, len(m.ToolCalls))
			for _, tc := range m.ToolCalls {
				ids = append(ids, fmt.Sprintf("%s(id=%s,args=%s)", tc.Name, tc.ID, truncateRunes(string(tc.Input), 60)))
			}
			extra = " tool_calls=[" + strings.Join(ids, ",") + "]"
		}
		if m.ToolCallID != "" {
			extra += fmt.Sprintf(" tool_call_id=%s tool_name=%s", m.ToolCallID, m.ToolName)
		}
		fmt.Fprintf(os.Stderr, "[debug]   [%d] %s: %q%s\n", i, role, content, extra)
	}
}

func debugLogToolCalls(step int, calls []ToolCall) {
	if !debugOn() {
		return
	}
	if len(calls) == 0 {
		fmt.Fprintf(os.Stderr, "[debug] step %d: model issued no tool calls (will finish)\n", step)
		return
	}
	fmt.Fprintf(os.Stderr, "[debug] step %d: model issued %d tool calls:\n", step, len(calls))
	for i, c := range calls {
		fmt.Fprintf(os.Stderr, "[debug]   ->[%d] %s id=%s input=%s\n", i, c.Name, c.ID, truncateRunes(string(c.Input), 100))
	}
}

func debugLogExecResult(callID, name string, result ToolResult, execErr error) {
	if !debugOn() {
		return
	}
	if execErr != nil {
		fmt.Fprintf(os.Stderr, "[debug]   <- %s id=%s ERROR: %v\n", name, callID, execErr)
		return
	}
	c := truncateRunes(result.Content, 80)
	fmt.Fprintf(os.Stderr, "[debug]   <- %s id=%s isErr=%v content=%q\n", name, callID, result.IsError, c)
}
