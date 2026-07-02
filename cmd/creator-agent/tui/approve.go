package tui

import (
	"context"
	"sync"

	"github.com/skys-mission/creator-agent/core/middlewares"
)

type askMsg struct {
	toolName string
	input    string
	replyCh  chan bool
}

type asyncApprover struct {
	mu       sync.RWMutex
	sender   func(msg any)         // pushes a message into the App event queue; nil before SetSender
	allowSet *middlewares.AllowSet // session-level approved invocations (shared type with the REPL approver)
}

func newAsyncApprover() *asyncApprover {
	return &asyncApprover{allowSet: middlewares.NewAllowSet()}
}

// SetSender injects the event-queue sender after assembly (the approver is built before the App).
func (a *asyncApprover) SetSender(sender func(msg any)) {
	a.mu.Lock()
	a.sender = sender
	a.mu.Unlock()
}

// senderSnapshot returns the current sender under the read lock.
func (a *asyncApprover) senderSnapshot() func(msg any) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.sender
}

func (a *asyncApprover) approve(ctx context.Context, toolName, input string) bool {
	if a.allowSet.Allowed(toolName, input) {
		return true
	}

	sender := a.senderSnapshot()
	if sender == nil {
		return false // assembly race window: fail closed, do not block
	}

	replyCh := make(chan bool, 1)
	sender(askMsg{toolName: toolName, input: input, replyCh: replyCh})
	select {
	case allow := <-replyCh:
		return allow
	case <-ctx.Done():
		return false
	}
}

func (a *asyncApprover) remember(toolName, input string) {
	a.allowSet.RememberExact(toolName, input)
}

func (a *asyncApprover) rememberAllEdits() {
	a.allowSet.RememberWriteEdit()
}

var _ middlewares.AskResolver = (*asyncApprover)(nil).approve
