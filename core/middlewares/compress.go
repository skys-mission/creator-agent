package middlewares

import (
	"context"

	"github.com/skys-mission/creator-agent/core"
)

// compressHistory summarizes the older portion of a message history into a single
// "[conversation summary so far]" user message and reassembles [system, summary, recent].
//
// Shared by Summarization (predictive, BeforeModel) and Reactive (OnError). Both run the same
// pipeline — separate the leading system message, partition the rest into compressible/recent via
// core.PartitionForCompact (pair-aware), summarize the compressible part through the model, then
// reassemble. They differ only in trigger, KeepRecent, prompt, and how they treat "nothing to
// compress"; those policies stay in the callers, which is why this helper returns a compressed
// flag instead of deciding.
//
// compressed=false means partitioning yielded nothing to summarize (no model call made); the caller
// maps that to its own policy (Summarization no-ops, Reactive counts it toward its circuit breaker).
//
// The implementation is the shared core.CompressHistory primitive; this thin wrapper keeps the
// package-local call sites unchanged.
func compressHistory(ctx context.Context, model core.ModelProvider, msgs []core.Message, keepRecent int, prompt string) (out []core.Message, compressed bool, err error) {
	return core.CompressHistory(ctx, model, msgs, keepRecent, prompt)
}
