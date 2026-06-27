package middlewares

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"

	"github.com/skys-mission/creator-agent/core"
	"github.com/skys-mission/creator-agent/paths"
)

// skillsStart / skillsEnd mark the boundaries of the injected block (idempotent across turns, same as AgentsMd/AutoMemory).
const (
	skillsStart = "<!-- skills:start -->"
	skillsEnd   = "<!-- skills:end -->"
)

// skillManifest is the required file inside each skill directory (Agent Skills standard). The legacy
// lowercase spelling is accepted as a fallback for case-sensitive filesystems.
const (
	skillManifest       = "SKILL.md"
	skillManifestLegacy = "skill.md"
)

// Skill is a single skill loaded from a <dir>/SKILL.md following the Agent Skills standard
// (https://agentskills.io/specification).
//
// Data flow:
//   - name + description stay in the system prompt so the model knows which skills exist;
//   - body is not injected into the system prompt; it is loaded on demand when the model calls SkillTool.
type Skill struct {
	Name        string   // skill name (== directory name, per the spec)
	Description string   // what the skill does and when to use it
	Body        string   // full SKILL.md instructions (loaded on demand)
	Tools       []string // tool whitelist derived from `allowed-tools` (empty = all tools allowed)
	path        string   // source SKILL.md path (debug/cache key)
}

// skillFrontmatter is the YAML frontmatter between the `---` fences of a SKILL.md file. Fields follow
// the Agent Skills specification; license/compatibility/metadata are parsed for forward compatibility
// even though they are not consumed yet.
type skillFrontmatter struct {
	Name          string            `yaml:"name"`
	Description   string            `yaml:"description"`
	AllowedTools  string            `yaml:"allowed-tools"`
	License       string            `yaml:"license"`
	Compatibility string            `yaml:"compatibility"`
	Metadata      map[string]string `yaml:"metadata"`
}

// Skills scans skill directories, injects name+description summaries into the system prompt, and
// serves SkillTool lookups by name.
//
// Sources (project overrides global by skill name):
//   - Global: ~/.creator/skills/<skill>/SKILL.md
//   - Project: walk up from workDir looking for .creator/skills/, stopping at the .git root
//   - Extra: directories from config (skills.dirs)
//
// Budget: when the total injected summary size exceeds Budget, skills degrade to names-only.
type Skills struct {
	core.BaseMiddleware

	globalDir string   // injected for tests; empty means default ~/.creator/skills
	workDir   string   // injected for tests; empty means os.Getwd
	extraDirs []string // additional scan directories (config.Skills.Dirs)

	// Budget is the byte budget for injected summaries (default 25000).
	Budget int

	cache fileCache[Skill]

	skillMu     sync.RWMutex
	activeTools map[string]bool
}

// NewSkills creates a Skills middleware with a default Budget of 25000.
func NewSkills() *Skills {
	return &Skills{Budget: 25000}
}

// WithDirs sets additional scan directories (config.Skills.Dirs).
func (s *Skills) WithDirs(dirs ...string) *Skills {
	s.extraDirs = append(s.extraDirs, dirs...)
	return s
}

// LoadAll scans all sources and returns discovered skills (deduplicated by name; project-level
// overrides global with the same name). Uses mtime caching to avoid re-reading every turn.
func (s *Skills) LoadAll() []Skill {
	dirs := s.discoverDirs()
	byName := map[string]Skill{}
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			manifest, ok := skillManifestPath(filepath.Join(dir, e.Name()))
			if !ok {
				continue
			}
			if sk, ok := s.cachedLoad(manifest, e.Name()); ok {
				byName[sk.Name] = sk // later directories override earlier ones (project-level overrides global)
			}
		}
	}
	out := make([]Skill, 0, len(byName))
	for _, sk := range byName {
		out = append(out, sk)
	}
	return out
}

// skillManifestPath returns the SKILL.md path inside a candidate skill directory, preferring the
// canonical SKILL.md and falling back to the legacy lowercase spelling.
func skillManifestPath(skillDir string) (string, bool) {
	for _, name := range []string{skillManifest, skillManifestLegacy} {
		p := filepath.Join(skillDir, name)
		if info, err := os.Stat(p); err == nil && !info.IsDir() {
			return p, true
		}
	}
	return "", false
}

// Lookup finds a skill by name (internal use).
func (s *Skills) Lookup(name string) (Skill, bool) {
	for _, sk := range s.LoadAll() {
		if sk.Name == name {
			return sk, true
		}
	}
	return Skill{}, false
}

// LookupSkill implements core.SkillProvider (used by builtins.SkillTool; returns a core.SkillInfo projection).
func (s *Skills) LookupSkill(name string) (core.SkillInfo, bool) {
	sk, ok := s.Lookup(name)
	if !ok {
		return core.SkillInfo{}, false
	}
	return core.SkillInfo{Name: sk.Name, Description: sk.Description, Body: sk.Body, Tools: sk.Tools}, true
}

// BeforeAgent injects skill summaries into the system prompt (idempotent; body is not injected),
// and clears the active-skill tool whitelist (a new user turn = no skill is active).
// LoadAll before setActiveTools so all callers use mu → skillMu lock order (consistent with WrapTool).
func (s *Skills) BeforeAgent(_ context.Context, st *core.RunState) error {
	skills := s.LoadAll()
	s.setActiveTools(nil) // reset per-turn (after LoadAll for consistent lock order)
	if len(skills) == 0 {
		return nil
	}
	block := buildSkillsBlock(skills, s.Budget)
	if block == "" {
		return nil
	}
	st.Messages = rewriteSystemBlock(st.Messages, skillsStart, skillsEnd, block)
	return nil
}

// WrapTool enforces the active-skill tool whitelist. When the `skill` tool is called, it loads the
// skill body AND sets the whitelist (so subsequent tool calls in the same turn are restricted).
// When any other tool is called and a whitelist is active, non-whitelisted tools are blocked.
func (s *Skills) WrapTool(name string, next core.ToolFunc) core.ToolFunc {
	return func(ctx context.Context, input json.RawMessage) (core.ToolResult, error) {
		// The `skill` tool itself: let it run, then apply the whitelist from the loaded skill.
		if name == "skill" {
			res, err := next(ctx, input)
			if err != nil {
				return res, err
			}
			var args struct {
				Name string `json:"name"`
			}
			if json.Unmarshal(input, &args) == nil && args.Name != "" {
				if sk, ok := s.Lookup(args.Name); ok {
					if len(sk.Tools) > 0 {
						allow := make(map[string]bool, len(sk.Tools)+2)
						for _, t := range sk.Tools {
							allow[t] = true
						}
						// Always allow the skill tool itself + todo_write (planning aid within a skill).
						allow["skill"] = true
						allow["todo_write"] = true
						s.setActiveTools(allow)
					} else {
						// No whitelist → clear any prior restriction so the next skill does not inherit it.
						s.setActiveTools(nil)
					}
				}
			}
			return res, nil
		}
		// Non-skill tool: enforce the whitelist if one is active.
		if !s.toolAllowed(name) {
			return core.ToolResult{
				Content: fmt.Sprintf("Tool %q is not allowed by the active skill's tool whitelist. Only the declared tools may be used while a skill is active.", name),
				IsError: true,
			}, nil
		}
		return next(ctx, input)
	}
}

// setActiveTools sets the active-skill tool whitelist (nil = no restriction).
func (s *Skills) setActiveTools(tools map[string]bool) {
	s.skillMu.Lock()
	defer s.skillMu.Unlock()
	s.activeTools = tools
}

// toolAllowed reports whether the tool is allowed under the active-skill whitelist. When no skill is
// active (activeTools is nil), all tools are allowed.
func (s *Skills) toolAllowed(name string) bool {
	s.skillMu.RLock()
	defer s.skillMu.RUnlock()
	if s.activeTools == nil {
		return true // no skill active → unrestricted
	}
	return s.activeTools[name]
}

// buildSkillsBlock constructs the injected block (only name+description summaries; body is excluded).
// When the budget is exceeded, degrades to names-only first.
func buildSkillsBlock(skills []Skill, budget int) string {
	if len(skills) == 0 {
		return ""
	}
	full := renderSkillsSummaries(skills, false)
	if budget <= 0 || len(full) <= budget {
		var sb strings.Builder
		sb.WriteString("\n\n")
		sb.WriteString(skillsStart)
		sb.WriteString("\n# Available skills (invoke the `skill` tool with the skill name to load full instructions)\n")
		sb.WriteString(full)
		sb.WriteString(skillsEnd)
		return sb.String()
	}
	namesOnly := renderSkillsSummaries(skills, true)
	var sb strings.Builder
	sb.WriteString("\n\n")
	sb.WriteString(skillsStart)
	sb.WriteString("\n# Available skills (budget exceeded — names only; invoke `skill` tool to load)\n")
	sb.WriteString(namesOnly)
	sb.WriteString(skillsEnd)
	return sb.String()
}

// renderSkillsSummaries renders a skill summary list. When namesOnly is true, only the name is listed.
func renderSkillsSummaries(skills []Skill, namesOnly bool) string {
	var sb strings.Builder
	for _, sk := range skills {
		if namesOnly {
			fmt.Fprintf(&sb, "- %s\n", sk.Name)
			continue
		}
		sb.WriteString("- **")
		sb.WriteString(sk.Name)
		sb.WriteString("**")
		if sk.Description != "" {
			sb.WriteString(": ")
			sb.WriteString(sk.Description)
		}
		sb.WriteString("\n")
	}
	return sb.String()
}

// discoverDirs collects all scan directories: global + project-level (walk up to .git root) + extra.
// Order: global first, then project-level (LoadAll later entries override earlier ones, so project-level overrides global).
func (s *Skills) discoverDirs() []string {
	var dirs []string
	gd := s.globalDir
	if gd == "" {
		if d, err := paths.SkillsDir(); err == nil {
			gd = d
		}
	}
	if gd != "" {
		dirs = append(dirs, gd)
	}
	wd := s.workDir
	if wd == "" {
		if d, err := os.Getwd(); err == nil {
			wd = d
		}
	}
	if wd != "" {
		walkUpToGitRoot(wd, func(d string) bool {
			cand := filepath.Join(d, paths.ProjectDir, paths.SkillsDirName)
			if info, err := os.Stat(cand); err == nil && info.IsDir() {
				dirs = append(dirs, cand)
			}
			return false
		})
	}
	dirs = append(dirs, s.extraDirs...)
	return dirs
}

// cachedLoad reads and parses a SKILL.md with mtime caching. dirName is the parent directory name,
// which is authoritative for the skill name per the Agent Skills spec.
func (s *Skills) cachedLoad(manifestPath, dirName string) (Skill, bool) {
	info, err := os.Stat(manifestPath)
	if err != nil {
		return Skill{}, false
	}
	return s.cache.get(manifestPath, info.ModTime(), func() (Skill, bool) {
		data, err := os.ReadFile(manifestPath)
		if err != nil {
			return Skill{}, false
		}
		return parseSkill(string(data), manifestPath, dirName), true
	})
}

// parseSkill parses a SKILL.md (YAML frontmatter + Markdown body). The skill name is the directory
// name (spec requirement); a mismatched frontmatter `name` is ignored in favor of the directory.
//
// Format:
//
//	---
//	name: my-skill
//	description: what it does and when to use it
//	allowed-tools: Read Grep Bash(git:*)
//	---
//	<markdown body>
func parseSkill(content, path, dirName string) Skill {
	sk := Skill{path: path, Name: dirName}
	trimmed := strings.TrimLeft(content, "\n\r\t ")
	if !strings.HasPrefix(trimmed, "---") {
		sk.Body = strings.TrimSpace(content)
		return sk
	}
	afterFirst := strings.TrimLeft(trimmed[3:], "\r\n")
	endIdx := strings.Index(afterFirst, "\n---")
	if endIdx < 0 {
		sk.Body = strings.TrimSpace(content)
		return sk
	}
	fmText := afterFirst[:endIdx]
	bodyStart := endIdx + 4 // skip "\n---"
	body := ""
	if bodyStart < len(afterFirst) {
		body = strings.TrimLeft(afterFirst[bodyStart:], "\r\n")
	}

	var fm skillFrontmatter
	if err := yaml.Unmarshal([]byte(fmText), &fm); err == nil {
		sk.Description = strings.TrimSpace(fm.Description)
		sk.Tools = parseAllowedTools(fm.AllowedTools)
	}
	sk.Body = strings.TrimSpace(body)
	return sk
}

// parseAllowedTools converts the Agent Skills `allowed-tools` field (a space-separated string such as
// "Read Grep Bash(git:*)") into this agent's built-in tool names. For each token it strips any
// argument pattern after "(", lowercases the base name, and maps it to a known built-in. Unknown
// tokens are dropped (they simply cannot widen the whitelist). An empty field yields nil (no
// restriction).
func parseAllowedTools(field string) []string {
	field = strings.TrimSpace(field)
	if field == "" {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	for _, tok := range strings.Fields(field) {
		base := tok
		if i := strings.IndexByte(base, '('); i >= 0 {
			base = base[:i]
		}
		base = strings.ToLower(strings.TrimSpace(base))
		name, ok := builtinToolName(base)
		if !ok || seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	return out
}

// builtinToolName maps a normalized (lowercased) allowed-tools token to this agent's registered
// built-in tool name. It accepts both this agent's own names and the common capitalized names used in
// upstream Agent Skills examples (Read/Write/Edit/Bash/Grep/Glob/Task).
func builtinToolName(token string) (string, bool) {
	switch token {
	case "read", "view":
		return "read", true
	case "write":
		return "write", true
	case "edit":
		return "edit", true
	case "bash", "shell":
		return "bash", true
	case "grep":
		return "grep", true
	case "glob":
		return "glob", true
	case "task":
		return "task", true
	case "todo_write", "todowrite", "todo":
		return "todo_write", true
	case "skill":
		return "skill", true
	default:
		return "", false
	}
}

// Compile-time check that Skills implements Middleware.
var _ core.Middleware = (*Skills)(nil)
