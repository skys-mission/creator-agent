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

// writeTestFile helper writes a test file and returns its path.
func writeTestFile(t *testing.T, path, content string) string {
	t.Helper()
	path = filepath.Join(t.TempDir(), path)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestAgentsMdInjects verifies that AGENTS.md content is injected into the first system message with boundary markers.
func TestAgentsMdInjects(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("RULE: be terse"), 0o644)

	a := &AgentsMd{workDir: dir}
	st := &core.RunState{Messages: []core.Message{core.SystemMessage("base")}}
	if err := a.BeforeAgent(context.Background(), st); err != nil {
		t.Fatal(err)
	}
	if st.Messages[0].Role != core.RoleSystem {
		t.Fatal("first msg not system")
	}
	c := st.Messages[0].Content
	if !strings.HasPrefix(c, "base") {
		t.Errorf("base system prompt lost: %q", c)
	}
	if !strings.Contains(c, agentsMdStart) || !strings.Contains(c, agentsMdEnd) {
		t.Errorf("boundary markers missing: %q", c)
	}
	if !strings.Contains(c, "RULE: be terse") {
		t.Errorf("AGENTS.md body missing: %q", c)
	}
}

// TestAgentsMdIdempotent verifies idempotency: running BeforeAgent multiple times produces only one block.
func TestAgentsMdIdempotent(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("ONCE"), 0o644)

	a := &AgentsMd{workDir: dir}
	st := &core.RunState{Messages: []core.Message{core.SystemMessage("base")}}
	_ = a.BeforeAgent(context.Background(), st)
	_ = a.BeforeAgent(context.Background(), st)
	_ = a.BeforeAgent(context.Background(), st)

	c := st.Messages[0].Content
	if n := strings.Count(c, agentsMdStart); n != 1 {
		t.Errorf("start markers = %d, want 1; content=%q", n, c)
	}
	if n := strings.Count(c, agentsMdEnd); n != 1 {
		t.Errorf("end markers = %d, want 1; content=%q", n, c)
	}
}

// TestAgentsMdMtimeCache verifies mtime caching: changing content without changing mtime keeps the old cache; changing mtime reloads.
func TestAgentsMdMtimeCache(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "AGENTS.md")
	os.WriteFile(p, []byte("V1"), 0o644)

	a := &AgentsMd{workDir: dir}
	st := &core.RunState{Messages: []core.Message{core.SystemMessage("base")}}
	_ = a.BeforeAgent(context.Background(), st)
	if !strings.Contains(st.Messages[0].Content, "V1") {
		t.Fatalf("expected injected content V1, got %q", st.Messages[0].Content)
	}

	// Change content but keep mtime unchanged → should hit the old cache.
	info, _ := os.Stat(p)
	os.WriteFile(p, []byte("V2-should-not-show"), 0o644)
	os.Chtimes(p, info.ModTime(), info.ModTime())

	st2 := &core.RunState{Messages: []core.Message{core.SystemMessage("base2")}}
	_ = a.BeforeAgent(context.Background(), st2)
	if strings.Contains(st2.Messages[0].Content, "V2-should-not-show") {
		t.Error("cache miss: V2 leaked despite mtime unchanged")
	}

	// Actually change mtime → should reload to V3
	future := time.Now().Add(time.Hour)
	os.WriteFile(p, []byte("V3"), 0o644)
	os.Chtimes(p, future, future)
	st3 := &core.RunState{Messages: []core.Message{core.SystemMessage("base3")}}
	_ = a.BeforeAgent(context.Background(), st3)
	if !strings.Contains(st3.Messages[0].Content, "V3") {
		t.Error("expected V3 after mtime change, not found")
	}
}

// TestAgentsMdProjectWinsOverGlobal verifies the single-source fallback: a project AGENTS.md takes
// precedence and the global file is not injected.
func TestAgentsMdProjectWinsOverGlobal(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("LOCAL"), 0o644)
	gp := writeTestFile(t, "global/AGENTS.md", "GLOBAL")

	a := &AgentsMd{workDir: dir, globalPath: gp}
	st := &core.RunState{Messages: []core.Message{core.SystemMessage("base")}}
	_ = a.BeforeAgent(context.Background(), st)
	c := st.Messages[0].Content
	if !strings.Contains(c, "LOCAL") {
		t.Errorf("project AGENTS.md missing; content=%q", c)
	}
	if strings.Contains(c, "GLOBAL") {
		t.Errorf("global should not be injected when project exists; content=%q", c)
	}
}

// TestAgentsMdGlobalFallback verifies the global AGENTS.md is used when the project has none.
func TestAgentsMdGlobalFallback(t *testing.T) {
	dir := t.TempDir() // no AGENTS.md / CLAUDE.md / .creator
	gp := writeTestFile(t, "global/AGENTS.md", "GLOBAL")

	a := &AgentsMd{workDir: dir, globalPath: gp}
	st := &core.RunState{Messages: []core.Message{core.SystemMessage("base")}}
	_ = a.BeforeAgent(context.Background(), st)
	if !strings.Contains(st.Messages[0].Content, "GLOBAL") {
		t.Errorf("global fallback missing; content=%q", st.Messages[0].Content)
	}
}

// TestAgentsMdCreatorDirFallback verifies the .creator/AGENTS.md fallback at a directory level when
// neither AGENTS.md nor CLAUDE.md is present there.
func TestAgentsMdCreatorDirFallback(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, ".creator"), 0o755)
	os.WriteFile(filepath.Join(dir, ".creator", "AGENTS.md"), []byte("CREATOR_DIR"), 0o644)

	a := &AgentsMd{workDir: dir}
	st := &core.RunState{Messages: []core.Message{core.SystemMessage("base")}}
	_ = a.BeforeAgent(context.Background(), st)
	if !strings.Contains(st.Messages[0].Content, "CREATOR_DIR") {
		t.Errorf(".creator/AGENTS.md fallback missing; content=%q", st.Messages[0].Content)
	}
}

// TestAgentsMdRootAgentsBeatsCreatorDir verifies that within a directory, AGENTS.md wins over the
// .creator/AGENTS.md fallback.
func TestAgentsMdRootAgentsBeatsCreatorDir(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("ROOT_AGENTS"), 0o644)
	os.MkdirAll(filepath.Join(dir, ".creator"), 0o755)
	os.WriteFile(filepath.Join(dir, ".creator", "AGENTS.md"), []byte("CREATOR_DIR"), 0o644)

	a := &AgentsMd{workDir: dir}
	st := &core.RunState{Messages: []core.Message{core.SystemMessage("base")}}
	_ = a.BeforeAgent(context.Background(), st)
	c := st.Messages[0].Content
	if !strings.Contains(c, "ROOT_AGENTS") {
		t.Errorf("root AGENTS.md should win; content=%q", c)
	}
	if strings.Contains(c, "CREATOR_DIR") {
		t.Errorf(".creator/AGENTS.md should not be used when root AGENTS.md exists; content=%q", c)
	}
}

// TestAgentsMdClaudeMdFallback verifies fallback to CLAUDE.md when AGENTS.md is missing.
func TestAgentsMdClaudeMdFallback(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "CLAUDE.md"), []byte("FALLBACK"), 0o644)

	a := &AgentsMd{workDir: dir}
	st := &core.RunState{Messages: []core.Message{core.SystemMessage("base")}}
	_ = a.BeforeAgent(context.Background(), st)
	if !strings.Contains(st.Messages[0].Content, "FALLBACK") {
		t.Error("CLAUDE.md fallback failed")
	}
}

// TestAgentsMdNoDuplicate verifies that AGENTS.md takes precedence when both files exist in the same directory.
func TestAgentsMdNoDuplicate(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("AGENT_BODY"), 0o644)
	os.WriteFile(filepath.Join(dir, "CLAUDE.md"), []byte("CLAUDE_BODY"), 0o644)

	a := &AgentsMd{workDir: dir}
	st := &core.RunState{Messages: []core.Message{core.SystemMessage("base")}}
	_ = a.BeforeAgent(context.Background(), st)
	c := st.Messages[0].Content
	if !strings.Contains(c, "AGENT_BODY") {
		t.Error("AGENTS.md missing")
	}
	if strings.Contains(c, "CLAUDE_BODY") {
		t.Error("CLAUDE.md leaked when AGENTS.md present")
	}
}

// TestAgentsMdWalksUpStopsAtGit verifies walking up from a subdirectory stops at the .git root.
func TestAgentsMdWalksUpStopsAtGit(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "pkg", "sub"), 0o755)
	os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte("ROOT_LEVEL"), 0o644)
	os.MkdirAll(filepath.Join(root, ".git"), 0o755)

	a := &AgentsMd{workDir: filepath.Join(root, "pkg", "sub")}
	st := &core.RunState{Messages: []core.Message{core.SystemMessage("base")}}
	_ = a.BeforeAgent(context.Background(), st)
	if !strings.Contains(st.Messages[0].Content, "ROOT_LEVEL") {
		t.Error("expected to find ROOT_LEVEL by walking up to git root")
	}
}

// TestAgentsMdNoopWhenMissing verifies no messages are modified when no AGENTS.md/CLAUDE.md exists.
func TestAgentsMdNoopWhenMissing(t *testing.T) {
	a := &AgentsMd{workDir: t.TempDir()}
	orig := []core.Message{core.SystemMessage("base")}
	st := &core.RunState{Messages: append([]core.Message{}, orig...)}
	_ = a.BeforeAgent(context.Background(), st)
	if st.Messages[0].Content != "base" {
		t.Errorf("system prompt mutated when no AGENTS.md: %q", st.Messages[0].Content)
	}
}

// TestAgentsMdNoSystemMsgInserts verifies that a system message is prepended when none exists and AGENTS.md is present.
func TestAgentsMdNoSystemMsgInserts(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("INSERTED"), 0o644)

	a := &AgentsMd{workDir: dir}
	st := &core.RunState{Messages: []core.Message{core.UserMessage("hi")}}
	_ = a.BeforeAgent(context.Background(), st)
	if st.Messages[0].Role != core.RoleSystem {
		t.Fatal("expected system message inserted at front")
	}
	if !strings.Contains(st.Messages[0].Content, "INSERTED") {
		t.Errorf("inserted system missing body: %q", st.Messages[0].Content)
	}
	if len(st.Messages) != 2 || st.Messages[1].Content != "hi" {
		t.Errorf("user message lost: %+v", st.Messages)
	}
}

// TestNewAgentsMdDefaults verifies default zero values.
func TestNewAgentsMdDefaults(t *testing.T) {
	a := NewAgentsMd()
	if a == nil {
		t.Fatal("NewAgentsMd should return non-nil")
	}
	if a.workDir != "" || a.globalPath != "" {
		t.Errorf("defaults should be empty (runtime-resolved), got workDir=%q globalPath=%q", a.workDir, a.globalPath)
	}
}

// TestAgentsMdStripUnmatched verifies that unclosed marker blocks are not stripped to avoid deleting user content.
func TestAgentsMdStripUnmatched(t *testing.T) {
	// Only start, no end → leave intact
	s := "before " + agentsMdStart + " content without end after"
	if got := stripBlock(s, agentsMdStart, agentsMdEnd); got != s {
		t.Errorf("unclosed block should not be stripped, got %q", got)
	}
	// end before start → leave intact (malformed, conservative)
	s2 := agentsMdEnd + " x " + agentsMdStart
	if got := stripBlock(s2, agentsMdStart, agentsMdEnd); got != s2 {
		t.Errorf("malformed (end before start) should not be stripped, got %q", got)
	}
}
