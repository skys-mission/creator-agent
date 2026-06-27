package middlewares

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/skys-mission/creator-agent/core"
	"github.com/skys-mission/creator-agent/paths"
)

// agentsMdStart / agentsMdEnd mark the boundaries of the injected block.
//
// Purpose: idempotent across turns. Session history is persisted (including the
// injected system message), so BeforeAgent must strip the old block before
// rewriting to keep only one copy.
const (
	agentsMdStart = "<!-- agents-md:start -->"
	agentsMdEnd   = "<!-- agents-md:end -->"
)

// AgentsMd injects project memory into the system prompt (convention files: AGENTS.md / CLAUDE.md).
//
// A single source is injected, resolved by a fallback chain (first hit wins):
//   - For each directory walking up from workDir to the .git root:
//     AGENTS.md → CLAUDE.md → .creator/AGENTS.md
//   - If nothing is found in the project, the global ~/.creator/AGENTS.md
//
// File contents are cached by mtime to avoid disk reads every turn.
type AgentsMd struct {
	core.BaseMiddleware

	globalPath string // injected for tests; empty means default ~/.creator/AGENTS.md
	workDir    string // injected for tests; empty means os.Getwd

	cache fileCache[string]
}

var _ core.Middleware = (*AgentsMd)(nil)

// NewAgentsMd creates an AgentsMd middleware with default settings.
func NewAgentsMd() *AgentsMd { return &AgentsMd{} }

// BeforeAgent appends the resolved AGENTS.md content to the first system message before execution
// (idempotent: strips the old block first).
func (a *AgentsMd) BeforeAgent(_ context.Context, st *core.RunState) error {
	content, scope := a.resolve()
	if content == "" {
		return nil
	}
	st.Messages = rewriteSystemBlock(st.Messages, agentsMdStart, agentsMdEnd, buildAgentsMdBlock(content, scope))
	return nil
}

// buildAgentsMdBlock builds a bounded injection block with a single source. scope ("project" or
// "global") is recorded in the header for transparency.
func buildAgentsMdBlock(content, scope string) string {
	var sb strings.Builder
	sb.WriteString("\n\n")
	sb.WriteString(agentsMdStart)
	sb.WriteString("\n# ")
	sb.WriteString(scope)
	sb.WriteString(" context (AGENTS.md — follow these instructions; do not mention the section itself)\n")
	sb.WriteString(content)
	sb.WriteString("\n")
	sb.WriteString(agentsMdEnd)
	return sb.String()
}

// resolve returns the first matching AGENTS.md content and its scope label ("project" or "global"),
// following the fallback chain. Empty content means nothing was found.
func (a *AgentsMd) resolve() (content, scope string) {
	if body := a.readProject(); body != "" {
		return body, "Project"
	}
	if body := a.readGlobal(); body != "" {
		return body, "Global"
	}
	return "", ""
}

// readProject walks up from workDir; at each level it tries AGENTS.md, then CLAUDE.md, then
// .creator/AGENTS.md, stopping at the .git root. The first hit wins.
func (a *AgentsMd) readProject() string {
	dir := a.workDir
	if dir == "" {
		var err error
		dir, err = os.Getwd()
		if err != nil {
			return ""
		}
	}
	var found string
	walkUpToGitRoot(dir, func(d string) bool {
		candidates := []string{
			filepath.Join(d, "AGENTS.md"),
			filepath.Join(d, "CLAUDE.md"),
			filepath.Join(d, paths.ProjectDir, paths.AgentsMdFileName),
		}
		for _, p := range candidates {
			if body, ok := a.cachedRead(p); ok {
				found = body
				return true
			}
		}
		return false
	})
	return found
}

// readGlobal reads the global AGENTS.md (~/.creator/AGENTS.md).
func (a *AgentsMd) readGlobal() string {
	p := a.globalPath
	if p == "" {
		gp, err := paths.GlobalAgentsMd()
		if err != nil {
			return ""
		}
		p = gp
	}
	body, ok := a.cachedRead(p)
	if !ok {
		return ""
	}
	return body
}

func (a *AgentsMd) cachedRead(path string) (string, bool) {
	info, err := os.Stat(path)
	if err != nil {
		return "", false
	}
	return a.cache.get(path, info.ModTime(), func() (string, bool) {
		data, err := os.ReadFile(path)
		if err != nil {
			return "", false
		}
		return string(data), true
	})
}
