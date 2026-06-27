package middlewares

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/skys-mission/creator-agent/core"
)

// ===== collectText (via core.CollectText) =====

// stubProvider records received messages and returns fixed text.
type stubProvider struct {
	text     string
	received []core.Message
}

func (s *stubProvider) Stream(ctx context.Context, req core.ModelRequest) (<-chan core.ModelEvent, error) {
	s.received = req.Messages
	ch := make(chan core.ModelEvent, 1)
	go func() {
		defer close(ch)
		ch <- core.MTextDelta{Delta: s.text}
	}()
	return ch, nil
}

func TestCollectText(t *testing.T) {
	p := &stubProvider{text: "summary result"}
	out, err := core.CollectText(context.Background(), p, []core.Message{core.UserMessage("hi")})
	if err != nil {
		t.Fatal(err)
	}
	if out != "summary result" {
		t.Errorf("got %q", out)
	}
	if len(p.received) != 1 {
		t.Errorf("provider received %d msgs", len(p.received))
	}
}

// ===== microCompact =====

func TestMicroCompactNoTriggerUnderThreshold(t *testing.T) {
	mc := NewMicroCompact()
	mc.Threshold = 10000
	st := &core.RunState{Messages: []core.Message{
		core.ToolMessage(strings.Repeat("x", 1000), "c1", "read"),
		core.UserMessage("q"),
	}}
	orig := st.Messages[0].Content
	_ = mc.BeforeModel(context.Background(), st)
	if st.Messages[0].Content != orig {
		t.Error("should not compact under threshold")
	}
}

func TestMicroCompactTruncatesOldToolResults(t *testing.T) {
	mc := NewMicroCompact()
	mc.Threshold = 100
	mc.KeepRecent = 2
	mc.MaxToolResultChars = 50
	// Build: system + old long tool result + old long tool result + user + user (recent 2 kept)
	long := strings.Repeat("x", 500)
	st := &core.RunState{Messages: []core.Message{
		core.SystemMessage("sys"),
		core.ToolMessage(long, "c1", "read"), // old, should be truncated
		core.ToolMessage(long, "c2", "bash"), // old, should be truncated
		core.UserMessage("recent1"),          // recent kept
		core.UserMessage("recent2"),          // recent kept
	}}
	_ = mc.BeforeModel(context.Background(), st)
	// Old tool results should be truncated to stubs
	if st.Messages[1].Content == long {
		t.Error("old tool result not truncated")
	}
	if !strings.Contains(st.Messages[1].Content, "truncated") {
		t.Errorf("old tool result stub wrong: %q", st.Messages[1].Content)
	}
	// Recent user messages should be untouched
	if st.Messages[3].Content != "recent1" || st.Messages[4].Content != "recent2" {
		t.Error("recent messages modified")
	}
}

func TestMicroCompactKeepsShortResults(t *testing.T) {
	mc := NewMicroCompact()
	mc.Threshold = 100
	mc.KeepRecent = 1
	mc.MaxToolResultChars = 500
	short := "short result"
	st := &core.RunState{Messages: []core.Message{
		core.ToolMessage(short, "c1", "read"),
		core.UserMessage("recent"),
	}}
	// Total chars < threshold? short(11) + recent(6) = 17 < 100, no trigger
	_ = mc.BeforeModel(context.Background(), st)
	if st.Messages[0].Content != short {
		t.Error("short result should be kept")
	}
}

func TestMicroCompactIdempotent(t *testing.T) {
	mc := NewMicroCompact()
	mc.Threshold = 100
	mc.KeepRecent = 1
	mc.MaxToolResultChars = 50
	long := strings.Repeat("x", 500)
	st := &core.RunState{Messages: []core.Message{
		core.ToolMessage(long, "c1", "read"),
		core.UserMessage("recent"),
	}}
	_ = mc.BeforeModel(context.Background(), st)
	first := st.Messages[0].Content
	_ = mc.BeforeModel(context.Background(), st) // run again
	if st.Messages[0].Content != first {
		t.Error("second compaction changed already-compacted result (not idempotent)")
	}
}

// ===== reactive =====

func TestIsContextLimitError(t *testing.T) {
	cases := []struct {
		err  error
		want bool
	}{
		{nil, false},
		{errors.New("context length exceeded"), true},
		{errors.New("request too large: maximum context window"), true},
		{errors.New("this model's maximum context length is 4097 tokens"), true},
		{errors.New("413 Payload Too Large"), true},
		{errors.New("token limit reached"), true},
		{errors.New("unauthorized: invalid api key"), false},
		{errors.New("rate limited"), false},
		{errors.New("internal server error"), false},
	}
	for _, c := range cases {
		if got := isContextLimitError(c.err); got != c.want {
			t.Errorf("isContextLimitError(%v) = %v, want %v", c.err, got, c.want)
		}
	}
}

func TestReactiveNonContextError(t *testing.T) {
	r := NewReactive(&stubProvider{text: "summary"})
	st := &core.RunState{Messages: []core.Message{core.UserMessage("x")}}
	orig := errors.New("unauthorized")
	ret := r.OnError(context.Background(), st, orig)
	if ret != orig {
		t.Error("non-context error should pass through unchanged")
	}
}

func TestReactiveCompactsAndHandles(t *testing.T) {
	r := NewReactive(&stubProvider{text: "compressed summary"})
	r.KeepRecent = 1
	// Build a long history to trigger compaction
	msgs := []core.Message{
		core.SystemMessage("sys"),
		core.UserMessage("old1"),
		core.AssistantMessage("resp1"),
		core.UserMessage("old2"),
		core.AssistantMessage("resp2"),
		core.UserMessage("recent"),
	}
	st := &core.RunState{Messages: msgs}
	origLen := len(st.Messages)
	ret := r.OnError(context.Background(), st, errors.New("context length exceeded"))
	if ret != nil {
		t.Errorf("expected nil (handled), got %v", ret)
	}
	// Message count should decrease after compaction
	if len(st.Messages) >= origLen {
		t.Errorf("messages not reduced: %d >= %d", len(st.Messages), origLen)
	}
	// Should contain a summary
	hasSummary := false
	for _, m := range st.Messages {
		if strings.Contains(m.Content, "summary") || strings.Contains(m.Content, "compressed") {
			hasSummary = true
		}
	}
	if !hasSummary {
		t.Error("summary not injected")
	}
}

func TestReactiveCircuitBreaker(t *testing.T) {
	// Provider returns an error (CollectText fails), triggering compaction failure → counts toward the circuit breaker
	r := NewReactive(&erroringProvider{})
	r.maxConsecutive = 2
	st := &core.RunState{Messages: []core.Message{
		core.UserMessage("a"), core.UserMessage("b"), core.UserMessage("c"),
	}}
	ctxErr := errors.New("context length exceeded")
	// First: compaction fails, return original error
	ret1 := r.OnError(context.Background(), st, ctxErr)
	if ret1 == nil {
		t.Error("first should return error (compact failed)")
	}
	// Second: still intervenes (count=1 < 2)
	ret2 := r.OnError(context.Background(), st, ctxErr)
	if ret2 == nil {
		t.Error("second should return error")
	}
	// Third: circuit breaker open (count=2 >= 2), even if the error is context-related it returns directly
	// Manually push consecutive to the threshold
	r.mu.Lock()
	r.consecutive = 2
	r.mu.Unlock()
	ret3 := r.OnError(context.Background(), st, ctxErr)
	if ret3 == nil {
		t.Error("circuit breaker should prevent intervention")
	}
}

// erroringProvider always returns a stream error (causes CollectText to fail).
type erroringProvider struct{}

func (erroringProvider) Stream(ctx context.Context, req core.ModelRequest) (<-chan core.ModelEvent, error) {
	ch := make(chan core.ModelEvent, 1)
	go func() {
		defer close(ch)
		ch <- core.MError{Err: errors.New("summarization model failed")}
	}()
	return ch, nil
}

func TestReactiveResetsOnSuccess(t *testing.T) {
	r := NewReactive(&stubProvider{text: "ok"})
	r.KeepRecent = 1
	// Manually push the counter up
	r.mu.Lock()
	r.consecutive = 1
	r.mu.Unlock()
	st := &core.RunState{Messages: []core.Message{
		core.UserMessage("a"), core.UserMessage("b"), core.UserMessage("c"),
	}}
	_ = r.OnError(context.Background(), st, errors.New("context length exceeded"))
	r.mu.Lock()
	count := r.consecutive
	r.mu.Unlock()
	if count != 0 {
		t.Errorf("consecutive not reset after success: %d", count)
	}
}
