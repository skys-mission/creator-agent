//go:build integration

// This file uses a real model API for end-to-end agent loop verification (long-range: multi-turn model calls + real tool execution).
//
// This is the highest-value real-world test: covers the full model→tool→model loop that mock tests cannot replace.
// For example: have the real model read a real file and answer its content — verifying OpenAI adapter tool_call streaming parsing,
// core loop tool execution, result backfill, and second-turn model call all work together correctly.
//
// Safety: contains no API keys (see core/adapters/openai-chat/integration_realapi_test.go for env reading).
// build tag=integration + CREATOR_AGENT_TEST_API_KEY double gating.
// Run instructions in docs/testing.md.
package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/skys-mission/creator-agent/core"
	openaichat "github.com/skys-mission/creator-agent/core/adapters/openai-chat"
	"github.com/skys-mission/creator-agent/core/adapters/shared"
	"github.com/skys-mission/creator-agent/core/builtins"
)

// realEnv reads real API config (isomorphic helper with the provider package, independent to avoid cross-package test dependencies).
type realEnv struct {
	apiKey  string
	baseURL string
	model   string
}

func loadRealEnv() (realEnv, bool) {
	k := os.Getenv("CREATOR_AGENT_TEST_API_KEY")
	if k == "" {
		return realEnv{}, false
	}
	return realEnv{
		apiKey:  k,
		baseURL: envDef("CREATOR_AGENT_TEST_BASE_URL", "https://api.deepseek.com"),
		model:   envDef("CREATOR_AGENT_TEST_MODEL", "deepseek-v4-flash"),
	}, true
}

func envDef(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

// requireReal is the shared gate: no key -> Skip, with key -> returns assembled agent + ctx.
// Agent is assembled with read/grep/glob read-only tools + system prompt (minimal set consistent with main.go).
func requireReal(t *testing.T) (core.Agent, context.Context) {
	t.Helper()
	env, ok := loadRealEnv()
	if !ok {
		t.Skip("CREATOR_AGENT_TEST_API_KEY not set; real API long-range test skipped (see docs/testing.md)")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	t.Cleanup(cancel)
	provider, err := openaichat.NewProvider(ctx, shared.ProviderConfig{
		BaseURL:        env.baseURL,
		APIKey:         env.apiKey,
		Model:          env.model,
		RequestTimeout: 90 * time.Second,
	})
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}
	ag := core.NewAgent(provider,
		core.WithTools(
			builtins.NewReadTool(),
			builtins.NewGrepTool(),
			builtins.NewGlobTool(),
		),
		core.WithSystemPrompt(realTestSystemPrompt),
	)
	return ag, ctx
}

const realTestSystemPrompt = `You are a test agent. You can read files using the read tool.
When asked about a file's content, use the read tool to read it, then report what you found.
Be concise and answer in the language the user used. When you have the answer, state it plainly.`

// collectFull runs one agent.Stream, collects all events, and returns concatenated text + tool call records.
// On failure (ErrorEvent) returns error.
type runResult struct {
	text        string
	toolResults []core.ToolResultEvent // tool execution results (regardless of whether provider uses Complete or Delta mode)
	finish      core.FinishReason
	err         error
	usage       *core.Usage
}

// toolCalls is derived from toolResults (reliable signal, does not depend on ToolUseStartEvent —
// that event is only sent when the provider uses MToolUseComplete mode, delta mode does not send it).
func (r runResult) toolCalls() int { return len(r.toolResults) }

func runAgent(ctx context.Context, ag core.Agent, sessionID, prompt string) runResult {
	ch, err := ag.Stream(ctx, core.StreamInput{
		SessionID: sessionID,
		Messages:  []core.Message{core.UserMessage(prompt)},
	})
	if err != nil {
		return runResult{err: err}
	}
	var r runResult
	for ev := range ch {
		switch e := ev.(type) {
		case core.TextEvent:
			r.text += e.Delta
		case core.ToolResultEvent:
			r.toolResults = append(r.toolResults, e)
		case core.FinishEvent:
			r.finish = e.Reason
		case core.ErrorEvent:
			r.err = e.Err
		case core.UsageEvent:
			u := e.Usage
			r.usage = &u
		}
	}
	return r
}

// =====================================================================
// Real API long-range tests: agent loop
// =====================================================================

// TestRealAgentReadsFile: core long-range test — have the real model use the read tool to read a real file and report its content.
// Covers: adapter streaming tool_call parsing -> core loop executes read -> result backfill -> second-turn model answer.
// Smart assertion: file contains a unique sentinel string; verify the model answer contains that sentinel (proves it really read the file).
func TestRealAgentReadsFile(t *testing.T) {
	ag, ctx := requireReal(t)

	// Prepare a temporary file with a unique sentinel
	dir := t.TempDir()
	sentinel := "ZEBRA-TANGO-MANGO-7291" // extremely unlikely to be guessed randomly
	path := filepath.Join(dir, "data.txt")
	content := "Project code: " + sentinel + "\nNote: This is a marker file for end-to-end testing.\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	r := runAgent(ctx, ag, "test-read", "Please use the read tool to read this file and tell me the project code inside: "+path)
	if r.err != nil {
		t.Fatalf("agent execution error: %v", r.err)
	}
	t.Logf("tool calls=%d, answer=%q", r.toolCalls(), r.text)

	// Core assertion 1: model actually called the tool (not hallucinating content)
	if r.toolCalls() == 0 {
		t.Error("model did not call read tool — should have at least one tool call")
	}
	// Core assertion 2: answer contains the sentinel string (proves it really read the file, not hallucinating)
	if !strings.Contains(r.text, sentinel) {
		t.Errorf("answer should contain sentinel %q, actual: %q", sentinel, r.text)
	}
	// Tool results should not be errors (file does exist)
	for _, tr := range r.toolResults {
		if tr.Err != nil {
			t.Errorf("read tool system error: %v", tr.Err)
		}
		if tr.Result.IsError {
			t.Errorf("read tool business error: %s", tr.Result.Content)
		}
	}
}

// TestRealAgentMultiTurnSession: verify session multi-turn memory (multi-turn closed loop).
// First turn tells it a fact and stores it in the session, second turn asks about that fact — assert it remembers.
func TestRealAgentMultiTurnSession(t *testing.T) {
	ag, ctx := requireReal(t)
	const sid = "test-memory"

	// First turn: implant information
	r1 := runAgent(ctx, ag, sid, "Please remember this password: PURPLE-OTTER-3308. Just reply 'remembered'.")
	if r1.err != nil {
		t.Fatalf("first turn error: %v", r1.err)
	}
	t.Logf("first turn answer: %q", r1.text)

	// Second turn: ask the same session (relies on SessionStore memory)
	r2 := runAgent(ctx, ag, sid, "What was the password I told you earlier? Only answer the password itself.")
	if r2.err != nil {
		t.Fatalf("second turn error: %v", r2.err)
	}
	t.Logf("second turn answer: %q", r2.text)

	if !strings.Contains(r2.text, "PURPLE-OTTER-3308") {
		t.Errorf("session multi-turn memory failed: second turn should recall the password, actual %q", r2.text)
	}
}

// TestRealAgentDeterministicMath: deterministic smart assertion — simple math.
// Use a question with a unique correct answer; assert the answer contains the correct number (more reliable than open-ended Q&A).
func TestRealAgentDeterministicMath(t *testing.T) {
	ag, ctx := requireReal(t)
	r := runAgent(ctx, ag, "test-math", "Calculate 12 + 8, reply only the numeric result, no other text.")
	if r.err != nil {
		t.Fatalf("execution error: %v", r.err)
	}
	t.Logf("answer: %q", r.text)
	if !strings.Contains(r.text, "20") {
		t.Errorf("12+8 should contain 20, actual %q", r.text)
	}
}

// TestRealAgentFinishReason: verify normal completion finish is not step_limit/error.
// (Should not end due to MaxSteps exhaustion or system error.)
func TestRealAgentFinishReason(t *testing.T) {
	ag, ctx := requireReal(t)
	r := runAgent(ctx, ag, "test-finish", "Explain what water is in one short sentence.")
	if r.err != nil {
		t.Fatalf("execution error: %v", r.err)
	}
	if r.finish == core.FinishStepLimit {
		t.Error("should not end due to step_limit (MaxSteps too low or model infinitely calling tools)")
	}
	if r.finish == core.FinishError {
		t.Error("should not end due to error")
	}
	if r.text == "" {
		t.Error("should have text output")
	}
}

// TestRealAgentUsageRecorded: verify usage events are emitted after a long-range run (billing/monitoring related).
func TestRealAgentUsageRecorded(t *testing.T) {
	ag, ctx := requireReal(t)
	r := runAgent(ctx, ag, "test-usage", "Introduce the solar system in two sentences.")
	if r.err != nil {
		t.Fatalf("execution error: %v", r.err)
	}
	if r.usage == nil {
		t.Error("no usage event received (agent should forward provider usage)")
	} else if r.usage.InputTokens == 0 {
		t.Error("InputTokens should be > 0")
	}
}

// TestRealAgentMissingFile: verify when the model reads a non-existent file, the tool returns IsError,
// and the model responds gracefully (instead of crashing the loop or hallucinating content).
func TestRealAgentMissingFile(t *testing.T) {
	ag, ctx := requireReal(t)
	fakePath := filepath.Join(t.TempDir(), "nonexistent-file-xyz.txt")
	r := runAgent(ctx, ag, "test-missing", "Please use the read tool to read this file: "+fakePath)
	if r.err != nil {
		t.Fatalf("execution error: %v", r.err)
	}
	t.Logf("tool calls=%d answer=%q", r.toolCalls(), r.text)
	// Tool should be called and return a business error (IsError), not a system panic
	foundToolError := false
	for _, tr := range r.toolResults {
		if tr.Result.IsError {
			foundToolError = true
		}
		if tr.Err != nil {
			t.Errorf("reading a non-existent file should be a business error (IsError) not a system error: %v", tr.Err)
		}
	}
	if !foundToolError {
		t.Error("reading a non-existent file should return IsError=true business error")
	}
}

// Ensure main package tests do not fail due to unused import (errors is only used on some paths).
var _ = errors.Is
