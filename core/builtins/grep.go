package builtins

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/skys-mission/creator-agent/core"
)

// defaultIgnoreDirs are the directory names skipped during walks when the caller
// does not configure an explicit ignore list. They cover VCS state and common
// dependency/build outputs that bloat results without adding signal.
var defaultIgnoreDirs = []string{".git", "vendor", "node_modules", "dist", "build"}

// GrepTool searches file contents with a regular expression.
type GrepTool struct {
	settings core.ToolSettings
}

// GrepOption configures a GrepTool.
type GrepOption func(*GrepTool)

// WithGrepSettings applies resolved tool settings (already merged by the caller
// through core.ResolveToolSettings across all inheritance layers). Fields at
// zero are replaced with builtin defaults and clamped to hard limits inside
// NewGrepTool, so callers only need to forward the fields they care about.
func WithGrepSettings(s core.ToolSettings) GrepOption {
	return func(g *GrepTool) { g.settings = s }
}

// NewGrepTool creates a GrepTool. Without options it uses builtin defaults
// (ignore .git/vendor/node_modules/dist/build, max 100 matches, walk depth 20,
// result cap 20000 chars).
func NewGrepTool(opts ...GrepOption) *GrepTool {
	g := &GrepTool{settings: core.ResolveToolSettings(GrepDefaultsInput(), core.ToolSettingsInput{}, core.ToolSettingsInput{})}
	for _, o := range opts {
		o(g)
	}
	g.settings = normalizeGrepSettings(g.settings)
	return g
}

func normalizeGrepSettings(s core.ToolSettings) core.ToolSettings {
	return normalizeSettings(core.ToolSettings(GrepDefaultsInput()), s, true)
}

func (g *GrepTool) Info() core.ToolInfo {
	return core.ToolInfo{
		Name:            "grep",
		Description:     "Search file contents with a regex. Input: {\"pattern\": \"regex\", \"path\": \"dir or file (default .)\"}. Returns path:line: match.",
		InputSchema:     json.RawMessage(`{"type":"object","properties":{"pattern":{"type":"string"},"path":{"type":"string"}},"required":["pattern"]}`),
		ReadOnly:        true,
		ConcurrencySafe: true,
		MaxResultChars:  g.settings.MaxResultChars,
	}
}

func (g *GrepTool) Exec(ctx context.Context, input json.RawMessage) (core.ToolResult, error) {
	var args struct {
		Pattern string `json:"pattern"`
		Path    string `json:"path"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return invalidInputResult(err), nil
	}
	if args.Pattern == "" {
		return core.ToolResult{Content: "error: pattern is required", IsError: true}, nil
	}
	if args.Path == "" {
		args.Path = "."
	}
	resolved, err := resolveToolPath(args.Path)
	if err != nil {
		return core.ToolResult{Content: err.Error(), IsError: true}, nil
	}

	re, err := regexp.Compile(args.Pattern)
	if err != nil {
		return core.ToolResult{Content: "bad regex: " + err.Error(), IsError: true}, nil
	}

	ignoreSet := ignoreSetOf(g.settings.IgnoreDirs)
	maxMatches := g.settings.MaxMatches
	// Byte budget caps in-memory growth before the loop-layer spill sees the result.
	// hardMaxMatches alone (up to 10000) × arbitrarily long lines can exceed MaxResultChars
	// and the hard cap, so the walk stops once either boundary is hit.
	byteBudget := g.settings.MaxResultChars
	if byteBudget <= 0 || byteBudget > hardMaxResultChars {
		byteBudget = hardMaxResultChars
	}

	var matches []string
	var bytesAccum int
	var skipped int // paths that could not be inspected (walk errors / unreadable files)
	_ = filepath.WalkDir(resolved, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			skipped++
			return nil
		}
		if d.IsDir() {
			if path != resolved {
				if _, skip := ignoreSet[d.Name()]; skip {
					return filepath.SkipDir
				}
				// Depth guard: stop descending once the configured walk depth
				// is exceeded, defending against pathologically deep trees.
				if walkDepth(path, resolved) > g.settings.MaxDepth {
					return filepath.SkipDir
				}
			}
			return nil
		}
		if shouldSkipByIgnore(path, ignoreSet) {
			return nil
		}
		// Skip oversized files to avoid OOM from GB-sized files. DirEntry has no Size; use Info.
		if info, ierr := d.Info(); ierr == nil && info.Size() > maxReadBytes {
			return nil
		}
		data, rerr := os.ReadFile(path)
		if rerr != nil {
			skipped++
			return nil // skip unreadable files
		}
		lines := strings.Split(string(data), "\n")
		for i, line := range lines {
			if re.MatchString(line) {
				m := fmt.Sprintf("%s:%d: %s", path, i+1, strings.TrimSpace(line))
				matches = append(matches, m)
				bytesAccum += len(m) + 1 // +1 for the join separator
				if bytesAccum >= byteBudget || len(matches) >= maxMatches {
					return filepath.SkipAll
				}
			}
		}
		return nil
	})

	if len(matches) == 0 {
		return core.ToolResult{Content: "(no matches)" + skippedNote(skipped)}, nil
	}
	// bytesAccum may overshoot byteBudget by up to one match (the one that tripped the threshold).
	// Trim the joined result back to the budget on a rune boundary so the tool honors its own cap
	// rather than relying solely on the loop-layer spill.
	return core.ToolResult{Content: core.TruncateBytesMaxSafe(strings.Join(matches, "\n"), byteBudget) + skippedNote(skipped)}, nil
}

// skippedNote returns a short transparency suffix when a walk skipped paths it could not inspect
// (permission errors, unreadable files), so the model does not mistake an incomplete search for an
// exhaustive one. Empty when nothing was skipped.
func skippedNote(skipped int) string {
	if skipped <= 0 {
		return ""
	}
	return fmt.Sprintf("\n(%d path(s) skipped: not readable)", skipped)
}
