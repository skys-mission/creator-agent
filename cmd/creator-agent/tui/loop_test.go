package tui

import (
	"errors"
	"testing"
)

func TestErrMsgDroppedWhenStreamSuperseded(t *testing.T) {
	a, _ := newAppWithSim(t, 80, 24)
	a.streamGen = 2
	a.status = statusIdle

	handleEvent(a, errMsg{err: errors.New("context canceled"), gen: 1})

	if a.status != statusIdle {
		t.Fatalf("status = %v, want idle after stale errMsg", a.status)
	}
	if a.err != nil {
		t.Fatalf("err = %v, want nil after stale errMsg", a.err)
	}
}

func TestErrMsgAcceptedWhenStreamGenMatches(t *testing.T) {
	a, _ := newAppWithSim(t, 80, 24)
	a.streamGen = 3
	a.status = statusThinking

	want := errors.New("session load failed")
	handleEvent(a, errMsg{err: want, gen: 3})

	if a.status != statusError {
		t.Fatalf("status = %v, want error", a.status)
	}
	if a.err != want {
		t.Fatalf("err = %v, want %v", a.err, want)
	}
}

func TestErrMsgInfraAlwaysShown(t *testing.T) {
	a, _ := newAppWithSim(t, 80, 24)
	a.streamGen = 5
	a.status = statusIdle

	want := errors.New("input pump panicked")
	handleEvent(a, errMsg{err: want}) // gen 0 = infrastructure

	if a.status != statusError {
		t.Fatalf("status = %v, want error for infra errMsg", a.status)
	}
	if a.err != want {
		t.Fatalf("err = %v, want %v", a.err, want)
	}
}
