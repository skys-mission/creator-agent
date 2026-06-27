package middlewares

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/skys-mission/creator-agent/core"
)

// TestAutoMemoryDisabled verifies that BeforeAgent does not modify messages when the memory directory is empty.
func TestAutoMemoryDisabled(t *testing.T) {
	a := NewAutoMemory(nil, "")
	st := &core.RunState{Messages: []core.Message{core.SystemMessage("base")}}
	_ = a.BeforeAgent(context.Background(), st)
	if st.Messages[0].Content != "base" {
		t.Error("disabled AutoMemory should not modify messages")
	}
}

// TestAutoMemoryEmptyIndex verifies that BeforeAgent does not inject anything when the index is empty.
func TestAutoMemoryEmptyIndex(t *testing.T) {
	a := NewAutoMemory(nil, t.TempDir())
	st := &core.RunState{Messages: []core.Message{core.SystemMessage("base")}}
	_ = a.BeforeAgent(context.Background(), st)
	if strings.Contains(st.Messages[0].Content, memoryStart) {
		t.Error("empty index should not inject")
	}
}

// TestAutoMemoryInjects verifies that memories are injected into the system prompt with boundary markers.
func TestAutoMemoryInjects(t *testing.T) {
	dir := t.TempDir()
	a := NewAutoMemory(nil, dir)
	// Pre-populate the index
	a.saveIndex(map[string]indexEntry{
		"go-conventions": {Topic: "go-conventions", Summary: "use errors.Is not ==", Updated: time.Now().Unix()},
	})
	st := &core.RunState{Messages: []core.Message{core.SystemMessage("base")}}
	_ = a.BeforeAgent(context.Background(), st)
	c := st.Messages[0].Content
	if !strings.HasPrefix(c, "base") {
		t.Error("base system prompt lost")
	}
	if !strings.Contains(c, memoryStart) || !strings.Contains(c, memoryEnd) {
		t.Error("memory block markers missing")
	}
	if !strings.Contains(c, "go-conventions") || !strings.Contains(c, "errors.Is") {
		t.Error("memory content missing")
	}
}

// TestAutoMemoryIdempotent verifies that multiple BeforeAgent calls produce only one block.
func TestAutoMemoryIdempotent(t *testing.T) {
	dir := t.TempDir()
	a := NewAutoMemory(nil, dir)
	a.saveIndex(map[string]indexEntry{"t": {Topic: "t", Summary: "s", Updated: 1}})
	st := &core.RunState{Messages: []core.Message{core.SystemMessage("base")}}
	_ = a.BeforeAgent(context.Background(), st)
	_ = a.BeforeAgent(context.Background(), st)
	_ = a.BeforeAgent(context.Background(), st)
	c := st.Messages[0].Content
	if strings.Count(c, memoryStart) != 1 {
		t.Errorf("start markers = %d, want 1", strings.Count(c, memoryStart))
	}
}

// TestAutoMemoryMaxEntries verifies that MaxIndexEntries limits the number of injected entries.
func TestAutoMemoryMaxEntries(t *testing.T) {
	dir := t.TempDir()
	a := NewAutoMemory(nil, dir)
	a.MaxIndexEntries = 2
	idx := map[string]indexEntry{}
	for i := 0; i < 5; i++ {
		idx[string(rune('a'+i))] = indexEntry{Topic: string(rune('a' + i)), Summary: "s", Updated: int64(i)}
	}
	a.saveIndex(idx)
	st := &core.RunState{Messages: []core.Message{core.SystemMessage("base")}}
	_ = a.BeforeAgent(context.Background(), st)
	c := st.Messages[0].Content
	// Should contain only the 2 most recent entries (largest updated)
	lines := strings.Count(c, "\n- [")
	if lines > 2 {
		t.Errorf("injected %d entries, want ≤2", lines)
	}
}

// TestExtractFacts verifies that extractFacts correctly parses JSON output.
func TestExtractFacts(t *testing.T) {
	provider := &textProvider{text: `{"facts":[{"topic":"go-style","fact":"prefer explicit errors"}]}`}
	a := NewAutoMemory(provider, t.TempDir())
	facts, err := a.extractFacts(context.Background(), []core.Message{core.UserMessage("chat")})
	if err != nil {
		t.Fatal(err)
	}
	if len(facts) != 1 || facts[0].Topic != "go-style" {
		t.Errorf("facts = %+v", facts)
	}
}

// TestExtractFactsMarkdownWrap verifies that extractFacts tolerates markdown-wrapped JSON.
func TestExtractFactsMarkdownWrap(t *testing.T) {
	provider := &textProvider{text: "```json\n{\"facts\":[{\"topic\":\"x\",\"fact\":\"y\"}]}\n```"}
	a := NewAutoMemory(provider, t.TempDir())
	facts, _ := a.extractFacts(context.Background(), []core.Message{core.UserMessage("x")})
	if len(facts) != 1 || facts[0].Topic != "x" {
		t.Errorf("markdown-wrapped JSON not parsed: %+v", facts)
	}
}

// TestExtractFactsEmpty verifies that extractFacts handles an empty facts array.
func TestExtractFactsEmpty(t *testing.T) {
	provider := &textProvider{text: `{"facts":[]}`}
	a := NewAutoMemory(provider, t.TempDir())
	facts, _ := a.extractFacts(context.Background(), []core.Message{core.UserMessage("x")})
	if len(facts) != 0 {
		t.Errorf("expected 0 facts, got %d", len(facts))
	}
}

// TestAppendFact verifies that appendFact writes the topic file and updates the index.
func TestAppendFact(t *testing.T) {
	dir := t.TempDir()
	a := NewAutoMemory(nil, dir)
	index := map[string]indexEntry{}
	a.appendFact(memoryFact{Topic: "topic1", Fact: "first fact"}, index)
	a.appendFact(memoryFact{Topic: "topic1", Fact: "second fact"}, index)
	// Topic file should contain both facts
	data, _ := os.ReadFile(filepath.Join(dir, "topic1.md"))
	if !strings.Contains(string(data), "first fact") || !strings.Contains(string(data), "second fact") {
		t.Errorf("topic file missing facts: %s", data)
	}
	// Index should be updated
	if _, ok := index["topic1"]; !ok {
		t.Error("index not updated")
	}
}

// TestAppendFactDoesNotUpdateIndexOnWriteFailure verifies index/topic consistency on persistence errors.
func TestAppendFactDoesNotUpdateIndexOnWriteFailure(t *testing.T) {
	dir := t.TempDir()
	a := NewAutoMemory(nil, dir)
	if err := os.Mkdir(filepath.Join(dir, "topic1.md"), 0o755); err != nil {
		t.Fatal(err)
	}
	index := map[string]indexEntry{}
	err := a.appendFact(memoryFact{Topic: "topic1", Fact: "first fact"}, index)
	if err == nil {
		t.Fatal("appendFact should fail when the target topic path is a directory")
	}
	if _, ok := index["topic1"]; ok {
		t.Error("index should not be updated when topic write fails")
	}
}

// TestExtractAndStore verifies the end-to-end flow: extraction -> persistence.
func TestExtractAndStore(t *testing.T) {
	dir := t.TempDir()
	provider := &textProvider{text: `{"facts":[{"topic":"convention","fact":"use tabs not spaces"}]}`}
	a := NewAutoMemory(provider, dir)
	msgs := []core.Message{
		core.UserMessage("question 1"), core.AssistantMessage("answer 1"),
		core.UserMessage("question 2"), core.AssistantMessage("answer 2"),
	}
	a.extractAndStore(context.Background(), msgs)
	// Verify files were written
	if _, err := os.Stat(filepath.Join(dir, "convention.md")); err != nil {
		t.Errorf("topic file not written: %v", err)
	}
	if _, err := os.Stat(a.indexPath()); err != nil {
		t.Errorf("index file not written: %v", err)
	}
}

// TestAfterAgentTooShort verifies that conversations with fewer than 4 messages do not trigger extraction.
func TestAfterAgentTooShort(t *testing.T) {
	provider := &textProvider{text: `{"facts":[{"topic":"x","fact":"y"}]}`}
	a := NewAutoMemory(provider, t.TempDir())
	// 2 messages (< 4) → no extraction
	_ = a.AfterAgent(context.Background(), &core.RunState{Messages: []core.Message{
		core.UserMessage("hi"), core.AssistantMessage("hey"),
	}})
	// Give the goroutine a moment to run (it should not)
	time.Sleep(50 * time.Millisecond)
	if _, err := os.Stat(a.indexPath()); !os.IsNotExist(err) {
		t.Error("should not extract for short conversation")
	}
}

// TestSanitizeTopic verifies that sanitizeTopic cleans invalid characters.
func TestSanitizeTopic(t *testing.T) {
	cases := map[string]string{
		"go-style": "go-style",
		"Go Style": "Go-Style",
		"a/b/../c": "a-b----c",
		"":         "misc",
		"---":      "misc",
		"normal_1": "normal_1",
	}
	for in, want := range cases {
		if got := sanitizeTopic(in); got != want {
			t.Errorf("sanitize(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestStripMemoryBlock verifies that stripMemoryBlock removes marked blocks.
func TestStripMemoryBlock(t *testing.T) {
	s := "before" + memoryStart + "\ncontent\n" + memoryEnd + "after"
	got := stripBlock(s, memoryStart, memoryEnd)
	if got != "beforeafter" {
		t.Errorf("strip = %q", got)
	}
}

// textProvider satisfies ModelProvider for tests (compact_test's stubProvider is not visible here).
type textProvider struct{ text string }

func (p *textProvider) Stream(ctx context.Context, req core.ModelRequest) (<-chan core.ModelEvent, error) {
	ch := make(chan core.ModelEvent, 1)
	go func() {
		defer close(ch)
		ch <- core.MTextDelta{Delta: p.text}
	}()
	return ch, nil
}

// ===== AfterAgent guard conditions + lifecycle =====

// TestAfterAgentSkipsShortConversation verifies that conversations with fewer than 4 messages do not trigger extraction.
func TestAfterAgentSkipsShortConversation(t *testing.T) {
	a := NewAutoMemory(&textProvider{text: `{"facts":[]}`}, t.TempDir())
	// 3 messages (< 4)
	st := &core.RunState{Messages: []core.Message{
		core.SystemMessage("sys"), core.UserMessage("hi"), core.AssistantMessage("yo"),
	}}
	if err := a.AfterAgent(context.Background(), st); err != nil {
		t.Fatalf("AfterAgent: %v", err)
	}
	// Short conversation should not start a goroutine: Close should return immediately (no wait)
	a.Close()
	// No panic means the test passes (WG was not incremented)
}

// TestAfterAgentSkipsNoModel verifies that extraction is skipped when Model is nil.
func TestAfterAgentSkipsNoModel(t *testing.T) {
	a := NewAutoMemory(nil, t.TempDir()) // Model=nil
	st := &core.RunState{Messages: []core.Message{
		core.SystemMessage("sys"), core.UserMessage("a"), core.AssistantMessage("b"), core.UserMessage("c"),
	}}
	if err := a.AfterAgent(context.Background(), st); err != nil {
		t.Fatalf("AfterAgent: %v", err)
	}
	a.Close()
}

// TestAfterAgentSkipsNoDir verifies that extraction is skipped when Dir is empty.
func TestAfterAgentSkipsNoDir(t *testing.T) {
	a := NewAutoMemory(&textProvider{text: `{"facts":[]}`}, "") // Dir=""
	st := &core.RunState{Messages: []core.Message{
		core.SystemMessage("sys"), core.UserMessage("a"), core.AssistantMessage("b"), core.UserMessage("c"),
	}}
	if err := a.AfterAgent(context.Background(), st); err != nil {
		t.Fatalf("AfterAgent: %v", err)
	}
}

// TestAfterAgentTriggersExtraction verifies that a long enough conversation with Model and Dir set triggers async extraction.
// Close should wait for the goroutine to finish without leaking.
func TestAfterAgentTriggersExtraction(t *testing.T) {
	a := NewAutoMemory(&textProvider{text: `{"facts":[]}`}, t.TempDir())
	st := &core.RunState{Messages: []core.Message{
		core.SystemMessage("sys"), core.UserMessage("turn1"), core.AssistantMessage("resp1"), core.UserMessage("turn2"),
	}}
	if err := a.AfterAgent(context.Background(), st); err != nil {
		t.Fatalf("AfterAgent: %v", err)
	}
	// Close should wait for the async extraction goroutine to finish (no leak, no panic)
	a.Close()
	// No panic + Close returns means the test passes (extraction returned empty facts, no files written)
}

// TestAutoMemoryCloseIdempotent verifies that calling Close multiple times does not panic.
func TestAutoMemoryCloseIdempotent(t *testing.T) {
	a := NewAutoMemory(&textProvider{}, t.TempDir())
	a.Close()
	a.Close() // second Close should not panic
}
