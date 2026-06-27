package middlewares

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/skys-mission/creator-agent/core"
)

// writeSkill writes a skill as <dir>/<name>/SKILL.md (Agent Skills standard) and returns the
// SKILL.md path.
func writeSkill(t *testing.T, dir, name, content string) string {
	t.Helper()
	skillDir := filepath.Join(dir, name)
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(skillDir, "SKILL.md")
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

const sampleSkill = `---
name: refactor-go
description: guide for refactoring Go code
---
# Refactoring Go

1. Run go vet first
2. Check test coverage
3. Make small incremental changes
`

// ===== parseSkill =====

func TestParseSkillFrontmatter(t *testing.T) {
	sk := parseSkill(sampleSkill, "/x/refactor-go/SKILL.md", "refactor-go")
	if sk.Name != "refactor-go" {
		t.Errorf("name = %q", sk.Name)
	}
	if sk.Description != "guide for refactoring Go code" {
		t.Errorf("desc = %q", sk.Description)
	}
	if !strings.Contains(sk.Body, "Run go vet") {
		t.Errorf("body missing content: %q", sk.Body)
	}
}

// TestParseSkillNameFromDir verifies the directory name is authoritative even when frontmatter sets a
// different name.
func TestParseSkillNameFromDir(t *testing.T) {
	content := "---\nname: mismatch\ndescription: x\n---\nbody"
	sk := parseSkill(content, "/x/canonical/SKILL.md", "canonical")
	if sk.Name != "canonical" {
		t.Errorf("name = %q, want canonical (directory wins)", sk.Name)
	}
}

// TestParseAllowedTools verifies the Agent Skills allowed-tools field maps to built-in tool names.
func TestParseAllowedTools(t *testing.T) {
	content := "---\nname: scoped\ndescription: a scoped skill\nallowed-tools: Read Grep Bash(git:*) Glob\n---\nbody here\n"
	sk := parseSkill(content, "/x/scoped/SKILL.md", "scoped")
	want := map[string]bool{"read": true, "grep": true, "bash": true, "glob": true}
	if len(sk.Tools) != len(want) {
		t.Fatalf("tools = %+v, want %d entries", sk.Tools, len(want))
	}
	for _, tool := range sk.Tools {
		if !want[tool] {
			t.Errorf("unexpected tool %q in %+v", tool, sk.Tools)
		}
	}
}

// TestParseAllowedToolsEmpty verifies a skill without allowed-tools has an empty whitelist.
func TestParseAllowedToolsEmpty(t *testing.T) {
	sk := parseSkill(sampleSkill, "/x/refactor-go/SKILL.md", "refactor-go")
	if len(sk.Tools) != 0 {
		t.Errorf("tools should be empty when not declared; got %+v", sk.Tools)
	}
}

func TestParseSkillNoFrontmatter(t *testing.T) {
	sk := parseSkill("just plain markdown\n# body", "/x/plain/SKILL.md", "plain")
	if sk.Name != "plain" { // falls back to directory name
		t.Errorf("name = %q, want plain", sk.Name)
	}
	if !strings.Contains(sk.Body, "plain markdown") {
		t.Errorf("body = %q", sk.Body)
	}
}

func TestParseSkillUnclosedFrontmatter(t *testing.T) {
	sk := parseSkill("---\nname: x\nthis has no end fence", "/x/x/SKILL.md", "x")
	if sk.Body == "" {
		t.Error("unclosed frontmatter should fall back to whole-content-as-body")
	}
}

func TestParseSkillEmptyFrontmatter(t *testing.T) {
	sk := parseSkill("---\n---\nbody here", "/x/empty/SKILL.md", "empty")
	if sk.Name != "empty" {
		t.Errorf("name = %q", sk.Name)
	}
	if !strings.Contains(sk.Body, "body here") {
		t.Errorf("body = %q", sk.Body)
	}
}

// ===== Skills scanning =====

func TestSkillsLoadFromGlobal(t *testing.T) {
	dir := t.TempDir()
	writeSkill(t, dir, "skill-a", "---\nname: skill-a\ndescription: alpha\n---\nbody A")
	s := &Skills{globalDir: dir}
	skills := s.LoadAll()
	if len(skills) != 1 || skills[0].Name != "skill-a" {
		t.Errorf("LoadAll = %+v, want [skill-a]", skills)
	}
}

func TestSkillsLoadFromProjectOverlapsGlobal(t *testing.T) {
	global := t.TempDir()
	writeSkill(t, global, "shared", "---\nname: shared\ndescription: GLOBAL\n---\nglobal body")

	// Project-level directories need a .git parent to be discovered by discoverDirs (stops at .git root)
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, ".git"), 0o755)
	projSkills := filepath.Join(root, ".creator", "skills")
	writeSkill(t, projSkills, "shared", "---\nname: shared\ndescription: PROJECT\n---\nproject body")

	s := &Skills{globalDir: global, workDir: root}
	skills := s.LoadAll()
	if len(skills) != 1 {
		t.Fatalf("want 1 skill, got %d", len(skills))
	}
	if skills[0].Description != "PROJECT" {
		t.Errorf("project didn't override global: %+v", skills[0])
	}
}

func TestSkillsEmptyDir(t *testing.T) {
	s := &Skills{globalDir: t.TempDir(), workDir: t.TempDir()}
	if skills := s.LoadAll(); len(skills) != 0 {
		t.Errorf("empty dirs should yield 0 skills, got %d", len(skills))
	}
}

// TestSkillsIgnoresLooseMarkdown verifies a bare *.md file (no skill subdirectory) is not discovered:
// the standard requires a directory containing SKILL.md.
func TestSkillsIgnoresLooseMarkdown(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "loose.md"), []byte("---\nname: loose\n---\nbody"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := &Skills{globalDir: dir}
	if skills := s.LoadAll(); len(skills) != 0 {
		t.Errorf("loose .md should be ignored, got %+v", skills)
	}
}

func TestSkillsLookup(t *testing.T) {
	dir := t.TempDir()
	writeSkill(t, dir, "findme", "---\nname: findme\ndescription: d\n---\nsecret body")
	s := &Skills{globalDir: dir}
	sk, ok := s.Lookup("findme")
	if !ok {
		t.Fatal("Lookup failed")
	}
	if sk.Body != "secret body" {
		t.Errorf("body = %q", sk.Body)
	}
	if _, ok := s.Lookup("nonexistent"); ok {
		t.Error("Lookup of nonexistent should return false")
	}
}

// ===== BeforeAgent injection =====

func TestSkillsBeforeAgentInjectsSummaries(t *testing.T) {
	dir := t.TempDir()
	writeSkill(t, dir, "a", "---\nname: a\ndescription: alpha\n---\nbody")
	s := &Skills{globalDir: dir}
	st := &core.RunState{Messages: []core.Message{core.SystemMessage("base")}}
	_ = s.BeforeAgent(context.Background(), st)
	c := st.Messages[0].Content
	if !strings.HasPrefix(c, "base") {
		t.Error("base system prompt lost")
	}
	if !strings.Contains(c, skillsStart) || !strings.Contains(c, skillsEnd) {
		t.Error("boundary markers missing")
	}
	if !strings.Contains(c, "alpha") {
		t.Errorf("description summary missing: %q", c)
	}
	if strings.Contains(c, "body") {
		t.Error("body should NOT be injected into system prompt")
	}
}

func TestSkillsBeforeAgentIdempotent(t *testing.T) {
	dir := t.TempDir()
	writeSkill(t, dir, "a", "---\nname: a\ndescription: d\n---\nbody")
	s := &Skills{globalDir: dir}
	st := &core.RunState{Messages: []core.Message{core.SystemMessage("base")}}
	_ = s.BeforeAgent(context.Background(), st)
	_ = s.BeforeAgent(context.Background(), st)
	_ = s.BeforeAgent(context.Background(), st)
	if n := strings.Count(st.Messages[0].Content, skillsStart); n != 1 {
		t.Errorf("start markers = %d, want 1", n)
	}
}

func TestSkillsBeforeAgentNoSkills(t *testing.T) {
	s := &Skills{globalDir: t.TempDir()}
	st := &core.RunState{Messages: []core.Message{core.SystemMessage("base")}}
	_ = s.BeforeAgent(context.Background(), st)
	if st.Messages[0].Content != "base" {
		t.Error("no skills should not modify system prompt")
	}
}

// ===== Budget degradation =====

func TestSkillsBudgetDegradation(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 10; i++ {
		name := "skill-" + string(rune('a'+i))
		writeSkill(t, dir, name,
			"---\nname: "+name+"\ndescription: "+strings.Repeat("desc ", 50)+"\n---\nbody")
	}
	s := &Skills{globalDir: dir, Budget: 100} // tiny budget forces degradation
	st := &core.RunState{Messages: []core.Message{core.SystemMessage("base")}}
	_ = s.BeforeAgent(context.Background(), st)
	c := st.Messages[0].Content
	if !strings.Contains(c, "names only") {
		t.Error("expected budget degradation to names-only")
	}
}

func TestSkillsBudgetDefaultAllowsFull(t *testing.T) {
	dir := t.TempDir()
	writeSkill(t, dir, "a", "---\nname: a\ndescription: short\n---\nbody")
	s := NewSkills() // default 25000 budget
	s.globalDir = dir
	st := &core.RunState{Messages: []core.Message{core.SystemMessage("base")}}
	_ = s.BeforeAgent(context.Background(), st)
	c := st.Messages[0].Content
	if strings.Contains(c, "names only") {
		t.Error("small skill set should not trigger degradation")
	}
	if !strings.Contains(c, "short") {
		t.Error("full description should be present")
	}
}

// ===== stripSkillsBlock =====

func TestStripSkillsBlock(t *testing.T) {
	s := "before" + skillsStart + "\ncontent\n" + skillsEnd + "after"
	if got := stripBlock(s, skillsStart, skillsEnd); got != "beforeafter" {
		t.Errorf("strip = %q", got)
	}
}

// TestSkillsWithDirs covers WithDirs (additional scan directories).
func TestSkillsWithDirs(t *testing.T) {
	extra := t.TempDir()
	writeSkill(t, extra, "extra", "---\nname: extra\ndescription: d\n---\nextra body")
	s := NewSkills().WithDirs(extra)
	if s == nil {
		t.Fatal("WithDirs should return non-nil Skills")
	}
	sk, ok := s.Lookup("extra")
	if !ok {
		t.Fatal("skill in extra dir should be found via WithDirs")
	}
	if sk.Body != "extra body" {
		t.Errorf("body = %q, want 'extra body'", sk.Body)
	}
}

// TestSkillsLookupSkill covers LookupSkill (core.SkillProvider interface implementation).
func TestSkillsLookupSkill(t *testing.T) {
	dir := t.TempDir()
	writeSkill(t, dir, "provider-test", "---\nname: provider-test\ndescription: a skill\n---\nthe body")
	s := &Skills{globalDir: dir}

	info, ok := s.LookupSkill("provider-test")
	if !ok {
		t.Fatal("LookupSkill should find existing skill")
	}
	if info.Name != "provider-test" || info.Description != "a skill" || info.Body != "the body" {
		t.Errorf("SkillInfo projection wrong: %+v", info)
	}
	if _, ok := s.LookupSkill("nope"); ok {
		t.Error("LookupSkill of nonexistent should return false")
	}
}

// TestSkillsImplementsSkillProvider is a compile-time check that Skills implements core.SkillProvider.
func TestSkillsImplementsSkillProvider(t *testing.T) {
	var _ core.SkillProvider = (*Skills)(nil)
}

// ===== Hard tool whitelist enforcement (WrapTool) =====

func TestSkillsWrapToolEnforcesWhitelist(t *testing.T) {
	s := NewSkills()
	s.setActiveTools(map[string]bool{"read": true, "grep": true, "skill": true, "todo_write": true})

	called := false
	wrappedRead := s.WrapTool("read", func(_ context.Context, _ json.RawMessage) (core.ToolResult, error) {
		called = true
		return core.ToolResult{Content: "ok"}, nil
	})
	res, _ := wrappedRead(context.Background(), json.RawMessage(`{}`))
	if !called {
		t.Errorf("read should be allowed (in whitelist)")
	}
	if res.IsError {
		t.Errorf("read should succeed, got error: %s", res.Content)
	}

	called = false
	wrappedWrite := s.WrapTool("write", func(_ context.Context, _ json.RawMessage) (core.ToolResult, error) {
		called = true
		return core.ToolResult{Content: "should not reach"}, nil
	})
	res2, _ := wrappedWrite(context.Background(), json.RawMessage(`{}`))
	if called {
		t.Errorf("write should be BLOCKED (not in whitelist); the inner func was called")
	}
	if !res2.IsError {
		t.Errorf("write should return IsError (blocked by whitelist)")
	}
}

func TestSkillsWrapToolNoWhitelistAllowsAll(t *testing.T) {
	s := NewSkills()
	called := false
	wrapped := s.WrapTool("bash", func(_ context.Context, _ json.RawMessage) (core.ToolResult, error) {
		called = true
		return core.ToolResult{Content: "ok"}, nil
	})
	res, _ := wrapped(context.Background(), json.RawMessage(`{}`))
	if !called {
		t.Errorf("bash should be allowed when no skill active")
	}
	if res.IsError {
		t.Errorf("bash should succeed (no whitelist active)")
	}
}

func TestSkillsBeforeAgentClearsWhitelist(t *testing.T) {
	s := NewSkills()
	s.setActiveTools(map[string]bool{"read": true, "skill": true})
	_ = s.BeforeAgent(context.Background(), &core.RunState{Messages: []core.Message{}})
	if !s.toolAllowed("bash") {
		t.Errorf("After BeforeAgent, bash should be allowed (whitelist cleared)")
	}
}

// TestSkillsWrapToolSkillToolSetsWhitelist verifies that calling the `skill` tool sets the whitelist
// from the loaded skill's allowed-tools. Uses a real skill folder on disk so Lookup resolves.
func TestSkillsWrapToolSkillToolSetsWhitelist(t *testing.T) {
	dir := t.TempDir()
	s := NewSkills()
	s.globalDir = dir

	writeSkill(t, dir, "scoped", "---\nname: scoped\ndescription: test\nallowed-tools: Read Grep\n---\n# Scoped skill\nbody\n")

	wrappedSkill := s.WrapTool("skill", func(_ context.Context, _ json.RawMessage) (core.ToolResult, error) {
		return core.ToolResult{Content: "skill body"}, nil
	})
	res, _ := wrappedSkill(context.Background(), json.RawMessage(`{"name":"scoped"}`))
	if res.IsError {
		t.Errorf("skill tool call should succeed")
	}
	if !s.toolAllowed("read") {
		t.Errorf("read should be allowed after loading skill with [read, grep] whitelist")
	}
	if s.toolAllowed("write") {
		t.Errorf("write should be blocked after loading skill with [read, grep] whitelist")
	}
}
