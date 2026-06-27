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
	var sys []core.Message
	rest := msgs
	if len(rest) > 0 && rest[0].Role == core.RoleSystem {
		sys = []core.Message{rest[0]}
		rest = rest[1:]
	}
	toCompress, recent := core.PartitionForCompact(rest, keepRecentCompact)
	if len(toCompress) == 0 {
		return msgs, false, nil
	}
	summary, serr := summarizeForCompact(ctx, provider, toCompress)
	if serr != nil {
		return nil, false, fmt.Errorf("summarize history: %w", serr)
	}
	compressed := make([]core.Message, 0, len(sys)+1+len(recent))
	compressed = append(compressed, sys...)
	compressed = append(compressed, core.UserMessage("[conversation summary so far]\n"+summary))
	compressed = append(compressed, recent...)
	return compressed, true, nil
}

// summarizeForCompact calls the model to compress old messages into a summary string (mirrors
// middlewares/summarization.summarize + its prompt).
func summarizeForCompact(ctx context.Context, provider core.ModelProvider, msgs []core.Message) (string, error) {
	req := append(append([]core.Message(nil), msgs...), core.UserMessage(summarizePromptText))
	return core.CollectText(ctx, provider, req)
}

const summarizePromptText = `Concisely summarize the conversation above. Preserve:
- The user's goal(s) and any constraints
- Key decisions and their reasons
- File names/paths that were read or modified
- Errors encountered and how they were resolved
- Current task state and any pending next step
Be brief (a few short bullets). Do not include full file contents.`

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
