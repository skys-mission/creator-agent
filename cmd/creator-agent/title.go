package main

// title.go implements the LLM-backed session title generator. It wraps a one-shot model call against
// the current provider (read through a closure so /model and /variants switches are picked up with
// no re-injection) and exposes it as a tui.TitleGenerator. The TUI calls it asynchronously after a
// session's first user message; the result is persisted via SessionStore.SaveWithMeta.

import (
	"context"
	"strings"

	"github.com/skys-mission/creator-agent/cmd/creator-agent/tui"
	"github.com/skys-mission/creator-agent/core"
)

// titleSystemPrompt instructs the model to output ONLY a short, single-line conversation title.
// Adapted from opencode's title-agent prompt: same language as the user, no tool names, no
// explanations, <=50 chars guidance (the caller hard-truncates at 100).
const titleSystemPrompt = `You are a title generator. You output ONLY a thread title. Nothing else.

Generate a brief title that would help the user find this conversation later.

Rules:
- Output a single line, <=50 characters, no explanations.
- Use the SAME language as the user message.
- Title must read naturally; no word salad.
- Never include tool names (e.g. "read tool", "bash tool").
- Focus on the main topic or question.
- Keep exact: technical terms, numbers, filenames, HTTP codes.
- Remove filler words (the, this, my, a, an).
- Never use tools. Never say you cannot generate a title.
- Always output something meaningful, even if the input is minimal.
- For short/conversational input (hello, hey, lol), reflect the tone (e.g. Greeting, Quick check-in).

Examples:
"debug 500 errors in production" -> Debugging production 500 errors
"refactor user service" -> Refactoring user service
"why is app.js failing" -> app.js failure investigation
"how do I connect postgres to my API" -> Postgres API connection
"@src/auth.ts add refresh token support" -> Auth refresh token support`

// titleTemperature is the sampling temperature for title generation (matches opencode's title agent).
const titleTemperature = 0.5

// titleMaxTokens caps the title output so a runaway model can't produce a long essay.
const titleMaxTokens = 64

// titleGeneratorImpl implements tui.TitleGenerator by calling the current provider once. It reads
// the provider through a closure so a single instance tracks /model and /variants switches (which
// rebuild the provider) without re-injection.
type titleGeneratorImpl struct {
	provider func() core.ModelProvider
}

// Generate makes a single non-tool model call to produce a title from the first user message.
// It calls provider.Stream directly (rather than core.CollectText) so it can set Temperature and
// MaxTokens. Returns the raw title text; cleaning/truncation is the caller's responsibility.
func (g *titleGeneratorImpl) Generate(ctx context.Context, firstUserMsg string) (string, error) {
	p := g.provider()
	if p == nil {
		return "", errNoTitleProvider
	}
	temp := float32(titleTemperature)
	maxTok := titleMaxTokens
	msgs := []core.Message{
		core.SystemMessage(titleSystemPrompt),
		core.UserMessage("Generate a title for this conversation:\n" + firstUserMsg),
	}
	stream, err := p.Stream(ctx, core.ModelRequest{
		Messages:    msgs,
		Temperature: &temp,
		MaxTokens:   &maxTok,
	})
	if err != nil {
		return "", err
	}
	var sb strings.Builder
	for ev := range stream {
		switch e := ev.(type) {
		case core.MTextDelta:
			sb.WriteString(e.Delta)
		case core.MError:
			if e.Err != nil {
				return "", e.Err
			}
		}
	}
	out := strings.TrimSpace(sb.String())
	return out, nil
}

// errNoTitleProvider is returned when no provider is available (title generation skipped).
var errNoTitleProvider = &titleErr{"no model provider available for title generation"}

type titleErr struct{ msg string }

func (e *titleErr) Error() string { return e.msg }

// newTitleGenerator builds a tui.TitleGenerator that reads the provider through the given closure.
// Returns nil when the closure is nil (disables auto-titling gracefully).
func newTitleGenerator(providerFn func() core.ModelProvider) tui.TitleGenerator {
	if providerFn == nil {
		return nil
	}
	return &titleGeneratorImpl{provider: providerFn}
}
