//go:build integration

// This file end-to-end tests the openai adapter with a real model API (provider layer).
//
// Security: contains no API keys. Keys are read from CREATOR_AGENT_TEST_API_KEY env var, never committed.
// Default skipped: `go test ./...` / CI skip this file because of the integration build tag (zero network).
// See docs/testing.md for how to run.
//
// Dual gating (either missing triggers t.Skip):
//  1. build tag: -tags=integration
//  2. env: CREATOR_AGENT_TEST_API_KEY is non-empty
//
// This ensures even if -tags=integration is accidentally added locally without a key,
// no real network calls are made (avoiding leaked test footprint / accidental quota usage).
package openai

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	core "github.com/skys-mission/creator-agent/core"
)

// testEnv wraps real API config read from environment variables.
// ok=false when the key is missing (triggers Skip).
type testEnv struct {
	apiKey  string
	baseURL string
	model   string
}

// loadTestEnv reads and validates test env. Missing key -> ok=false (triggers Skip).
func loadTestEnv() (testEnv, bool) {
	k := os.Getenv("CREATOR_AGENT_TEST_API_KEY")
	if k == "" {
		return testEnv{}, false
	}
	return testEnv{
		apiKey:  k,
		baseURL: envOr("CREATOR_AGENT_TEST_BASE_URL", "https://api.deepseek.com"),
		model:   envOr("CREATOR_AGENT_TEST_MODEL", "deepseek-v4-flash"),
	}, true
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

// requireEnv is the shared Skip gate: skips when no key is present, otherwise returns provider + ctx.
func requireEnv(t *testing.T) (*Provider, context.Context) {
	t.Helper()
	env, ok := loadTestEnv()
	if !ok {
		t.Skip("CREATOR_AGENT_TEST_API_KEY not set; real API integration test skipped (see docs/testing.md)")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	p, err := NewProvider(ctx, Config{
		BaseURL:        env.baseURL,
		APIKey:         env.apiKey,
		Model:          env.model,
		RequestTimeout: 50 * time.Second,
	})
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}
	return p, ctx
}

// requireStream calls provider.Stream once, collects all events, and fails on error.
func requireStream(t *testing.T, p *Provider, ctx context.Context, req core.ModelRequest) []core.ModelEvent {
	t.Helper()
	ch, err := p.Stream(ctx, req)
	if err != nil {
		t.Fatalf("Stream setup failed: %v", err)
	}
	var evs []core.ModelEvent
	for ev := range ch {
		evs = append(evs, ev)
	}
	return evs
}

// countText concatenates text deltas from the stream (ignoring thinking / refusal).
func countText(evs []core.ModelEvent) string {
	var sb []byte
	for _, ev := range evs {
		if d, ok := ev.(core.MTextDelta); ok {
			sb = append(sb, d.Delta...)
		}
	}
	return string(sb)
}

// hasError reports whether the stream contains an MError.
func hasError(evs []core.ModelEvent) (error, bool) {
	for _, ev := range evs {
		if e, ok := ev.(core.MError); ok && e.Err != nil {
			return e.Err, true
		}
	}
	return nil, false
}

// =====================================================================
// Real API tests: provider layer
// =====================================================================

// TestRealStreamBasicText: simplest case -- one-sentence prompt, verify non-empty text + clean finish (no MError).
func TestRealStreamBasicText(t *testing.T) {
	p, ctx := requireEnv(t)
	evs := requireStream(t, p, ctx, core.ModelRequest{
		Messages: []core.Message{core.UserMessage("用一个词回答：天空是什么颜色？")},
	})
	if err, ok := hasError(evs); ok {
		t.Fatalf("received MError: %v", err)
	}
	if txt := countText(evs); len(txt) == 0 {
		t.Error("expected text output, got empty")
	}
}

// TestRealStreamUsage: verify usage statistics (last chunk carries it, StreamOptions.IncludeUsage=true).
// Billing-related: must verify token counts > 0.
func TestRealStreamUsage(t *testing.T) {
	p, ctx := requireEnv(t)
	evs := requireStream(t, p, ctx, core.ModelRequest{
		Messages: []core.Message{core.UserMessage("数到 5。")},
	})
	var usage *core.Usage
	for _, ev := range evs {
		if u, ok := ev.(core.MUsage); ok {
			uu := u.Usage
			usage = &uu
		}
	}
	if usage == nil {
		t.Fatal("no MUsage received (IncludeUsage may be broken or endpoint does not support usage stats)")
	}
	if usage.InputTokens == 0 {
		t.Errorf("InputTokens = 0, expected > 0")
	}
	t.Logf("usage: in=%d out=%d", usage.InputTokens, usage.OutputTokens)
}

// TestRealStreamReasoning: verify reasoning_content (DeepSeek thinking delta).
// Note: non-reasoning models may not return reasoning; this test asserts "no error" + "has text" --
// if reasoning is present it is logged additionally.
func TestRealStreamReasoning(t *testing.T) {
	p, ctx := requireEnv(t)
	evs := requireStream(t, p, ctx, core.ModelRequest{
		Messages: []core.Message{core.UserMessage("逐步推理：17 * 23 等于多少？")},
	})
	if err, ok := hasError(evs); ok {
		t.Fatalf("MError: %v", err)
	}
	var reasoning, text string
	for _, ev := range evs {
		switch e := ev.(type) {
		case core.MThinkingDelta:
			reasoning += e.Delta
		case core.MTextDelta:
			text += e.Delta
		}
	}
	t.Logf("reasoning length=%d, text=%q", len(reasoning), text)
	if text == "" {
		t.Error("expected text answer")
	}
}

// TestRealStreamMultiTurn: verify multi-turn context (system + history + new question) is understood by the endpoint.
// Smart assertion: first round "my name is Tester", second round asks "what is my name", assert answer contains "Tester".
func TestRealStreamMultiTurn(t *testing.T) {
	p, ctx := requireEnv(t)
	msgs := []core.Message{
		core.SystemMessage("你是一个简洁的助手，用最少的词回答。"),
		core.UserMessage("记住：我的名字是测试员。"),
		core.AssistantMessage("好的，记住了。"),
		core.UserMessage("我叫什么名字？"),
	}
	evs := requireStream(t, p, ctx, core.ModelRequest{Messages: msgs})
	if err, ok := hasError(evs); ok {
		t.Fatalf("MError: %v", err)
	}
	txt := countText(evs)
	t.Logf("answer: %q", txt)
	if !containsHan(txt, "测试") {
		t.Errorf("multi-turn context broken: answer should contain '测试', got %q", txt)
	}
}

// TestRealStreamBadKey: verify bad key -> ClientError (401/403, no panic, no infinite hang).
// Uses an intentionally wrong key against the same endpoint.
func TestRealStreamBadKey(t *testing.T) {
	env, ok := loadTestEnv()
	if !ok {
		t.Skip("needs CREATOR_AGENT_TEST_API_KEY to determine endpoint")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	p, err := NewProvider(ctx, Config{
		BaseURL: env.baseURL,
		APIKey:  "sk-invalid-key-for-test-xxxxxxxxxxxx",
		Model:   env.model,
	})
	if err != nil {
		t.Fatal(err)
	}
	evs := requireStream(t, p, ctx, core.ModelRequest{
		Messages: []core.Message{core.UserMessage("hi")},
	})
	e, ok := hasError(evs)
	if !ok {
		t.Fatal("bad key should return MError, got none (endpoint may not validate key?)")
	}
	// Should be classified as ClientError (4xx)
	var ce *core.ClientError
	if !errors.As(e, &ce) {
		t.Errorf("bad key should be ClientError, got %T: %v", e, e)
	} else if ce.StatusCode < 400 || ce.StatusCode >= 500 {
		t.Errorf("StatusCode=%d, expected 4xx", ce.StatusCode)
	}
	t.Logf("bad key correctly classified: %v", e)
}

// containsHan loosely checks for a Chinese substring (avoids fragile exact-match assertions).
func containsHan(s, sub string) bool {
	return len(s) >= len(sub) && indexOf(s, sub) >= 0
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
