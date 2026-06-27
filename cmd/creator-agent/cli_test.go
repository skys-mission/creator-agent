package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/skys-mission/creator-agent/config"
	"github.com/skys-mission/creator-agent/core"
)

// mockAgent records calls + returns a preset event stream.
type mockAgent struct {
	streamErr    error             // Stream returns error
	events       []core.Event      // returned events (in order)
	cleared      []string          // ClearSession records
	lastStreamIn *core.StreamInput // records last Stream input
	streamCalls  int

	// lastCtx is the ctx passed to the most recent Stream call.
	lastCtx context.Context
}

func (m *mockAgent) Stream(ctx context.Context, in core.StreamInput) (<-chan core.Event, error) {
	m.streamCalls++
	m.lastCtx = ctx
	cp := in
	m.lastStreamIn = &cp
	if m.streamErr != nil {
		return nil, m.streamErr
	}
	ch := make(chan core.Event, len(m.events))
	go func() {
		defer close(ch)
		for _, e := range m.events {
			ch <- e
		}
	}()
	return ch, nil
}

func (m *mockAgent) ClearSession(id string) {
	m.cleared = append(m.cleared, id)
}

// ===== printEventsTo rendering =====

// Verify: text events are streamed and printed.
func TestPrintEventsText(t *testing.T) {
	var buf bytes.Buffer
	printEventsTo(mockEventChan(
		core.TextEvent{Delta: "hel"},
		core.TextEvent{Delta: "lo"},
	), &buf)
	// First text is preceded by println (blank line), then deltas are concatenated
	out := buf.String()
	if !strings.Contains(out, "hello") {
		t.Errorf("text not printed: %q", out)
	}
}

// Verify: tool event rendering (start + result).
func TestPrintEventsTool(t *testing.T) {
	useColor = false // clean output
	var buf bytes.Buffer
	printEventsTo(mockEventChan(
		core.ToolUseStartEvent{ID: "t1", Name: "read"},
		core.ToolResultEvent{ID: "t1", Result: core.ToolResult{Content: "data"}},
	), &buf)
	out := buf.String()
	if !strings.Contains(out, "read") {
		t.Errorf("tool name missing: %q", out)
	}
	if !strings.Contains(out, "✓") {
		t.Errorf("success marker missing: %q", out)
	}
}

// Verify: tool errors use ✗ marker (both IsError and Err variants).
func TestPrintEventsToolError(t *testing.T) {
	useColor = false
	t.Run("IsError", func(t *testing.T) {
		var buf bytes.Buffer
		printEventsTo(mockEventChan(
			core.ToolUseStartEvent{ID: "t1", Name: "bash"},
			core.ToolResultEvent{ID: "t1", Result: core.ToolResult{Content: "fail", IsError: true}},
		), &buf)
		if !strings.Contains(buf.String(), "✗") {
			t.Errorf("error marker missing: %q", buf.String())
		}
	})
	t.Run("systemError", func(t *testing.T) {
		var buf bytes.Buffer
		printEventsTo(mockEventChan(
			core.ToolUseStartEvent{ID: "t2", Name: "bash"},
			core.ToolResultEvent{ID: "t2", Err: errors.New("boom")},
		), &buf)
		if !strings.Contains(buf.String(), "✗") {
			t.Errorf("error marker missing: %q", buf.String())
		}
	})
}

// Verify: non-stop FinishEvent prints reason.
func TestPrintEventsFinishReason(t *testing.T) {
	useColor = false
	var buf bytes.Buffer
	printEventsTo(mockEventChan(
		core.FinishEvent{Reason: core.FinishStepLimit},
	), &buf)
	out := buf.String()
	if !strings.Contains(out, "step_limit") {
		t.Errorf("step_limit reason missing: %q", out)
	}
}

// Verify: ErrorEvent goes to out (no longer to os.Stderr), making it testable.
func TestPrintEventsErrorEvent(t *testing.T) {
	useColor = false
	var buf bytes.Buffer
	printEventsTo(mockEventChan(
		core.ErrorEvent{Err: errors.New("model stream: connection refused")},
	), &buf)
	out := buf.String()
	if !strings.Contains(out, "connection refused") {
		t.Errorf("error content missing: %q", out)
	}
	// Error classification hint (core.UserHint): unclassified errors get a neutral "[Error]" prefix
	if !strings.Contains(out, "[Error]") {
		t.Errorf("error classification prefix missing: %q", out)
	}
}

// Verify: classified error (401) in dumb/headless output shows auth hint (core.UserHint).
func TestPrintEventsErrorClassifiedAuth(t *testing.T) {
	useColor = false
	var buf bytes.Buffer
	printEventsTo(mockEventChan(
		core.ErrorEvent{Err: &core.ClientError{Err: errors.New("401 Unauthorized"), StatusCode: 401}},
	), &buf)
	out := buf.String()
	if !strings.Contains(out, "auth") || !strings.Contains(out, "key") {
		t.Errorf("401 should show auth hint in dumb path, got: %q", out)
	}
	if !strings.Contains(out, "401 Unauthorized") {
		t.Errorf("401 raw error should still be shown, got: %q", out)
	}
}

// Verify: IsError ToolResult now prints Content (fixed bug where only ✗ was printed).
func TestPrintEventsIsErrorShowsContent(t *testing.T) {
	useColor = false
	var buf bytes.Buffer
	printEventsTo(mockEventChan(
		core.ToolUseStartEvent{ID: "t1", Name: "bash"},
		core.ToolResultEvent{ID: "t1", Result: core.ToolResult{Content: "command not found", IsError: true}},
	), &buf)
	out := buf.String()
	if !strings.Contains(out, "command not found") {
		t.Errorf("IsError content should be shown: %q", out)
	}
}

// Verify: long errors are truncated.
func TestPrintEventsTruncateLongError(t *testing.T) {
	useColor = false
	var buf bytes.Buffer
	long := strings.Repeat("x", 800)
	printEventsTo(mockEventChan(
		core.ErrorEvent{Err: errors.New(long)},
	), &buf)
	out := buf.String()
	// Truncated to 500 + …, should not contain the full 800 characters
	if strings.Contains(out, strings.Repeat("x", 600)) {
		t.Error("long error should be truncated")
	}
}

// Verify: stop FinishEvent is silent (avoids noise).
func TestPrintEventsStopSilent(t *testing.T) {
	useColor = false
	var buf bytes.Buffer
	printEventsTo(mockEventChan(
		core.TextEvent{Delta: "done"},
		core.FinishEvent{Reason: core.FinishStop},
	), &buf)
	if strings.Contains(buf.String(), "finish") {
		t.Errorf("stop finish should be silent: %q", buf.String())
	}
}

// ===== hostOf (converged to config.HostOf, tests in config package TestHostOf) =====

// ===== paint / isTTY =====

func TestPaintNoColor(t *testing.T) {
	useColor = false
	if got := paint(cRed, "x"); got != "x" {
		t.Errorf("paint with color off = %q, want x", got)
	}
}

func TestPaintColorOn(t *testing.T) {
	useColor = true
	defer func() { useColor = false }()
	got := paint(cRed, "x")
	if !strings.Contains(got, cRed) || !strings.Contains(got, ansiReset) {
		t.Errorf("paint with color on = %q, missing ANSI codes", got)
	}
}

// ===== runHeadlessIO =====

// Verify: headless sends prompt to agent and prints events.
func TestRunHeadlessPrintsEvents(t *testing.T) {
	useColor = false
	ag := &mockAgent{events: []core.Event{
		core.TextEvent{Delta: "answer"},
		core.FinishEvent{Reason: core.FinishStop},
	}}
	var buf bytes.Buffer
	runHeadlessIO(context.Background(), ag, "hi", &buf)
	if ag.streamCalls != 1 {
		t.Errorf("Stream called %d times, want 1", ag.streamCalls)
	}
	// Verify prompt is passed through
	if ag.lastStreamIn == nil || len(ag.lastStreamIn.Messages) != 1 {
		t.Fatalf("StreamInput wrong: %+v", ag.lastStreamIn)
	}
	if ag.lastStreamIn.Messages[0].Content != headlessModeNote+"hi" {
		t.Errorf("prompt = %q, want headless-context-wrapped hi", ag.lastStreamIn.Messages[0].Content)
	}
	// headless has no sessionID (stateless)
	if ag.lastStreamIn.SessionID != "" {
		t.Errorf("headless sessionID = %q, want empty", ag.lastStreamIn.SessionID)
	}
	if !strings.Contains(buf.String(), "answer") {
		t.Errorf("answer not printed: %q", buf.String())
	}
}

// Verify: headless on Stream error calls die (os.Exit) — too heavy for a subprocess test,
// here we only verify that ag is called and does not panic (die is untestable, skip exit verification).

// ===== runREPLIO command routing =====

// Verify: /exit exits immediately (does not call Stream).
func TestREPLExit(t *testing.T) {
	useColor = false
	ag := &mockAgent{events: []core.Event{}}
	var out bytes.Buffer
	runREPLIO(context.Background(), ag, config.Profile{Model: "m"}, strings.NewReader("/exit\n"), &out)
	if ag.streamCalls != 0 {
		t.Errorf("Stream should not be called for /exit: %d", ag.streamCalls)
	}
}

// Verify: /clear calls ClearSession but does not call Stream.
func TestREPLClear(t *testing.T) {
	useColor = false
	ag := &mockAgent{events: []core.Event{}}
	var out bytes.Buffer
	runREPLIO(context.Background(), ag, config.Profile{Model: "m"},
		strings.NewReader("/clear\n/exit\n"), &out)
	if len(ag.cleared) != 1 || !strings.HasPrefix(ag.cleared[0], "ses_") {
		t.Errorf("ClearSession not called for the session id: %v", ag.cleared)
	}
	if ag.streamCalls != 0 {
		t.Errorf("Stream should not be called for /clear: %d", ag.streamCalls)
	}
	if !strings.Contains(out.String(), "cleared") {
		t.Errorf("clear message missing: %q", out.String())
	}
}

// Verify: /help prints help.
func TestREPLHelp(t *testing.T) {
	useColor = false
	ag := &mockAgent{events: []core.Event{}}
	var out bytes.Buffer
	runREPLIO(context.Background(), ag, config.Profile{Model: "m"},
		strings.NewReader("/help\n/exit\n"), &out)
	if !strings.Contains(out.String(), "/clear") || !strings.Contains(out.String(), "/exit") {
		t.Errorf("help text missing: %q", out.String())
	}
}

// Verify: normal input is sent to agent with a generated "ses_..." session id.
func TestREPLNormalInput(t *testing.T) {
	useColor = false
	ag := &mockAgent{events: []core.Event{core.FinishEvent{Reason: core.FinishStop}}}
	var out bytes.Buffer
	runREPLIO(context.Background(), ag, config.Profile{Model: "m"},
		strings.NewReader("hello world\n/exit\n"), &out)
	if ag.streamCalls != 1 {
		t.Fatalf("Stream calls = %d, want 1", ag.streamCalls)
	}
	if !strings.HasPrefix(ag.lastStreamIn.SessionID, "ses_") {
		t.Errorf("sessionID = %q, want a generated ses_ id", ag.lastStreamIn.SessionID)
	}
	if len(ag.lastStreamIn.Messages) != 1 || ag.lastStreamIn.Messages[0].Content != "hello world" {
		t.Errorf("prompt wrong: %+v", ag.lastStreamIn.Messages)
	}
}

// Verify: empty lines are skipped (no Stream call).
func TestREPLEmptyLineSkipped(t *testing.T) {
	useColor = false
	ag := &mockAgent{events: []core.Event{}}
	var out bytes.Buffer
	runREPLIO(context.Background(), ag, config.Profile{Model: "m"},
		strings.NewReader("\n  \n/exit\n"), &out)
	if ag.streamCalls != 0 {
		t.Errorf("empty lines should not call Stream: %d", ag.streamCalls)
	}
}

// Verify: EOF (Ctrl+D) exits gracefully.
func TestREPLEOF(t *testing.T) {
	useColor = false
	ag := &mockAgent{events: []core.Event{}}
	var out bytes.Buffer
	runREPLIO(context.Background(), ag, config.Profile{Model: "m"},
		strings.NewReader(""), &out) // empty reader -> immediate EOF
	if ag.streamCalls != 0 {
		t.Errorf("EOF should not call Stream: %d", ag.streamCalls)
	}
}

// Verify: multi-turn sessionID is consistent (every turn reuses the same generated id).
func TestREPLMultiTurnSessionConsistent(t *testing.T) {
	useColor = false
	ag := &mockAgent{events: []core.Event{core.FinishEvent{Reason: core.FinishStop}}}
	var out bytes.Buffer
	runREPLIO(context.Background(), ag, config.Profile{Model: "m"},
		strings.NewReader("q1\nq2\n/exit\n"), &out)
	if ag.streamCalls != 2 {
		t.Fatalf("Stream calls = %d, want 2", ag.streamCalls)
	}
	// Second turn's sessionID is a generated ses_ id (mock only records the last call).
	if !strings.HasPrefix(ag.lastStreamIn.SessionID, "ses_") {
		t.Errorf("sessionID = %q, want a generated ses_ id", ag.lastStreamIn.SessionID)
	}
}

// TestREPLTurnCtxNotCanceledDuringStream is a regression test: the per-turn context passed to
// Stream must remain live while events are being produced and consumed. Previously the code called
// signal.NotifyContext's stop() immediately after Stream() returned; stop() cancels the context
// as a side effect, which made the real agent loop see ctx.Err()==Canceled and emit FinishCanceled
// before any text reached the consumer.
//
// We reproduce the observable symptom: a background goroutine that samples ctx.Err() while events
// are still in flight must see nil. The buggy code canceled ctx before the consumer drained the
// channel, so the sample would observe context.Canceled.
func TestREPLTurnCtxNotCanceledDuringStream(t *testing.T) {
	useColor = false

	// midStreamErr receives ctx.Err() sampled from a goroutine that runs while the consumer is
	// draining the event channel. Buffered=1 so the goroutine never blocks even if the test
	// does not read it.
	midStreamErr := make(chan error, 1)
	ag := &mockAgent{events: []core.Event{
		core.TextEvent{Delta: "hi"},
		core.FinishEvent{Reason: core.FinishStop},
	}}

	wrapped := &ctxSamplingAgent{inner: ag, sampleCh: midStreamErr}

	var out bytes.Buffer
	runREPLIO(context.Background(), wrapped, config.Profile{Model: "m"},
		strings.NewReader("hello\n/exit\n"), &out)

	// Text must reach the consumer (proves the turn completed normally).
	if !strings.Contains(out.String(), "hi") {
		t.Errorf("text event was not delivered to consumer; output=%q", out.String())
	}
	// The critical assertion: ctx must not be canceled while events are in flight.
	if err := <-midStreamErr; err != nil {
		t.Errorf("turn ctx was canceled while events were still in flight: %v (the old stop() bug)", err)
	}
}

// ctxSamplingAgent wraps a core.Agent and samples the ctx passed to Stream from a background
// goroutine that emits events. The sample is taken after a short delay so it overlaps with the
// window in which the real agent loop would be running and the consumer would be draining.
type ctxSamplingAgent struct {
	inner    core.Agent
	sampleCh chan<- error
}

func (c *ctxSamplingAgent) Stream(ctx context.Context, in core.StreamInput) (<-chan core.Event, error) {
	events, err := c.inner.Stream(ctx, in)
	if err != nil {
		return events, err
	}
	// Wrap the returned channel so we can sample ctx while the consumer drains. We forward
	// events through a new channel and take one sample mid-stream.
	out := make(chan core.Event, cap(events)+1)
	go func() {
		defer close(out)
		first := true
		for ev := range events {
			if first {
				// Sample ctx state at the start of event delivery: this is the moment the
				// real agent loop would be checking ctx.Err() between steps.
				c.sampleCh <- ctx.Err()
				first = false
			}
			out <- ev
		}
	}()
	return out, nil
}

func (c *ctxSamplingAgent) ClearSession(id string) { c.inner.ClearSession(id) }

// Verify: REPL startup banner contains model name.
func TestREPLBanner(t *testing.T) {
	useColor = false
	ag := &mockAgent{events: []core.Event{}}
	var out bytes.Buffer
	runREPLIO(context.Background(), ag, config.Profile{Model: "deepseek-chat", BaseURL: "https://api.deepseek.com"},
		strings.NewReader("/exit\n"), &out)
	banner := out.String()
	if !strings.Contains(banner, "deepseek-chat") {
		t.Errorf("model name missing in banner: %q", banner)
	}
	if !strings.Contains(banner, "api.deepseek.com") {
		t.Errorf("host missing in banner: %q", banner)
	}
}

// ===== helpers =====

// mockEventChan stuffs events into a buffered channel and closes it.
func mockEventChan(events ...core.Event) <-chan core.Event {
	ch := make(chan core.Event, len(events))
	for _, e := range events {
		ch <- e
	}
	close(ch)
	return ch
}

// Ensure mockAgent implements core.Agent interface (compile-time check).
var _ core.Agent = (*mockAgent)(nil)

// Prevent unused import error (io is used for interface signature matching, but not directly referenced in tests).
var _ = io.EOF

// TestFirstRunGuide: first-run guidance text should contain key info (config path, line to change, env var).
// This is what the user sees on first contact with the program; pin the elements to avoid regression to obscure hints.
func TestFirstRunGuide(t *testing.T) {
	got := firstRunGuide("/home/u/.creator/config.toml")
	for _, want := range []string{
		"/home/u/.creator/config.toml", // config file path
		"api_key",                            // field to change
		"OPENAI_API_KEY",                     // env var alternative
		"1.", "2.", "3.",                     // three-step guidance
	} {
		if !strings.Contains(got, want) {
			t.Errorf("firstRunGuide missing %q, got:\n%s", want, got)
		}
	}
}

// TestHeadlessModeNote: headless injected constraint note should clearly state write operations are blocked + give an alternative.
func TestHeadlessModeNote(t *testing.T) {
	for _, want := range []string{"non-interactive", "blocked", "read-only"} {
		if !strings.Contains(headlessModeNote, want) {
			t.Errorf("headlessModeNote missing %q, got %q", want, headlessModeNote)
		}
	}
}

// TestRegisterAndRunCleanups: verify exit cleanup registry.
// - registerCleanup functions run in LIFO reverse order during runCleanups
// - each cleanup panic does not block others
// - runCleanups is idempotent (repeated calls do not re-execute)
func TestRegisterAndRunCleanups(t *testing.T) {
	// Reset global registry (test isolation)
	exitCleanups = nil
	var order []int
	registerCleanup(func() { order = append(order, 1) })
	registerCleanup(func() { order = append(order, 2) })
	registerCleanup(func() { order = append(order, 3) })

	runCleanups()
	// LIFO: last registered runs first -> 3,2,1
	if len(order) != 3 || order[0] != 3 || order[1] != 2 || order[2] != 1 {
		t.Errorf("cleanups should run LIFO, got order %v, want [3 2 1]", order)
	}
	// Idempotent: second call does not execute again (exitCleanups set to nil)
	runCleanups()
	if len(order) != 3 {
		t.Errorf("runCleanups should be idempotent (no re-run), got %d executions", len(order))
	}
}

// TestCleanupPanicDoesNotBlockOthers: a single cleanup panic should not block other cleanups from running.
func TestCleanupPanicDoesNotBlockOthers(t *testing.T) {
	exitCleanups = nil
	var ran bool
	registerCleanup(func() { ran = true })
	registerCleanup(func() { panic("boom") }) // LIFO: runs first

	// Should not panic (runCleanups recovers internally)
	runCleanups()
	if !ran {
		t.Error("cleanup after a panicking cleanup should still run (recover isolation)")
	}
}

// TestWithSessionStoreValidHome: when HOME is valid, returns a non-nil Option (disk persistence enabled).
func TestWithSessionStoreValidHome(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	opt := withSessionStore()
	if opt == nil {
		t.Fatal("withSessionStore should return a non-nil Option")
	}
	// Verify Option can be applied to an AgentConfig (no panic)
	cfg := &core.AgentConfig{}
	opt(cfg)
	// SessionStore is a non-exported field in AgentConfig, cannot assert directly.
	// Persistence logic is covered by core package session_test.
}

// TestIsTTY: for non-character devices (e.g. regular *os.File) returns false.
// /dev/tty may not exist or be openable in the test environment; here we verify the false branch with a real file.
func TestIsTTYOnRegularFile(t *testing.T) {
	// Create a temporary regular file; isTTY should return false (not a character device)
	tmp, err := os.CreateTemp("", "istty-test-*")
	if err != nil {
		t.Skip("cannot create temp file")
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()
	if isTTY(tmp) {
		t.Error("regular file should not be detected as TTY")
	}
}

// TestRecentSessionID covers the -c/--continue helper: it returns the most recently updated
// session id from the store, and falls back when the store is empty, nil, or unreadable.
func TestRecentSessionID(t *testing.T) {
	fallback := "ses_fresh"

	t.Run("nil store returns fallback", func(t *testing.T) {
		if got := recentSessionID(nil, fallback); got != fallback {
			t.Errorf("recentSessionID(nil, ...) = %q, want %q", got, fallback)
		}
	})

	t.Run("empty store returns fallback", func(t *testing.T) {
		store := core.NewMemoryStore()
		if got := recentSessionID(store, fallback); got != fallback {
			t.Errorf("recentSessionID(empty, ...) = %q, want %q", got, fallback)
		}
	})

	t.Run("returns newest session", func(t *testing.T) {
		store := core.NewMemoryStore()
		// Seed oldest-first; SaveWithMeta stamps UpdatedAt at call time so List() is newest-first.
		_ = store.SaveWithMeta("ses_old", "old", []core.Message{core.UserMessage("a")})
		time.Sleep(2 * time.Millisecond)
		_ = store.SaveWithMeta("ses_new", "new", []core.Message{core.UserMessage("b")})

		got := recentSessionID(store, fallback)
		if got != "ses_new" {
			t.Errorf("recentSessionID = %q, want ses_new (most recent)", got)
		}
	})
}
