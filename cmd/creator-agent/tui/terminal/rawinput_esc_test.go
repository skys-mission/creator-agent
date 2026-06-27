package terminal

import (
	"io"
	"testing"
	"time"
)

// rawinput_esc_test.go verifies PumpInput's esc-timeout: a lone 0x1b decodes to KeyEsc promptly
// (instead of blocking until the next keypress), complete sequences still decode correctly, and the
// post-timeout probe read reclaims the next byte instead of swallowing it.

// blockingReader delivers bytes pushed on the channel, blocking when none are available; closing the
// channel yields io.EOF. It lets tests drive PumpInput with deterministic timing (push a byte,
// observe an event, push the next) without real wall-clock races.
type blockingReader chan byte

func (r blockingReader) Read(p []byte) (int, error) {
	b, ok := <-r
	if !ok {
		return 0, io.EOF
	}
	p[0] = b
	return 1, nil
}

// withShortEscTimeout lowers escTimeout for the test and restores it on cleanup, so esc-timeout is
// exercised in a few milliseconds instead of 30ms. Tests in this package run serially (none call
// t.Parallel), so overriding the package var is safe.
func withShortEscTimeout(t *testing.T) {
	t.Helper()
	prev := escTimeout
	escTimeout = 5 * time.Millisecond
	t.Cleanup(func() { escTimeout = prev })
}

// TestEscAloneYieldsKeyEscPromptly verifies a lone 0x1b decodes to KeyEsc within escTimeout, rather
// than blocking forever waiting for a following byte (the bug being fixed).
func TestEscAloneYieldsKeyEscPromptly(t *testing.T) {
	withShortEscTimeout(t)
	r := make(blockingReader, 4)
	ch := make(chan Event, 4)
	quit := make(chan struct{})
	defer close(quit)
	go PumpInput(r, ch, quit)

	r <- 0x1b
	select {
	case ev := <-ch:
		ek, ok := ev.(*EventKey)
		if !ok || ek.Key() != KeyEsc {
			t.Fatalf("got %v, want KeyEsc", ev)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for KeyEsc — esc-timeout did not fire")
	}
}

// TestEscSequenceNoFalseEsc verifies a complete arrow sequence \x1b[A decodes to KeyUp, not a
// spurious KeyEsc — the esc-timeout must not fire when the sequence bytes arrive promptly.
func TestEscSequenceNoFalseEsc(t *testing.T) {
	withShortEscTimeout(t)
	r := make(blockingReader, 4)
	ch := make(chan Event, 4)
	quit := make(chan struct{})
	defer close(quit)
	// Pre-load the full sequence so the probe read completes well within escTimeout.
	r <- 0x1b
	r <- '['
	r <- 'A'
	go PumpInput(r, ch, quit)

	select {
	case ev := <-ch:
		ek, ok := ev.(*EventKey)
		if !ok || ek.Key() != KeyUp {
			t.Fatalf("got %v, want KeyUp", ev)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for KeyUp")
	}
}

// TestEscThenByteReclaimsByte verifies the byte-reclaim path: a lone 0x1b yields KeyEsc, and the
// next byte sent afterward must still be delivered as its own event — not swallowed by the timed-out
// probe read. This is the most important correctness test for the pending mechanism.
func TestEscThenByteReclaimsByte(t *testing.T) {
	withShortEscTimeout(t)
	r := make(blockingReader, 4)
	ch := make(chan Event, 4)
	quit := make(chan struct{})
	defer close(quit)
	go PumpInput(r, ch, quit)

	r <- 0x1b
	select {
	case ev := <-ch:
		ek, ok := ev.(*EventKey)
		if !ok || ek.Key() != KeyEsc {
			t.Fatalf("first event got %v, want KeyEsc", ev)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for KeyEsc")
	}

	// Send 'q' after the esc-timeout has fired; it must be reclaimed and delivered as a rune.
	r <- 'q'
	select {
	case ev := <-ch:
		ek, ok := ev.(*EventKey)
		if !ok || ek.Key() != KeyRune || ek.Rune() != 'q' {
			t.Fatalf("second event got %v, want KeyRune('q')", ev)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for reclaimed 'q' byte")
	}
}

// TestQuitDuringPendingReturns verifies PumpInput returns promptly when quit closes while a probe
// read is pending (the reclaim select must respect quit, not strand the loop waiting for a byte).
func TestQuitDuringPendingReturns(t *testing.T) {
	withShortEscTimeout(t)
	r := make(blockingReader, 4)
	ch := make(chan Event, 4)
	quit := make(chan struct{})

	done := make(chan struct{})
	go func() {
		PumpInput(r, ch, quit)
		close(done)
	}()

	r <- 0x1b
	// Wait for KeyEsc so a pending probe read is in flight on the next iteration.
	select {
	case <-ch:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for KeyEsc")
	}

	close(quit)
	select {
	case <-done:
		// PumpInput returned — good.
	case <-time.After(2 * time.Second):
		t.Fatal("PumpInput did not return after quit while a probe read was pending")
	}
}
