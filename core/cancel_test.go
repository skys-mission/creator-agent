package core

import (
	"context"
	"testing"
)

// TestCancelBeforeStart verifies: if ctx is already canceled before the loop starts, FinishCanceled is emitted without calling the model.
func TestCancelBeforeStart(t *testing.T) {
	mock := &mockProvider{turns: [][]ModelEvent{
		{MTextDelta{Delta: "should-not-reach"}},
	}}
	ag := NewAgent(mock, WithMaxSteps(5)).(*agent)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel before start

	events, err := ag.Stream(ctx, PromptInput("", "x"))
	if err != nil {
		t.Fatal(err)
	}
	var finish string
	for ev := range events {
		if f, ok := ev.(FinishEvent); ok {
			finish = string(f.Reason)
		}
	}
	if finish != "canceled" {
		t.Errorf("finish = %q, want canceled", finish)
	}
	// The model should not be called (call should be 0 because the loop exits immediately).
	if mock.call != 0 {
		t.Errorf("model called %d times, want 0", mock.call)
	}
}

// TestCancelNotTriggeredOnNormal verifies: normal completion still yields stop (cancel logic does not break the normal path).
func TestCancelNotTriggeredOnNormal(t *testing.T) {
	mock := &mockProvider{turns: [][]ModelEvent{
		{MTextDelta{Delta: "hi"}},
	}}
	ag := NewAgent(mock, WithMaxSteps(5)).(*agent)

	events, _ := ag.Stream(context.Background(), PromptInput("", "x"))
	var finish string
	for ev := range events {
		if f, ok := ev.(FinishEvent); ok {
			finish = string(f.Reason)
		}
	}
	if finish != "stop" {
		t.Errorf("finish = %q, want stop (normal path broken)", finish)
	}
}
