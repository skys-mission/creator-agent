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
		Description:     "Find files by name pattern (e.g. \"**/*.go\" or \"core/*.go\"). Input: {\"pattern\": \"\"}.",
		InputSchema:     json.RawMessage(`{"type":"object","properties":{"pattern":{"type":"string"}},"required":["pattern"]}`),
		ReadOnly:        true,
		ConcurrencySafe: true,
		MaxResultChars:  g.settings.MaxResultChars,
	}
}

func (g *GlobTool) Exec(ctx context.Context, input json.RawMessage) (core.ToolResult, error) {
	var args struct {
		Pattern string `json:"pattern"`
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

	ignoreSet := ignoreSetOf(g.settings.IgnoreDirs)

	patSegs := collapseDoubleStar(strings.Split(args.Pattern, "/"))
	var matches []string
	var skipped int // directories that could not be inspected (walk errors)
	stop := false
	_ = filepath.WalkDir(".", func(path string, d fs.DirEntry, err error) error {
		if stop {
			return nil
		}
		if err != nil {
			skipped++
			return nil
		}
		if d.IsDir() {
			if path != "." {
				if _, skip := ignoreSet[d.Name()]; skip {
					return filepath.SkipDir
				}
				if walkDepth(path, ".") > g.settings.MaxDepth {
					return filepath.SkipDir
				}
			}
			return nil
		}
		if shouldSkipByIgnore(path, ignoreSet) {
			return nil
		}
		rel := strings.TrimPrefix(path, "./")
		nameSegs := strings.Split(rel, "/")
		if matchSegments(patSegs, nameSegs) {
			matches = append(matches, rel)
			// Path-count cap: stop the walk early once the hard limit is hit,
			// preventing unbounded memory growth on huge trees.
			if len(matches) >= hardMaxPaths {
				stop = true
				return filepath.SkipAll
			}
		}
		return nil
	})

	if len(matches) == 0 {
		return core.ToolResult{Content: "(no files matched)" + skippedNote(skipped)}, nil
	}
	sort.Strings(matches)
	// Note: large outputs are spilled to disk by the loop layer using MaxResultChars; the tool does not truncate on its own.
	return core.ToolResult{Content: strings.Join(matches, "\n") + skippedNote(skipped)}, nil
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
