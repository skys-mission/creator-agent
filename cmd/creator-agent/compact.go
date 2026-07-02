package main

// compact.go implements manual conversation compaction (/compact). It reuses the same approach as
// core/middlewares/summarization.go: partition the history into (system + old) + recent, summarize
// the old part via a one-shot model call, and replace the session's stored messages with
// [system, summary, recent]. Exposed to the TUI as a factory (WithCompactFactory) so the tui
// package has no direct dependency on core/middlewares or the model provider.

import (
	"context"
	"fmt"

	"github.com/skys-mission/creator-agent/cmd/creator-agent/tui"
	"github.com/skys-mission/creator-agent/core"
)

// keepRecentCompact is how many recent messages manual /compact preserves verbatim (mirrors the
// Summarization middleware's default).
const keepRecentCompact = 6

// minMessagesToCompact is the minimum history length for a manual compact to do anything useful.
const minMessagesToCompact = keepRecentCompact + 2

// compactMessages computes the compacted message list for the given history using the provider:
// it preserves the leading system message, summarizes the older messages into a single summary
// user message, and keeps the recent tail. ok is false (and out is the unchanged input) when the
// history is too short to compact. Errors come from the model call.
func compactMessages(ctx context.Context, provider core.ModelProvider, msgs []core.Message) (out []core.Message, ok bool, err error) {
	if len(msgs) < minMessagesToCompact {
		return msgs, false, nil
	}
	// Delegate to the shared compaction primitive (same partitioning + prompt as the Summarization
	// middleware), so manual /compact and automatic compaction never drift apart.
	compressed, did, cerr := core.CompressHistory(ctx, provider, msgs, keepRecentCompact, core.DefaultSummarizePrompt)
	if cerr != nil {
		return nil, false, fmt.Errorf("summarize history: %w", cerr)
	}
	if !did {
		return msgs, false, nil
	}
	return compressed, true, nil
}

// newCompactFactory builds a tui.Compactor that compacts a session's stored history. It reads the
// provider live (so /model switches are picked up) and persists the compacted messages via the
// store. Returns nil when the provider closure is nil (compaction unavailable).
func newCompactFactory(providerFn func() core.ModelProvider, store core.SessionStore) tui.Compactor {
	if providerFn == nil {
		return nil
	}
	return func(ctx context.Context, sessionID string) ([]core.Message, error) {
		p := providerFn()
		if p == nil {
			return nil, fmt.Errorf("no model provider available for compaction")
		}
		msgs, err := store.Load(sessionID)
		if err != nil {
			return nil, fmt.Errorf("load session %q: %w", sessionID, err)
		}
		out, ok, cerr := compactMessages(ctx, p, msgs)
		if cerr != nil {
			return nil, cerr
		}
		if !ok {
			return msgs, nil
		}
		if serr := store.SaveWithMeta(sessionID, "", out); serr != nil {
			return nil, fmt.Errorf("save compacted session %q: %w", sessionID, serr)
		}
		return out, nil
	}
}
