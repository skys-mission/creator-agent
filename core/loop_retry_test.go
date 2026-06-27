package core

// loop_retry_test.go covers openStreamWithRetry transient-error retry logic:
// - transient errors (429/5xx) retried then succeed -> no error surfaced to user
// - persistent transient errors exhausting retries -> last error returned
// - client errors (4xx excluding 429) not retried, returned immediately
// - ctx cancel exits immediately without waiting for backoff

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

// flakyProvider returns preset errors for the first N Stream calls, then a successful stream.
type flakyProvider struct {
	errs []error      // errors for the first len(errs) calls
	call int32        // call counter
	ok   []ModelEvent // events emitted on success
}

func (p *flakyProvider) Stream(ctx context.Context, req ModelRequest) (<-chan ModelEvent, error) {
	n := int(atomic.AddInt32(&p.call, 1))
	if n <= len(p.errs) && p.errs[n-1] != nil {
		return nil, p.errs[n-1]
	}
	ch := make(chan ModelEvent, len(p.ok))
	go func() {
		defer close(ch)
		for _, e := range p.ok {
			ch <- e
		}
	}()
	return ch, nil
}

// transientErr returns a RateLimitedError (429).
func transientErr() error {
	return &RateLimitedError{Err: errors.New("too many requests"), StatusCode: 429}
}

// fatalErr returns a ClientError (401, not retryable).
func fatalErr() error {
	return &ClientError{Err: errors.New("unauthorized"), StatusCode: 401}
}

// TestRetryTransientThenSucceed: first call 429, second succeeds -> stream recovered, 2 total calls.
func TestRetryTransientThenSucceed(t *testing.T) {
	p := &flakyProvider{
		errs: []error{transientErr()},
		ok:   []ModelEvent{MTextDelta{Delta: "recovered"}},
	}
	a := &agent{cfg: AgentConfig{Model: p}}
	req := ModelRequest{Messages: []Message{UserMessage("hi")}}

	stream, err := a.openStreamWithRetry(context.Background(), req)
	if err != nil {
		t.Fatalf("expected recovery, got: %v", err)
	}
	var got string
	for ev := range stream {
		if d, ok := ev.(MTextDelta); ok {
			got += d.Delta
		}
	}
	if got != "recovered" {
		t.Errorf("stream content = %q, want recovered", got)
	}
	if calls := atomic.LoadInt32(&p.call); calls != 2 {
		t.Errorf("provider called %d times, want 2 (1 fail + 1 success)", calls)
	}
}

// TestRetryExhausted: persistent 429 beyond retry limit -> error returned, total calls = limit+1.
func TestRetryExhausted(t *testing.T) {
	p := &flakyProvider{
		errs: []error{transientErr(), transientErr(), transientErr(), transientErr()},
	}
	a := &agent{cfg: AgentConfig{Model: p}}
	req := ModelRequest{Messages: []Message{UserMessage("hi")}}

	_, err := a.openStreamWithRetry(context.Background(), req)
	if err == nil {
		t.Fatal("expected error after retries exhausted")
	}
	// transientRetryMax=2 -> total calls = 2+1 = 3
	if calls := atomic.LoadInt32(&p.call); calls != int32(transientRetryMax+1) {
		t.Errorf("provider called %d times, want %d", calls, transientRetryMax+1)
	}
}

// TestNoRetryOnClientError: client error (401) not retried, returned immediately, 1 call.
func TestNoRetryOnClientError(t *testing.T) {
	p := &flakyProvider{
		errs: []error{fatalErr()},
	}
	a := &agent{cfg: AgentConfig{Model: p}}
	req := ModelRequest{Messages: []Message{UserMessage("hi")}}

	_, err := a.openStreamWithRetry(context.Background(), req)
	if err == nil {
		t.Fatal("expected client error to surface")
	}
	if calls := atomic.LoadInt32(&p.call); calls != 1 {
		t.Errorf("client error should not retry, called %d times, want 1", calls)
	}
}

// TestRetryRespectsCancel: if ctx is canceled during backoff, return immediately without waiting the full backoff.
func TestRetryRespectsCancel(t *testing.T) {
	p := &flakyProvider{
		errs: []error{transientErr(), transientErr(), transientErr(), transientErr()},
	}
	a := &agent{cfg: AgentConfig{Model: p}}
	req := ModelRequest{Messages: []Message{UserMessage("hi")}}

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()
	start := time.Now()
	_, err := a.openStreamWithRetry(ctx, req)
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("expected error (ctx canceled)")
	}
	// Should return well before the full backoff (1s+3s=4s).
	if elapsed > 1*time.Second {
		t.Errorf("retry should respect cancel quickly, took %v", elapsed)
	}
}

// TestIsTransientError boundary checks.
func TestIsTransientError(t *testing.T) {
	if !isTransientError(&RateLimitedError{StatusCode: 429}) {
		t.Error("429 should be transient")
	}
	if !isTransientError(&ServerError{StatusCode: 503}) {
		t.Error("5xx should be transient")
	}
	if isTransientError(&ClientError{StatusCode: 401}) {
		t.Error("4xx (non-429) should NOT be transient")
	}
	if isTransientError(errors.New("some network blip")) {
		t.Error("unclassified error should NOT be transient (conservative)")
	}
	if isTransientError(context.Canceled) {
		t.Error("canceled should NOT be transient")
	}
}
