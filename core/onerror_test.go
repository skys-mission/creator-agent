package core

import (
	"context"
	"errors"
	"testing"
)

// errorOnceProvider returns an error on the first call, then delegates to the embedded mockProvider.
type errorOnceProvider struct {
	mockProvider
	calls int
}

func (p *errorOnceProvider) Stream(ctx context.Context, req ModelRequest) (<-chan ModelEvent, error) {
	p.calls++
	if p.calls == 1 {
		return nil, errors.New("request too large: context length exceeded")
	}
	return p.mockProvider.Stream(ctx, req)
}

// handlerMiddleware intercepts OnError: returns nil (handled) and records it was called.
type handlerMiddleware struct {
	BaseMiddleware
	called int
}

func (h *handlerMiddleware) OnError(_ context.Context, _ *RunState, _ error) error {
	h.called++
	return nil // handled
}

// TestOnErrorRetriesAfterHandled verifies: when the model errors, OnError middleware is invoked;
// returning nil causes the loop to retry.
func TestOnErrorRetriesAfterHandled(t *testing.T) {
	mock := &errorOnceProvider{
		mockProvider: mockProvider{turns: [][]ModelEvent{
			{MTextDelta{Delta: "recovered"}},
		}},
	}
	hook := &handlerMiddleware{}
	ag := NewAgent(mock, WithMiddlewares(hook), WithMaxSteps(5)).(*agent)

	events, err := ag.Stream(context.Background(), PromptInput("", "x"))
	if err != nil {
		t.Fatal(err)
	}
	var finish string
	for ev := range events {
		if f, ok := ev.(FinishEvent); ok {
			finish = string(f.Reason)
		}
	}
	if hook.called != 1 {
		t.Errorf("OnError called %d times, want 1", hook.called)
	}
	if finish != "stop" {
		t.Errorf("finish = %q, want stop (should recover via OnError)", finish)
	}
}

// TestOnErrorAbortsWhenUnhandled verifies: OnError returning an error causes the loop to abort and emit ErrorEvent.
type passthroughMiddleware struct {
	BaseMiddleware
	called int
}

func (p *passthroughMiddleware) OnError(_ context.Context, _ *RunState, err error) error {
	p.called++
	return err // not handled, pass through
}

func TestOnErrorAbortsWhenUnhandled(t *testing.T) {
	mock2 := &alwaysErrorProvider{}
	hook := &passthroughMiddleware{}
	ag := NewAgent(mock2, WithMiddlewares(hook), WithMaxSteps(5)).(*agent)

	events, _ := ag.Stream(context.Background(), PromptInput("", "x"))
	var gotErr bool
	for ev := range events {
		if _, ok := ev.(ErrorEvent); ok {
			gotErr = true
		}
	}
	if !gotErr {
		t.Error("expected ErrorEvent when OnError returns error")
	}
	if hook.called != 1 {
		t.Errorf("OnError called %d times, want 1", hook.called)
	}
}

type alwaysErrorProvider struct{}

func (alwaysErrorProvider) Stream(ctx context.Context, req ModelRequest) (<-chan ModelEvent, error) {
	return nil, errors.New("persistent failure")
}

// TestOnErrorFirstHandlerWins verifies: with multiple OnError middlewares, the first returning nil wins and the rest are skipped.
type firstHandler struct {
	BaseMiddleware
	called int
}
type secondHandler struct {
	BaseMiddleware
	called int
}

func (h *firstHandler) OnError(_ context.Context, _ *RunState, _ error) error {
	h.called++
	return nil
}
func (h *secondHandler) OnError(_ context.Context, _ *RunState, _ error) error {
	h.called++
	return errors.New("second")
}

func TestOnErrorFirstHandlerWins(t *testing.T) {
	mock := &errorOnceProvider{
		mockProvider: mockProvider{turns: [][]ModelEvent{
			{MTextDelta{Delta: "ok"}},
		}},
	}
	first := &firstHandler{}
	second := &secondHandler{}
	ag := NewAgent(mock, WithMiddlewares(first, second), WithMaxSteps(5)).(*agent)

	events, _ := ag.Stream(context.Background(), PromptInput("", "x"))
	for range events {
	}
	if first.called != 1 {
		t.Errorf("first OnError called %d times, want 1", first.called)
	}
	if second.called != 0 {
		t.Errorf("second OnError called %d times, want 0 (first should win)", second.called)
	}
}

// rateLimitedProvider always fails with a RateLimitedError (429). After
// openStreamWithRetry exhausts its retries, the error reaches the OnError chain.
type rateLimitedProvider struct{}

func (rateLimitedProvider) Stream(ctx context.Context, req ModelRequest) (<-chan ModelEvent, error) {
	return nil, &RateLimitedError{Err: errors.New("too many requests"), StatusCode: 429}
}

// contractViolator returns a BARE error that does NOT wrap the original,
// simulating a middleware that breaks the OnError %w contract.
type contractViolator struct {
	BaseMiddleware
	called int
}

func (c *contractViolator) OnError(_ context.Context, _ *RunState, _ error) error {
	c.called++
	// Contract violation: returns a fresh error instead of wrapping the original.
	// errors.Is(this, original) is false, so the loop's guard must keep the original.
	return errors.New("totally unrelated error")
}

// TestOnErrorChainPreservesErrorType pins the contract guard: when a middleware
// returns an error that does not wrap the original (errors.Is fails), the loop
// keeps the original error so downstream errors.As classification (e.g. rate
// limit → user hint, retry decisions) keeps working instead of being swallowed.
func TestOnErrorChainPreservesErrorType(t *testing.T) {
	violator := &contractViolator{}
	ag := NewAgent(rateLimitedProvider{}, WithMiddlewares(violator), WithMaxSteps(5)).(*agent)

	events, _ := ag.Stream(context.Background(), PromptInput("", "x"))
	var finalErr error
	for ev := range events {
		if e, ok := ev.(ErrorEvent); ok {
			finalErr = e.Err
		}
	}
	if finalErr == nil {
		t.Fatal("expected ErrorEvent, got none")
	}
	// The original RateLimitedError must survive so errors.As can still classify it.
	var rl *RateLimitedError
	if !errors.As(finalErr, &rl) {
		t.Errorf("RateLimitedError type lost in OnError chain (finalErr=%v); contract guard should preserve the original", finalErr)
	}
	if violator.called != 1 {
		t.Errorf("violator OnError called %d times, want 1", violator.called)
	}
}
