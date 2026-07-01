//go:build integration

// This file end-to-end tests the openai-responses adapter with a real OpenAI Responses API.
//
// Security: contains no API keys. Keys are read from CREATOR_AGENT_TEST_API_KEY env var, never committed.
// Dual gating (either missing triggers t.Skip):
//  1. build tag: -tags=integration
//  2. env: CREATOR_AGENT_TEST_API_KEY is non-empty
//
// The Responses API is OpenAI-only (no interoperable clones), so this targets the official endpoint
// by default. Run: CREATOR_AGENT_TEST_API_KEY=sk-... go test -tags=integration ./core/adapters/openai-responses/
package openairesponses

import (
	"context"
	"os"
	"testing"
	"time"

	core "github.com/skys-mission/creator-agent/core"
	"github.com/skys-mission/creator-agent/core/adapters/shared"
)

func envDef(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

// TestRealResponses_TextStream verifies a basic text turn against the real Responses API.
func TestRealResponses_TextStream(t *testing.T) {
	key := os.Getenv("CREATOR_AGENT_TEST_API_KEY")
	if key == "" {
		t.Skip("CREATOR_AGENT_TEST_API_KEY not set; Responses real API test skipped (see docs/testing.md)")
	}
	model := envDef("CREATOR_AGENT_TEST_MODEL", "gpt-4o-mini")
	base := envDef("CREATOR_AGENT_TEST_BASE_URL", "https://api.openai.com/v1")
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	p, err := NewProvider(ctx, shared.ProviderConfig{
		BaseURL: base, APIKey: key, Model: model, RequestTimeout: 50 * time.Second,
	})
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}
	ch, err := p.Stream(ctx, core.ModelRequest{Messages: []core.Message{core.UserMessage("Reply with the single word: pong")}})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	var text string
	var hadFinish bool
	for ev := range ch {
		switch e := ev.(type) {
		case core.MTextDelta:
			text += e.Delta
		case core.MFinish:
			hadFinish = true
		case core.MError:
			t.Fatalf("MError: %v", e.Err)
		}
	}
	if text == "" {
		t.Fatal("empty text response")
	}
	if !hadFinish {
		t.Error("no finish event")
	}
}
