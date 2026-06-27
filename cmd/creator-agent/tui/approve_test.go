package tui

import (
	"context"
	"testing"
)

func TestAsyncApproverRememberAllEditsSkipsOtherPaths(t *testing.T) {
	ap := newAsyncApprover()
	ap.rememberAllEdits()

	if !ap.approve(context.Background(), "write", `{"path":"/a.txt"}`) {
		t.Fatal("session-wide write allow should skip prompt for other paths")
	}
	if !ap.approve(context.Background(), "edit", `{"path":"/b.txt"}`) {
		t.Fatal("session-wide edit allow should skip prompt for other paths")
	}
}

func TestAsyncApproverRememberSinglePathDoesNotAllowOtherPaths(t *testing.T) {
	ap := newAsyncApprover()
	ap.remember("write", `{"path":"/a.txt"}`)

	prompted := make(chan struct{}, 1)
	ap.mu.Lock()
	ap.sender = func(msg any) {
		if m, ok := msg.(askMsg); ok {
			prompted <- struct{}{}
			m.replyCh <- false
		}
	}
	ap.mu.Unlock()

	if ap.approve(context.Background(), "write", `{"path":"/b.txt"}`) {
		t.Fatal("path-specific remember should not allow other paths")
	}
	select {
	case <-prompted:
	default:
		t.Fatal("path-specific remember should still prompt for other paths")
	}
}
