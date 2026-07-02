package tui

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/skys-mission/creator-agent/cmd/creator-agent/tui/i18n"
)

// mentionFileCap bounds how many bytes of a referenced file are injected, to protect the context
// budget (a huge file shouldn't flood the model). Files larger than this are skipped entirely.
const mentionFileCap = 64 * 1024

// mentionTokenRe matches an @path or @path#N or @path#N-M reference at the start of input or after
// whitespace. The captured token (after '@') is non-whitespace, non-@, and may carry a trailing
// #range. Used at submit-time to resolve references (not for the live popup, which uses the
// stricter mentionTriggerIndex on the in-progress input).
var mentionTokenRe = regexp.MustCompile(`(?:^|\s)@([^\s@]+)`)

func resolveMentions(text string) string {
	var blocks []string
	seen := map[string]bool{}
	for _, m := range mentionTokenRe.FindAllStringSubmatchIndex(text, -1) {
		// m[2],m[3] = the captured token after '@'.
		token := text[m[2]:m[3]]
		path, start, end, hasRange := parseMentionRange(token)
		if path == "" {
			continue
		}
		// Avoid duplicate work for the same (path, range) reference.
		key := token
		if seen[key] {
			continue
		}
		seen[key] = true
		if strings.HasSuffix(path, "/") {
			continue
		}
		if block, ok := readMentionBlock(path, start, end, hasRange); ok {
			blocks = append(blocks, block)
		}
	}
	if len(blocks) == 0 {
		return text
	}
	var sb strings.Builder
	sb.WriteString(text)
	sb.WriteString(i18n.T("mention.header"))
	sb.WriteString(strings.Join(blocks, "\n"))
	return sb.String()
}

// readMentionBlock reads the file at path (relative to cwd), optionally slices it to the 1-indexed
// inclusive [start, end] line range, and formats it as a <path>/<content> block with line numbers.
// ok is false when the file is missing/unreadable/oversized (best-effort skip).
func readMentionBlock(path string, start, end int, hasRange bool) (string, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	if len(data) > mentionFileCap {
		return "", false // protect the context budget
	}
	lines := strings.Split(string(data), "\n")
	lo, hi := 1, len(lines)
	if hasRange {
		lo = start
		if lo < 1 {
			lo = 1
		}
		if end > 0 {
			hi = end
		} else if end == 0 && !strings.HasSuffix(path, "#") {
			hi = start
		}
		if hi > len(lines) {
			hi = len(lines)
		}
		if lo > hi {
			lo = hi
		}
	}
	var sb strings.Builder
	sb.WriteString("<path>")
	sb.WriteString(path)
	sb.WriteString("</path>\n<content>\n")
	for i := lo; i <= hi; i++ {
		sb.WriteString(strconv.Itoa(i))
		sb.WriteString(": ")
		sb.WriteString(lines[i-1])
		sb.WriteString("\n")
	}
	sb.WriteString("</content>")
	return sb.String(), true
}

// fileIgnoreDirs mirrors the glob/grep tools' defaultIgnoreDirs (core/builtins/grep.go). Kept as a
// local copy to avoid a tui -> core/builtins dependency (tui already imports core, but pulling the
// builtins package in just for this list would widen the dependency surface unnecessarily).
var fileIgnoreDirs = []string{".git", "vendor", "node_modules", "dist", "build"}

// fileIndexCap bounds the number of paths indexed. A typical project is well under this; huge
// monorepos would make the popup sluggish and the popup caps display at 10 anyway.
const fileIndexCap = 2000

func buildFileIndex(root string) []string {
	ignoreSet := make(map[string]struct{}, len(fileIgnoreDirs))
	for _, d := range fileIgnoreDirs {
		ignoreSet[d] = struct{}{}
	}
	var out []string
	stop := false
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || stop {
			return nil
		}
		if d.IsDir() {
			if path != root {
				if _, skip := ignoreSet[d.Name()]; skip {
					return filepath.SkipDir
				}
				rel, _ := filepath.Rel(root, path)
				rel = filepath.ToSlash(rel)
				out = append(out, rel+"/")
				if len(out) >= fileIndexCap {
					stop = true
					return filepath.SkipAll
				}
			}
			return nil
		}
		if shouldSkipFile(d.Name()) {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		out = append(out, rel)
		if len(out) >= fileIndexCap {
			stop = true
			return filepath.SkipAll
		}
		return nil
	})
	sort.Strings(out)
	return out
}

func shouldSkipFile(name string) bool {
	switch name {
	case ".DS_Store", "Thumbs.db":
		return true
	}
	switch filepath.Ext(name) {
	case ".pyc", ".swp", ".swo", ".log":
		return true
	}
	return false
}

// getFileIndex returns the cached file index for root, rebuilding it when the cache is empty or
// the root directory's mtime changed since the last build. All file-system access is contained
// here so callers can treat it as a pure lookup keyed on (root, root-mtime).
func getFileIndex(a *App, root string) []string {
	info, err := os.Stat(root)
	if err != nil {
		a.fileIndexDir = ""
		a.fileIndex = nil
		return nil
	}
	mtime := info.ModTime()
	if a.fileIndex != nil && a.fileIndexDir == root && eqTime(a.fileIndexMtime, mtime) {
		return a.fileIndex
	}
	a.fileIndex = buildFileIndex(root)
	a.fileIndexDir = root
	a.fileIndexMtime = mtime
	return a.fileIndex
}

// eqTime reports whether two ModTimes are equal at second precision. Sub-second drift from FS
// metadata reads should not trigger redundant rebuilds.
func eqTime(a, b time.Time) bool {
	return a.Unix() == b.Unix()
}

// mentionTriggerIndex finds the index of the `@` that should open the file-mention popup, or -1.
// Rules (mirror opencode's prompt/display.ts:mentionTriggerIndex):
//   - the `@` must be at position 0 OR immediately preceded by whitespace;
//   - there must be no whitespace between the `@` and the end of input (the user is still typing
//     the path token).
//
// Returns the byte index of the triggering `@`, or -1 when no trigger applies.
func mentionTriggerIndex(v string) int {
	// Only the last `@` matters (the one nearest the cursor / end of input).
	idx := strings.LastIndex(v, "@")
	if idx < 0 {
		return -1
	}
	// Must be at start or preceded by whitespace.
	if idx > 0 {
		prev := v[idx-1]
		if prev != ' ' && prev != '\t' && prev != '\n' {
			return -1
		}
	}
	tail := v[idx+1:]
	if strings.ContainsAny(tail, " \t\n") {
		return -1
	}
	return idx
}

func extractMentionQuery(v string) string {
	idx := mentionTriggerIndex(v)
	if idx < 0 {
		return ""
	}
	return v[idx+1:]
}

var mentionRangeRe = regexp.MustCompile(`^#(\d+)(?:-(\d*))?$`)

// parseMentionRange splits a mention query into its base path and an optional line range. ok is
// true when the query ends with a parseable `#N` / `#N-M` suffix. start is always the 1-indexed
// start line; end is the 1-indexed end line (0 means "single line / open-ended" when the suffix
// was `#N` or `#N-`).
func parseMentionRange(query string) (base string, start, end int, ok bool) {
	hash := strings.LastIndex(query, "#")
	if hash < 0 {
		return query, 0, 0, false
	}
	m := mentionRangeRe.FindStringSubmatch(query[hash:])
	if m == nil {
		return query, 0, 0, false
	}
	base = query[:hash]
	start = atoi(m[1])
	if m[2] != "" {
		end = atoi(m[2])
	}
	return base, start, end, true
}

func preserveMentionSuffix(query, sel string) (suffix string, ok bool) {
	if !strings.HasPrefix(query, sel) {
		return "", false
	}
	rest := query[len(sel):]
	if rest == "" {
		return "", false
	}
	if mentionRangeRe.MatchString(rest) {
		return rest, true
	}
	return "", false
}

func stripMentionRange(query string) string {
	base, _, _, ok := parseMentionRange(query)
	if !ok {
		return query
	}
	return base
}

// atoi is a local Atoi (avoids importing strconv for a single use); returns 0 on non-numeric.
func atoi(s string) int {
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0
		}
		n = n*10 + int(r-'0')
	}
	return n
}
