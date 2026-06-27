package main

// title_test.go covers the LLM-backed title generator: it builds the right messages (system prompt +
// "Generate a title..." user content), sets Temperature/MaxTokens, and returns the concatenated
// text deltas. Uses an offline mock ModelProvider (no network).

import (
	"context"
	"strings"
	"testing"

	"github.com/skys-mission/creator-agent/core"
)

// titleMockProvider emits a preset sequence of ModelEvents and records the last request.
type titleMockProvider struct {
	lastReq   core.ModelRequest
	events    []core.ModelEvent
	streamErr error
}

func (m *titleMockProvider) Stream(_ context.Context, req core.ModelRequest) (<-chan core.ModelEvent, error) {
	m.lastReq = req
	ch := make(chan core.ModelEvent, len(m.events))
	for _, ev := range m.events {
		ch <- ev
	}
	close(ch)
	return ch, m.streamErr
}

// TestTitleGeneratorBuildsMessagesAndCollectsText verifies the generator constructs the expected
// message shape and returns the concatenated text deltas.
func TestTitleGeneratorBuildsMessagesAndCollectsText(t *testing.T) {
	mp := &titleMockProvider{events: []core.ModelEvent{
		core.MTextDelta{Delta: "Refactor "},
		core.MTextDelta{Delta: "config loader"},
	}}
	gen := &titleGeneratorImpl{provider: func() core.ModelProvider { return mp }}

	out, err := gen.Generate(context.Background(), "help me refactor the config loader")
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if out != "Refactor config loader" {
		t.Errorf("Generate output = %q, want 'Refactor config loader'", out)
	}
	// Message shape: [system=titleSystemPrompt, user="Generate a title...\n"+firstUserMsg].
	if len(mp.lastReq.Messages) != 2 {
		t.Fatalf("request messages = %d, want 2", len(mp.lastReq.Messages))
	}
	if mp.lastReq.Messages[0].Role != core.RoleSystem || !strings.Contains(mp.lastReq.Messages[0].Content, "title generator") {
		t.Errorf("system message wrong: role=%q content=%q", mp.lastReq.Messages[0].Role, mp.lastReq.Messages[0].Content)
	}
	if mp.lastReq.Messages[1].Role != core.RoleUser {
		t.Errorf("user message role = %q, want user", mp.lastReq.Messages[1].Role)
	}
	if !strings.Contains(mp.lastReq.Messages[1].Content, "Generate a title for this conversation") {
		t.Errorf("user message should contain the title instruction; got %q", mp.lastReq.Messages[1].Content)
	}
	if !strings.Contains(mp.lastReq.Messages[1].Content, "help me refactor the config loader") {
		t.Errorf("user message should embed the first user message; got %q", mp.lastReq.Messages[1].Content)
	}
	// Sampling params: low temperature + capped max tokens.
	if mp.lastReq.Temperature == nil || *mp.lastReq.Temperature != titleTemperature {
		t.Errorf("Temperature = %v, want %v", mp.lastReq.Temperature, titleTemperature)
	}
	if mp.lastReq.MaxTokens == nil || *mp.lastReq.MaxTokens != titleMaxTokens {
		t.Errorf("MaxTokens = %v, want %d", mp.lastReq.MaxTokens, titleMaxTokens)
	}
}

// TestTitleGeneratorNilProviderErrors verifies a nil provider (no provider available) returns an
// error rather than panicking.
func TestTitleGeneratorNilProviderErrors(t *testing.T) {
	gen := &titleGeneratorImpl{provider: func() core.ModelProvider { return nil }}
	if _, err := gen.Generate(context.Background(), "hi"); err == nil {
		t.Errorf("expected an error when no provider is available")
	}
}

// TestNewTitleGeneratorNilClosureReturnsNil verifies a nil closure disables the feature.
func TestNewTitleGeneratorNilClosureReturnsNil(t *testing.T) {
	if g := newTitleGenerator(nil); g != nil {
		t.Errorf("newTitleGenerator(nil) should return nil; got %T", g)
	}
}

// TestTitleGeneratorPropagatesStreamError verifies a stream-level error is returned.
func TestTitleGeneratorPropagatesStreamError(t *testing.T) {
	mp := &titleMockProvider{events: []core.ModelEvent{
		core.MError{Err: errNoTitleProvider},
	}}
	gen := &titleGeneratorImpl{provider: func() core.ModelProvider { return mp }}
	if _, err := gen.Generate(context.Background(), "hi"); err == nil {
		t.Errorf("expected a stream error to propagate")
	}
}
