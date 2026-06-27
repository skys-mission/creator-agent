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
	sender   func(msg any) // pushes a message into the App event queue; nil before SetSender
	allowSet map[string]bool
}

func newAsyncApprover() *asyncApprover {
	return &asyncApprover{allowSet: make(map[string]bool)}
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
	a.mu.Lock()
	if a.allowSet[middlewares.ApproveKey(toolName, input)] {
		a.mu.Unlock()
		return true
	}
	if toolName == "write" || toolName == "edit" {
		if a.allowSet["write"] || a.allowSet["edit"] {
			a.mu.Unlock()
			return true
		}
	}
	a.mu.Unlock()

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
	a.mu.Lock()
	a.allowSet[middlewares.ApproveKey(toolName, input)] = true
	a.mu.Unlock()
}

func (a *asyncApprover) rememberAllEdits() {
	a.mu.Lock()
	a.allowSet["write"] = true
	a.allowSet["edit"] = true
	a.mu.Unlock()
}

var _ middlewares.AskResolver = (*asyncApprover)(nil).approve
