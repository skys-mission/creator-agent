package middlewares

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"github.com/skys-mission/creator-agent/core"
)

// memoryStart / memoryEnd mark the boundaries of the injected block (idempotent across turns, same as AgentsMd).
const (
	memoryStart = "<!-- auto-memory:start -->"
	memoryEnd   = "<!-- auto-memory:end -->"
)

// extractMemoryPrompt is the prompt sent to a forked agent to extract durable facts from the conversation.
const extractMemoryPrompt = `Analyze the conversation above. Extract durable facts worth remembering for future sessions — NOT transient task state.
Focus on: user preferences, project conventions, recurring decisions, confirmed architecture choices, key file locations, tools/setup specifics.
Output STRICT JSON (no markdown fences): {"facts": [{"topic": "short-slug", "fact": "one-line durable fact"}]}
If nothing worth remembering, output: {"facts": []}`

// AutoMemory maintains a long-term memory store:
//   - AfterAgent: asynchronously forks an agent to extract memorable facts from the turn and writes them to topic files + updates the index;
//   - BeforeAgent: injects the index summary into the system prompt so the agent knows historical context.
//
// Two-tier storage: INDEX.json (lightweight: topic + summary line, always loaded) and <topic>.md (heavyweight: details, loaded on demand).
type AutoMemory struct {
	core.BaseMiddleware

	Model core.ModelProvider // used by the forked extraction agent
	Dir   string             // memory directory (~/.creator/memory); empty means disabled

	// Injection control: inject the first N topics by update time to avoid bloating the context.
	MaxIndexEntries int

	mu sync.Mutex // serializes writes to the memory store

	extractCtx    context.Context
	extractCancel context.CancelFunc
	extractWG     sync.WaitGroup
}

// NewAutoMemory creates an AutoMemory middleware; dir empty disables both injection and extraction.
func NewAutoMemory(model core.ModelProvider, dir string) *AutoMemory {
	a := &AutoMemory{
		Model:           model,
		Dir:             dir,
		MaxIndexEntries: 15,
	}
	a.extractCtx, a.extractCancel = context.WithCancel(context.Background())
	return a
}

// Close cancels all in-flight memory extractions and waits for them to finish.
// Call before program exit (registered in main cleanup). Extraction is best-effort: cancellation abandons the current round without affecting already-stored memories.
func (a *AutoMemory) Close() {
	if a.extractCancel != nil {
		a.extractCancel()
	}
	a.extractWG.Wait()
}

// BeforeAgent injects the memory index into the system prompt (idempotent across turns).
func (a *AutoMemory) BeforeAgent(_ context.Context, st *core.RunState) error {
	if a.Dir == "" {
		return nil
	}
	index := a.loadIndex()
	if len(index) == 0 {
		return nil
	}
	st.Messages = rewriteSystemBlock(st.Messages, memoryStart, memoryEnd, buildMemoryBlock(index, a.MaxIndexEntries))
	return nil
}

// AfterAgent asynchronously triggers memory extraction (non-blocking for the main response flow).
func (a *AutoMemory) AfterAgent(ctx context.Context, st *core.RunState) error {
	if a.Dir == "" || a.Model == nil || len(st.Messages) < 4 {
		return nil // too short to extract
	}
	// Snapshot messages because st may be reused while extraction runs asynchronously.
	msgs := append([]core.Message(nil), st.Messages...)
	a.extractWG.Add(1)
	go func() {
		defer a.extractWG.Done()
		a.extractAndStore(a.extractCtx, msgs)
	}()
	return nil
}

// extractAndStore uses a forked agent to extract facts and writes them into the memory store.
func (a *AutoMemory) extractAndStore(ctx context.Context, msgs []core.Message) {
	// Extraction is best-effort and off the critical path. Recover panics to keep the process stable.
	defer func() {
		if r := recover(); r != nil {
			core.Warnf("auto-memory extract panic recovered: %v\n%s", r, debug.Stack())
		}
	}()
	facts, err := a.extractFacts(ctx, msgs)
	if err != nil || len(facts) == 0 {
		return
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	if err := os.MkdirAll(a.Dir, 0o755); err != nil {
		return
	}
	index := a.loadIndex()
	for _, f := range facts {
		if err := a.appendFact(f, index); err != nil {
			core.Warnf("auto-memory append fact failed: %v", err)
		}
	}
	if err := a.saveIndex(index); err != nil {
		core.Warnf("auto-memory save index failed: %v", err)
	}
}

// extractFacts calls the model to extract facts (reuses CollectText + JSON parsing).
func (a *AutoMemory) extractFacts(ctx context.Context, msgs []core.Message) ([]memoryFact, error) {
	out, err := core.CollectText(ctx, a.Model, append(append([]core.Message(nil), msgs...), core.UserMessage(extractMemoryPrompt)))
	if err != nil {
		return nil, err
	}
	out = strings.TrimSpace(out)
	// Strip possible markdown code block wrappers
	out = strings.TrimPrefix(out, "```json")
	out = strings.TrimPrefix(out, "```")
	out = strings.TrimSuffix(out, "```")
	out = strings.TrimSpace(out)
	var res struct {
		Facts []memoryFact `json:"facts"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		return nil, err
	}
	cleaned := make([]memoryFact, 0, len(res.Facts))
	for _, f := range res.Facts {
		if strings.TrimSpace(f.Topic) != "" && strings.TrimSpace(f.Fact) != "" {
			f.Topic = sanitizeTopic(f.Topic)
			cleaned = append(cleaned, f)
		}
	}
	return cleaned, nil
}

// memoryFact is a single memory entry (topic grouping + fact content).
type memoryFact struct {
	Topic string `json:"topic"`
	Fact  string `json:"fact"`
}

// loadIndex reads the index file (topic -> summary line + update time).
func (a *AutoMemory) loadIndex() map[string]indexEntry {
	data, err := os.ReadFile(a.indexPath())
	if err != nil {
		return map[string]indexEntry{}
	}
	var entries []indexEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		return map[string]indexEntry{}
	}
	m := make(map[string]indexEntry, len(entries))
	for _, e := range entries {
		m[e.Topic] = e
	}
	return m
}

// saveIndex writes the index file atomically.
func (a *AutoMemory) saveIndex(index map[string]indexEntry) error {
	entries := make([]indexEntry, 0, len(index))
	for _, e := range index {
		entries = append(entries, e)
	}
	data, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal index: %w", err)
	}
	tmp := a.indexPath() + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("write index tmp: %w", err)
	}
	if err := os.Rename(tmp, a.indexPath()); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("rename index: %w", err)
	}
	return nil
}

// atomicWriteFile writes data atomically via tmp+rename to prevent partial writes on crash.
func atomicWriteFile(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".tmp-mem-*")
	if err != nil {
		return fmt.Errorf("create temp: %w", err)
	}
	name := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(name)
		return fmt.Errorf("write temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(name)
		return fmt.Errorf("close temp: %w", err)
	}
	if err := os.Chmod(name, mode); err != nil {
		_ = os.Remove(name)
		return fmt.Errorf("chmod temp: %w", err)
	}
	if err := os.Rename(name, path); err != nil {
		_ = os.Remove(name)
		return fmt.Errorf("rename temp: %w", err)
	}
	return nil
}

// appendFact appends a fact to the topic file and updates the index summary.
func (a *AutoMemory) appendFact(f memoryFact, index map[string]indexEntry) error {
	path := filepath.Join(a.Dir, f.Topic+".md")
	var existing string
	if data, err := os.ReadFile(path); err == nil {
		existing = string(data)
	}
	var sb strings.Builder
	sb.WriteString(existing)
	if !strings.HasSuffix(existing, "\n") && existing != "" {
		sb.WriteString("\n")
	}
	fmt.Fprintf(&sb, "- %s\n", f.Fact)
	if err := atomicWriteFile(path, []byte(sb.String()), 0o644); err != nil {
		return fmt.Errorf("write topic %q: %w", f.Topic, err)
	}

	// Update index (summary = first fact line or topic name)
	summary := f.Fact
	if len(summary) > 80 {
		summary = summary[:80] + "..."
	}
	index[f.Topic] = indexEntry{Topic: f.Topic, Summary: summary, Updated: time.Now().Unix()}
	return nil
}

// indexEntry is a single record in the index file.
type indexEntry struct {
	Topic   string `json:"topic"`
	Summary string `json:"summary"`
	Updated int64  `json:"updated"`
}

func (a *AutoMemory) indexPath() string { return filepath.Join(a.Dir, "INDEX.json") }

// buildMemoryBlock builds the injected memory block (up to maxEntries, sorted by update time descending).
func buildMemoryBlock(index map[string]indexEntry, maxEntries int) string {
	if len(index) == 0 {
		return ""
	}
	entries := make([]indexEntry, 0, len(index))
	for _, e := range index {
		entries = append(entries, e)
	}
	// Sort by update time descending (newest first)
	sortEntriesByUpdatedDesc(entries)
	if len(entries) > maxEntries {
		entries = entries[:maxEntries]
	}
	var sb strings.Builder
	sb.WriteString("\n\n")
	sb.WriteString(memoryStart)
	sb.WriteString("\n# Long-term memory (from past sessions)\n")
	for _, e := range entries {
		fmt.Fprintf(&sb, "- [%s] %s\n", e.Topic, e.Summary)
	}
	sb.WriteString(memoryEnd)
	return sb.String()
}

// sortEntriesByUpdatedDesc sorts entries by update time descending.
func sortEntriesByUpdatedDesc(entries []indexEntry) {
	for i := 1; i < len(entries); i++ {
		for j := i; j > 0 && entries[j].Updated > entries[j-1].Updated; j-- {
			entries[j], entries[j-1] = entries[j-1], entries[j]
		}
	}
}

// sanitizeTopic converts a topic slug into a filename-safe form.
func sanitizeTopic(topic string) string {
	if s := strings.Trim(core.SanitizeFilename(topic), "-"); s != "" {
		return s
	}
	return "misc"
}

// Compile-time check that AutoMemory implements Middleware.
var _ core.Middleware = (*AutoMemory)(nil)
