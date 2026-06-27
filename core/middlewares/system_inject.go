package middlewares

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/skys-mission/creator-agent/core"
)

// stripBlock removes every closed block delimited by start..end from s.
// An unclosed block (start with no later end) is left intact to avoid accidental deletion.
func stripBlock(s, start, end string) string {
	for {
		i := strings.Index(s, start)
		if i < 0 {
			return s
		}
		j := strings.Index(s, end)
		if j < i {
			return s
		}
		s = s[:i] + s[j+len(end):]
	}
}

// rewriteSystemBlock appends block to the first system message, stripping any existing
// block delimited by start..end first (idempotent across turns). If there is no system
// message, a new one is prepended.
func rewriteSystemBlock(msgs []core.Message, start, end, block string) []core.Message {
	if len(msgs) > 0 && msgs[0].Role == core.RoleSystem {
		msgs[0].Content = stripBlock(msgs[0].Content, start, end) + block
		return msgs
	}
	return append([]core.Message{core.SystemMessage(strings.TrimLeft(block, "\n"))}, msgs...)
}

// walkUpToGitRoot invokes visit for start and each parent directory, stopping after the
// directory that contains .git (or at the filesystem root). visit returns stop=true to
// short-circuit the walk.
func walkUpToGitRoot(start string, visit func(dir string) (stop bool)) {
	dir := start
	for {
		if visit(dir) {
			return
		}
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return
		}
		dir = parent
	}
}
