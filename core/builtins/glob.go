package builtins

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"

	"github.com/skys-mission/creator-agent/core"
)

// GlobTool finds files by name pattern (supports ** cross-directory wildcard).
type GlobTool struct {
	settings core.ToolSettings
}

// GlobOption configures a GlobTool.
type GlobOption func(*GlobTool)

// WithGlobSettings applies resolved tool settings (already merged by the caller
// through core.ResolveToolSettings). Zero fields are replaced with builtin
// defaults and clamped inside NewGlobTool.
func WithGlobSettings(s core.ToolSettings) GlobOption {
	return func(g *GlobTool) { g.settings = s }
}

// NewGlobTool creates a GlobTool.
func NewGlobTool(opts ...GlobOption) *GlobTool {
	g := &GlobTool{settings: core.ResolveToolSettings(GlobDefaultsInput(), core.ToolSettingsInput{}, core.ToolSettingsInput{})}
	for _, o := range opts {
		o(g)
	}
	g.settings = normalizeGlobSettings(g.settings)
	return g
}

func normalizeGlobSettings(s core.ToolSettings) core.ToolSettings {
	return normalizeSettings(core.ToolSettings(GlobDefaultsInput()), s, false)
}

func (g *GlobTool) Info() core.ToolInfo {
	return core.ToolInfo{
		Name:            "glob",
		Description:     "Find files by name pattern (e.g. \"**/*.go\" or \"core/*.go\"). Input: {\"pattern\": \"\", \"path\": \"dir to search under (default .)\"}. The pattern is matched relative to path.",
		InputSchema:     json.RawMessage(`{"type":"object","properties":{"pattern":{"type":"string"},"path":{"type":"string","description":"directory to search under (default current directory)"}},"required":["pattern"]}`),
		ReadOnly:        true,
		ConcurrencySafe: true,
		MaxResultChars:  g.settings.MaxResultChars,
	}
}

func (g *GlobTool) Exec(ctx context.Context, input json.RawMessage) (core.ToolResult, error) {
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
	if err := validatePattern(args.Pattern); err != nil {
		return core.ToolResult{Content: fmt.Sprintf("invalid pattern %q: %v", args.Pattern, err), IsError: true}, nil
	}
	if args.Path == "" {
		args.Path = "."
	}
	root, err := resolveToolPath(args.Path)
	if err != nil {
		return core.ToolResult{Content: err.Error(), IsError: true}, nil
	}

	ignoreSet := ignoreSetOf(g.settings.IgnoreDirs)

	// Byte budget bounds in-memory result growth: hardMaxPaths × arbitrarily long paths could still
	// blow MaxResultChars, so the walk also stops once the accumulated byte size is reached.
	byteBudget := g.settings.MaxResultChars
	if byteBudget <= 0 || byteBudget > hardMaxResultChars {
		byteBudget = hardMaxResultChars
	}

	patSegs := collapseDoubleStar(strings.Split(args.Pattern, "/"))
	var matches []string
	var bytesAccum int
	var skipped int // directories that could not be inspected (walk errors)
	stop := false
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if stop {
			return nil
		}
		if ctx.Err() != nil {
			stop = true
			return filepath.SkipAll // user interrupted; surfaced after the walk
		}
		if err != nil {
			skipped++
			return nil
		}
		if d.IsDir() {
			if path != root {
				if _, skip := ignoreSet[d.Name()]; skip {
					return filepath.SkipDir
				}
				if walkDepth(path, root) > g.settings.MaxDepth {
					return filepath.SkipDir
				}
			}
			return nil
		}
		if shouldSkipByIgnore(path, ignoreSet) {
			return nil
		}
		// Match the pattern against the path relative to the search root so "*.go" behaves the
		// same regardless of where the walk started.
		rel, rerr := filepath.Rel(root, path)
		if rerr != nil {
			rel = strings.TrimPrefix(path, "./")
		}
		rel = filepath.ToSlash(rel)
		nameSegs := strings.Split(rel, "/")
		if matchSegments(patSegs, nameSegs) {
			matches = append(matches, rel)
			bytesAccum += len(rel) + 1
			// Path-count and byte caps: stop the walk early once either hard limit is hit,
			// preventing unbounded memory growth on huge trees.
			if len(matches) >= hardMaxPaths || bytesAccum >= byteBudget {
				stop = true
				return filepath.SkipAll
			}
		}
		return nil
	})

	if err := ctx.Err(); err != nil {
		return core.ToolResult{}, err
	}
	if len(matches) == 0 {
		return core.ToolResult{Content: "(no files matched)" + skippedNote(skipped)}, nil
	}
	sort.Strings(matches)
	// Trim the joined result back to the byte budget on a rune boundary; the loop layer still spills
	// to disk using MaxResultChars, but the tool honors its own cap first.
	return core.ToolResult{Content: core.TruncateBytesMaxSafe(strings.Join(matches, "\n"), byteBudget) + skippedNote(skipped)}, nil
}

// validatePattern checks whether the pattern contains syntax that filepath.Match cannot parse
// (e.g. unclosed [). Returns ErrBadPattern so the caller errors instead of silently returning empty results.
func validatePattern(pattern string) error {
	for _, seg := range strings.Split(pattern, "/") {
		if seg == "**" {
			continue
		}
		if _, err := filepath.Match(seg, "x"); err != nil { // syntax check only, match result is irrelevant
			return err
		}
	}
	return nil
}

// matchSegments matches ** cross-directory wildcards recursively.
func matchSegments(pat, name []string) bool {
	if len(pat) == 0 {
		return len(name) == 0
	}
	if pat[0] == "**" {
		// ** matches 0 or more segments
		for i := 0; i <= len(name); i++ {
			if matchSegments(pat[1:], name[i:]) {
				return true
			}
		}
		return false
	}
	if len(name) == 0 {
		return false
	}
	ok, _ := filepath.Match(pat[0], name[0])
	if !ok {
		return false
	}
	return matchSegments(pat[1:], name[1:])
}

// collapseDoubleStar merges consecutive ** segments into one.
//
// Prevents exponential backtracking: matchSegments for N ** segments and M path segments
// on a failed match is C(M+N, N) complexity. A pattern like **/**/**/**/*.go from a model
// can take 100ms+ per non-matching path; with thousands of files in a project this stretches
// to minutes (measured: 8x** vs 20 segments = 105ms/path).
//
// Consecutive ** are semantically equivalent to a single ** (both match 0+ segments), so
// merging them yields linear complexity.
func collapseDoubleStar(segs []string) []string {
	out := make([]string, 0, len(segs))
	prevStar := false
	for _, s := range segs {
		if s == "**" {
			if prevStar {
				continue // consecutive **, skip duplicate
			}
			prevStar = true
		} else {
			prevStar = false
		}
		out = append(out, s)
	}
	return out
}
