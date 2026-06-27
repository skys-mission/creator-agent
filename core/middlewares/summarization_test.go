package middlewares

import (
	"context"
	"strings"
	"testing"

	"github.com/skys-mission/creator-agent/core"
)

// mockProvider returns a preset text stream.
type mockProvider struct{ text string }

func (m *mockProvider) Stream(ctx context.Context, req core.ModelRequest) (<-chan core.ModelEvent, error) {
	ch := make(chan core.ModelEvent, 1)
	ch <- core.MTextDelta{Delta: m.text}
	close(ch)
	return ch, nil
}

// TestSummarizationCompress verifies compression above the threshold, keeping system + summary + recent N messages.
func TestSummarizationCompress(t *testing.T) {
	mock := &mockProvider{text: "SUMMARY"}
	s := &Summarization{
		Model:      mock,
		Threshold:  10, // low threshold to force trigger
		KeepRecent: 2,
	}
	st := &core.RunState{
		Messages: []core.Message{
			core.SystemMessage("sys"),
			core.UserMessage(strings.Repeat("a", 50)),      // old (to be compressed)
			core.AssistantMessage(strings.Repeat("b", 50)), // old
			core.UserMessage("recent1"),                    // kept
			core.AssistantMessage("recent2"),               // kept
		},
	}
	if err := s.BeforeModel(context.Background(), st); err != nil {
		t.Fatalf("BeforeModel: %v", err)
	}

	// Expected: system + summary + recent1 + recent2 = 4 messages
	if len(st.Messages) != 4 {
		t.Fatalf("messages len = %d, want 4 (sys+summary+2 recent). got: %v", len(st.Messages), st.Messages)
	}
	if st.Messages[0].Role != core.RoleSystem {
		t.Errorf("msg[0] role = %v, want system", st.Messages[0].Role)
	}
	if !strings.Contains(st.Messages[1].Content, "SUMMARY") {
		t.Errorf("msg[1] should be summary, got: %q", st.Messages[1].Content)
	}
	if st.Messages[2].Content != "recent1" || st.Messages[3].Content != "recent2" {
		t.Errorf("recent messages not preserved: %q %q", st.Messages[2].Content, st.Messages[3].Content)
	}
}

// TestSummarizationNoOpBelowThreshold verifies no compression when below the threshold.
func TestSummarizationNoOpBelowThreshold(t *testing.T) {
	s := &Summarization{
		Model:      &mockProvider{text: "X"},
		Threshold:  10000,
		KeepRecent: 2,
	}
	original := []core.Message{
		core.UserMessage("hi"),
		core.AssistantMessage("hello"),
	}
	st := &core.RunState{Messages: original}
	if err := s.BeforeModel(context.Background(), st); err != nil {
		t.Fatal(err)
	}
	if len(st.Messages) != len(original) {
		t.Errorf("should not compress below threshold; got %d msgs", len(st.Messages))
	}
}

// TestSummarizationShortHistory verifies that short history (<= KeepRecent) is not compressed.
func TestSummarizationShortHistory(t *testing.T) {
	s := &Summarization{
		Model:      &mockProvider{text: "X"},
		Threshold:  1,
		KeepRecent: 6,
	}
	st := &core.RunState{Messages: []core.Message{core.UserMessage("a"), core.AssistantMessage("b")}}
	if err := s.BeforeModel(context.Background(), st); err != nil {
		t.Fatal(err)
	}
	if len(st.Messages) != 2 {
		t.Errorf("short history should not be compressed; got %d", len(st.Messages))
	}
}
