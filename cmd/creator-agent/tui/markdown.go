package tui

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/clipperhouse/uax29/v2/graphemes"
)

type styledRun struct {
	text  string
	style Style
}

type styledLine struct {
	runs     []styledRun
	barColor Color // non-default => render a left bar '┃' at col 0 in this color
	lineBg   Color // non-default => fill the row with this background before the runs
	indent   int   // content start column offset from the row's left edge (after the bar)
}

func (sl *styledLine) appendRun(text string, st Style) {
	if text == "" {
		return
	}
	sl.runs = append(sl.runs, styledRun{text: text, style: st})
}

// wrapStyledLine wraps a single styledLine to the given display width, returning one or more
// styledLines. Each grapheme cluster keeps the style of the run it came from. Wrapping is
// display-width aware: a wide (CJK/emoji) cluster that does not fit the remaining row width starts
// a new line rather than being split, and a space at the wrap boundary is consumed (not carried to
// the next line). Iterates grapheme clusters (not runes) so ZWJ emoji sequences like 👨‍👩👧 are
// measured as a single unit with width 2, preventing inflated line widths that misalign dividers.
//
// This fixes the "ragged right edge" defect: finalized markdown lines were previously clipped at
// the area width instead of wrapped, so long CJK paragraphs lost content and their visible edge
// looked misaligned row-to-row.
func wrapStyledLine(sl styledLine, width int) []styledLine {
	if width < 2 {
		return []styledLine{sl}
	}
	var out []styledLine
	var cur styledLine
	curW := 0
	flush := func() {
		out = append(out, cur)
		cur = styledLine{}
		curW = 0
	}
	for _, run := range sl.runs {
		iter := graphemes.FromString(run.text)
		for iter.Next() {
			cluster := iter.Value()
			cw := strW(cluster)
			// Consume a space at the start of a continuation line (avoids leading-space wrap artifact).
			if cluster == " " && curW == 0 && len(out) > 0 {
				continue
			}
			if cw <= 0 {
				// Zero-width cluster (combining/variation): attach to the current run without advancing.
				cur.appendRun(cluster, run.style)
				continue
			}
			if curW+cw > width {
				// than width), emit it anyway to avoid an infinite loop.
				if curW == 0 {
					cur.appendRun(cluster, run.style)
					flush()
					continue
				}
				flush()
				if cluster == " " {
					continue
				}
			}
			cur.appendRun(cluster, run.style)
			curW += cw
		}
	}
	flush()
	if len(out) == 0 {
		return []styledLine{{}}
	}
	return out
}

// wrapStyledLines wraps each line in lines to width (see wrapStyledLine).
func wrapStyledLines(lines []styledLine, width int) []styledLine {
	if width < 2 {
		return lines
	}
	var out []styledLine
	for _, ln := range lines {
		out = append(out, wrapStyledLine(ln, width)...)
	}
	return out
}

// markdownCache caches rendered output by source text to avoid re-parsing finalized blocks.
//
// The cache key includes the active theme name: rendered styledLines carry colors resolved from
// the palette at render time, so a theme switch must invalidate them. Baking the theme into the key
// (rather than clearing the map) keeps the old entries around harmlessly and lets a /themes round
// trip reuse the prior render with no re-parse.
type markdownCache struct {
	cache map[markdownKey][]styledLine
}

// markdownKey is the structured cache identity. Using a struct (rather than a NUL-delimited string)
// avoids any ambiguity from control characters in the source text.
type markdownKey struct {
	theme string
	src   string
}

func newMarkdownCache() *markdownCache {
	return &markdownCache{cache: make(map[markdownKey][]styledLine)}
}

func (m *markdownCache) render(src string) []styledLine {
	key := markdownKey{theme: pal().name, src: src}
	if cached, ok := m.cache[key]; ok {
		return cached
	}
	out := parseMarkdown(src)
	m.cache[key] = out
	return out
}

func parseMarkdown(src string) []styledLine {
	rawLines := strings.Split(src, "\n")
	var out []styledLine
	inFence := false
	fenceMarker := ""
	var codeBuf strings.Builder

	flushCode := func() {
		if codeBuf.Len() == 0 {
			return
		}
		codeBg := pal().backgroundElement
		body := strings.TrimRight(codeBuf.String(), "\n")
		for _, cl := range strings.Split(body, "\n") {
			sl := styledLine{lineBg: codeBg}
			sl.runs = highlightCodeLine(cl)
			out = append(out, sl)
		}
		codeBuf.Reset()
	}

	for _, line := range rawLines {
		trimmed := strings.TrimRight(line, " \t")

		if !inFence {
			if fm, ok := matchFence(trimmed); ok {
				inFence = true
				fenceMarker = fm
				continue
			}
		} else {
			if isFenceClose(trimmed, fenceMarker) {
				inFence = false
				fenceMarker = ""
				flushCode()
				continue
			}
			codeBuf.WriteString(line)
			codeBuf.WriteByte('\n')
			continue
		}

		if strings.TrimSpace(trimmed) == "" {
			out = append(out, styledLine{})
			continue
		}

		if isHRule(trimmed) {
			var sl styledLine
			// ASCII dashes avoid the ambiguous-width box-drawing char.
			sl.appendRun(strings.Repeat("-", 40), styleToolDim())
			out = append(out, sl)
			continue
		}

		if lvl, rest, ok := matchHeading(trimmed); ok {
			_ = lvl
			var sl styledLine
			renderInline(&sl, strings.TrimSpace(rest), styleBold())
			out = append(out, sl)
			continue
		}

		if strings.HasPrefix(trimmed, ">") {
			rest := strings.TrimSpace(strings.TrimPrefix(trimmed, ">"))
			var sl styledLine
			// ASCII pipe avoids the ambiguous-width box-drawing char.
			sl.appendRun("| ", styleToolDim())
			renderInline(&sl, rest, styleThinking())
			out = append(out, sl)
			continue
		}

		if marker, rest, ok := matchListItem(trimmed); ok {
			var sl styledLine
			sl.appendRun("  "+marker+" ", styleToolName())
			renderInline(&sl, strings.TrimSpace(rest), styleAssistant())
			out = append(out, sl)
			continue
		}

		var sl styledLine
		renderInline(&sl, trimmed, styleAssistant())
		out = append(out, sl)
	}

	if inFence {
		flushCode()
	}
	return out
}

func renderInline(sl *styledLine, s string, base Style) {
	i := 0
	for i < len(s) {
		if s[i] == '`' {
			if end := strings.IndexByte(s[i+1:], '`'); end >= 0 {
				code := s[i+1 : i+1+end]
				sl.appendRun(code, styleCode())
				i += end + 2
				continue
			}
		}
		if s[i] == '[' {
			if closeIdx := strings.IndexByte(s[i:], ']'); closeIdx > 0 && i+closeIdx+1 < len(s) && s[i+closeIdx+1] == '(' {
				text := s[i+1 : i+closeIdx]
				if urlEnd := strings.IndexByte(s[i+closeIdx+2:], ')'); urlEnd >= 0 {
					sl.appendRun(text, styleToolName())
					i += closeIdx + 2 + urlEnd + 1
					continue
				}
			}
		}
		if strings.HasPrefix(s[i:], "**") || strings.HasPrefix(s[i:], "__") {
			marker := s[i : i+2]
			if end := strings.Index(s[i+2:], marker); end >= 0 {
				inner := s[i+2 : i+2+end]
				renderInlineNoCode(sl, inner, styleBold())
				i += 2 + end + 2
				continue
			}
		}
		if (s[i] == '*' || s[i] == '_') && !(i+1 < len(s) && s[i+1] == s[i]) {
			if end := strings.IndexByte(s[i+1:], s[i]); end >= 0 {
				inner := s[i+1 : i+1+end]
				renderInlineNoCode(sl, inner, styleItalic())
				i += 1 + end + 1
				continue
			}
		}
		start := i
		for i < len(s) && s[i] != '`' && s[i] != '[' && s[i] != '*' && s[i] != '_' {
			i++
		}
		if i == start {
			sl.appendRun(s[start:start+1], base)
			i = start + 1
		} else {
			sl.appendRun(s[start:i], base)
		}
	}
}

// renderInlineNoCode is renderInline without code/link handling (used inside bold/italic to avoid
// double-processing). It still recurses for the opposite emphasis.
func renderInlineNoCode(sl *styledLine, s string, base Style) {
	i := 0
	for i < len(s) {
		if strings.HasPrefix(s[i:], "**") || strings.HasPrefix(s[i:], "__") {
			marker := s[i : i+2]
			if end := strings.Index(s[i+2:], marker); end >= 0 {
				renderInlineNoCode(sl, s[i+2:i+2+end], styleBold())
				i += 2 + end + 2
				continue
			}
		}
		if (s[i] == '*' || s[i] == '_') && !(i+1 < len(s) && s[i+1] == s[i]) {
			if end := strings.IndexByte(s[i+1:], s[i]); end >= 0 {
				renderInlineNoCode(sl, s[i+1:i+1+end], styleItalic())
				i += 1 + end + 1
				continue
			}
		}
		start := i
		for i < len(s) && s[i] != '*' && s[i] != '_' {
			i++
		}
		if i == start {
			sl.appendRun(s[start:start+1], base)
			i = start + 1
		} else {
			sl.appendRun(s[start:i], base)
		}
	}
}

func matchFence(line string) (string, bool) {
	t := strings.TrimSpace(line)
	if strings.HasPrefix(t, "```") {
		return "```", true
	}
	if strings.HasPrefix(t, "~~~") {
		return "~~~", true
	}
	return "", false
}

func isFenceClose(line, marker string) bool {
	t := strings.TrimSpace(line)
	return t == marker
}

func isHRule(line string) bool {
	t := strings.TrimSpace(line)
	if len(t) < 3 {
		return false
	}
	c := t[0]
	if c != '-' && c != '=' && c != '*' {
		return false
	}
	for i := 0; i < len(t); i++ {
		if t[i] != c {
			return false
		}
	}
	return true
}

func matchHeading(line string) (int, string, bool) {
	t := strings.TrimLeft(line, " ")
	level := 0
	for level < 6 && level < len(t) && t[level] == '#' {
		level++
	}
	if level == 0 || level >= len(t) || t[level] != ' ' {
		return 0, "", false
	}
	return level, t[level+1:], true
}

func matchListItem(line string) (string, string, bool) {
	t := strings.TrimLeft(line, " ")
	if len(t) >= 2 && (t[0] == '-' || t[0] == '*' || t[0] == '+') && t[1] == ' ' {
		return string(t[0]), t[2:], true
	}
	dot := strings.IndexByte(t, '.')
	if dot > 0 && dot < 5 {
		num := t[:dot]
		allDigits := true
		for _, c := range num {
			if c < '0' || c > '9' {
				allDigits = false
				break
			}
		}
		if allDigits && dot+1 < len(t) && t[dot+1] == ' ' {
			return num + ".", t[dot+2:], true
		}
	}
	return "", "", false
}

var commonKeywords = map[string]bool{
	"break": true, "case": true, "catch": true, "chan": true, "class": true,
	"const": true, "continue": true, "def": true, "default": true, "defer": true,
	"do": true, "else": true, "elif": true, "enum": true, "export": true,
	"extends": true, "false": true, "False": true, "fn": true, "for": true,
	"func": true, "go": true, "if": true, "impl": true, "import": true,
	"in": true, "interface": true, "let": true, "loop": true, "map": true,
	"match": true, "module": true, "mut": true, "nil": true, "None": true,
	"package": true, "pub": true, "range": true, "return": true, "select": true,
	"self": true, "Self": true, "struct": true, "super": true, "switch": true,
	"this": true, "true": true, "True": true, "type": true, "var": true,
	"void": true, "while": true, "with": true, "yield": true, "async": true,
	"await": true, "try": true, "throw": true, "throws": true, "new": true,
	"static": true, "public": true, "private": true, "protected": true, "println": true,
	"print": true, "echo": true, "from": true, "as": true, "is": true,
	"not": true, "and": true, "or": true, "pass": true, "lambda": true,
	"null": true, "undefined": true,
}

// highlightCodeLine tokenizes one code line into styled runs. It never returns an empty slice;
// a blank line yields a single default-style run.
func highlightCodeLine(line string) []styledRun {
	// and avoids scanning the line as code.
	if t := strings.TrimLeft(line, " \t"); strings.HasPrefix(t, "//") || strings.HasPrefix(t, "#") {
		return []styledRun{{text: line, style: styleSyntaxComment()}}
	}
	defaultStyle := styleSyntaxCodeBlock()
	var runs []styledRun
	i := 0
	n := len(line)
	for i < n {
		c := line[i]
		switch {
		case c == '/' && i+1 < n && line[i+1] == '/':
			runs = append(runs, styledRun{text: line[i:], style: styleSyntaxComment()})
			i = n
		case c == '"' || c == '\'' || c == '`':
			j := consumeString(line, i, c)
			runs = append(runs, styledRun{text: line[i:j], style: styleSyntaxString()})
			i = j
		case c >= '0' && c <= '9':
			j := consumeNumber(line, i)
			runs = append(runs, styledRun{text: line[i:j], style: styleSyntaxNumber()})
			i = j
		case isIdentStart(c):
			j := consumeIdent(line, i)
			word := line[i:j]
			st := defaultStyle
			switch {
			case commonKeywords[word]:
				st = styleSyntaxKeyword()
			case j < n && line[j] == '(':
				st = styleSyntaxFunction()
			}
			runs = append(runs, styledRun{text: word, style: st})
			i = j
		default:
			j := i
			for j < n && !isCodeTokenStart(line[j]) {
				j++
			}
			if j == i {
				j++ // guarantee progress
			}
			runs = append(runs, styledRun{text: line[i:j], style: styleSyntaxPunctuation()})
			i = j
		}
	}
	if len(runs) == 0 {
		runs = []styledRun{{text: line, style: defaultStyle}}
	}
	return mergeRuns(runs)
}

func consumeString(line string, i int, quote byte) int {
	j := i + 1
	for j < len(line) {
		if line[j] == '\\' && j+1 < len(line) {
			j += 2
			continue
		}
		if line[j] == quote {
			return j + 1
		}
		j++
	}
	return j
}

func consumeNumber(line string, i int) int {
	j := i
	if j+1 < len(line) && line[j] == '0' && (line[j+1] == 'x' || line[j+1] == 'X') {
		j += 2
		for j < len(line) && isHexDigit(line[j]) {
			j++
		}
		return j
	}
	for j < len(line) && ((line[j] >= '0' && line[j] <= '9') || line[j] == '.' || line[j] == '_') {
		j++
	}
	return j
}

func isHexDigit(c byte) bool {
	return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
}

func consumeIdent(line string, i int) int {
	j := i
	for j < len(line) && isIdentChar(line[j]) {
		j++
	}
	return j
}

func isIdentStart(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c == '_' || c >= 0x80
}

func isIdentChar(c byte) bool {
	return isIdentStart(c) || (c >= '0' && c <= '9')
}

// isCodeTokenStart reports whether c can start a non-punctuation token (string/number/identifier)
// or a // comment, i.e. the point at which the punctuation run should end.
func isCodeTokenStart(c byte) bool {
	if c == '"' || c == '\'' || c == '`' {
		return true
	}
	if c >= '0' && c <= '9' {
		return true
	}
	if isIdentStart(c) {
		return true
	}
	return false
}

func mergeRuns(runs []styledRun) []styledRun {
	if len(runs) < 2 {
		return runs
	}
	out := make([]styledRun, 0, len(runs))
	for _, r := range runs {
		if r.text == "" {
			continue
		}
		if len(out) > 0 && out[len(out)-1].style == r.style {
			out[len(out)-1].text += r.text
		} else {
			out = append(out, r)
		}
	}
	if len(out) == 0 {
		out = append(out, styledRun{text: "", style: styleSyntaxCodeBlock()})
	}
	return out
}

type diffOp int

const (
	opEqual  diffOp = iota // line present in both old and new (context)
	opDelete               // line only in old
	opInsert               // line only in new
)

type diffLine struct {
	kind diffKind // kindContext/kindAdd/kindDel (render-layer marker)
	text string
}

type diffKind int

const (
	kindContext diffKind = iota
	kindAdd
	kindDel
)

// myersDiff computes a minimal line-level diff between two line slices using the Myers algorithm.
// Returns the edit script (oldest-first): equal/delete/insert operations.
//
// Complexity: the trace search is O((N+M)D), the backtrack needs the V table per trace iteration
// stored in a history slice (O((N+M)D) memory in the worst case). For the snippet sizes this TUI
// handles (bounded by MaxResultChars) this is negligible and far cheaper than the old O(NM) LCS
// with its O(NM) memory and non-minimal output.
func myersDiff(a, b []string) []diffLine {
	n, m := len(a), len(b)
	if n == 0 && m == 0 {
		return nil
	}
	if n == 0 {
		out := make([]diffLine, m)
		for i := range b {
			out[i] = diffLine{kind: kindAdd, text: b[i]}
		}
		return out
	}
	if m == 0 {
		out := make([]diffLine, n)
		for i := range a {
			out[i] = diffLine{kind: kindDel, text: a[i]}
		}
		return out
	}

	max := n + m
	offset := max
	vlen := 2*max + 1
	v := make([]int, vlen) // current iteration furthest-x per diagonal
	trace := make([][]int, 0, max+1)

	v[offset+1] = 0 // diagonal k=1 starts at x=0 (sentinel for the d=0 iteration)
	var d int
found:
	for d = 0; d <= max; d++ {
		vc := make([]int, vlen)
		copy(vc, v)
		trace = append(trace, vc)

		for k := -d; k <= d; k += 2 {
			var x int
			idx := offset + k
			if k == -d || (k != d && v[idx-1] < v[idx+1]) {
				x = v[idx+1] // insertion: x stays, y increases (k decreases)
			} else {
				x = v[idx-1] + 1 // deletion: x increases, y stays
			}
			y := x - k
			for x < n && y < m && a[x] == b[y] {
				x++
				y++
			}
			v[idx] = x
			if x >= n && y >= m {
				break found
			}
		}
	}

	// --- Backtrack: from (n,m) to (0,0), reading the stored V snapshots to recover the path ---
	// We walk d downward, at each step deciding whether we arrived via an insert or delete.
	var ops []diffOp
	x, y := n, m
	for d = len(trace) - 1; d > 0; d-- {
		v = trace[d]
		k := x - y
		idx := offset + k

		var prevK int
		if k == -d || (k != d && v[idx-1] < v[idx+1]) {
			prevK = k + 1 // came from diagonal k+1 via insertion
		} else {
			prevK = k - 1 // came from diagonal k-1 via deletion
		}

		prevX := v[offset+prevK]
		prevY := prevX - prevK

		for x > prevX && y > prevY {
			ops = append(ops, opEqual)
			x--
			y--
		}
		if d > 0 {
			if x == prevX {
				ops = append(ops, opInsert) // y moved: insert b[y-1]
			} else {
				ops = append(ops, opDelete) // x moved: delete a[x-1]
			}
			x = prevX
			y = prevY
		}
	}
	for x > 0 && y > 0 && a[x-1] == b[y-1] {
		ops = append(ops, opEqual)
		x--
		y--
	}

	for i, j := 0, len(ops)-1; i < j; i, j = i+1, j-1 {
		ops[i], ops[j] = ops[j], ops[i]
	}
	out := make([]diffLine, 0, len(ops))
	ai, bi := 0, 0
	for _, op := range ops {
		switch op {
		case opEqual:
			out = append(out, diffLine{kind: kindContext, text: a[ai]})
			ai++
			bi++
		case opDelete:
			out = append(out, diffLine{kind: kindDel, text: a[ai]})
			ai++
		case opInsert:
			out = append(out, diffLine{kind: kindAdd, text: b[bi]})
			bi++
		}
	}
	return out
}

type hunk struct {
	oldStart, oldCount int
	newStart, newCount int
	lines              []diffLine
}

// unifiedHunks splits a diff line sequence into unified diff hunks:
// contiguous non-context lines + surrounding context lines (default 3), hunk context lines are
// not merged between hunks separated by more than 2*context unchanged lines.
//
// Line numbers are exact: as we scan the diff we track old/new running line numbers (1-based),
// so each hunk's oldStart/newStart is correct — no post-hoc fix-up needed.
func unifiedHunks(lines []diffLine, context int) []hunk {
	if context < 0 {
		context = 3
	}
	var changeIdx []int
	for i, l := range lines {
		if l.kind != kindContext {
			changeIdx = append(changeIdx, i)
		}
	}
	if len(changeIdx) == 0 {
		return nil
	}

	oldLine := make([]int, len(lines))
	newLine := make([]int, len(lines))
	o, n := 1, 1
	for i, l := range lines {
		oldLine[i] = o
		newLine[i] = n
		switch l.kind {
		case kindContext:
			o++
			n++
		case kindDel:
			o++
		case kindAdd:
			n++
		}
	}

	var hunks []hunk
	groupStart := changeIdx[0]
	groupEnd := changeIdx[0]
	threshold := 2*context + 1
	flush := func(start, end int) {
		lo := start - context
		if lo < 0 {
			lo = 0
		}
		hi := end + context
		if hi >= len(lines) {
			hi = len(lines) - 1
		}
		h := hunk{
			lines:    lines[lo : hi+1],
			oldStart: oldLine[lo],
			newStart: newLine[lo],
		}
		for _, l := range h.lines {
			switch l.kind {
			case kindContext:
				h.oldCount++
				h.newCount++
			case kindDel:
				h.oldCount++
			case kindAdd:
				h.newCount++
			}
		}
		hunks = append(hunks, h)
	}
	for _, idx := range changeIdx {
		if idx-groupEnd > threshold {
			flush(groupStart, groupEnd)
			groupStart = idx
		}
		groupEnd = idx
	}
	flush(groupStart, groupEnd)
	return hunks
}

func formatHunk(h hunk) string {
	var sb strings.Builder
	oldS := fmt.Sprintf("%d", h.oldStart)
	newS := fmt.Sprintf("%d", h.newStart)
	if h.oldCount != 1 {
		oldS = fmt.Sprintf("%d,%d", h.oldStart, h.oldCount)
	}
	if h.newCount != 1 {
		newS = fmt.Sprintf("%d,%d", h.newStart, h.newCount)
	}
	fmt.Fprintf(&sb, "@@ -%s +%s @@\n", oldS, newS)
	for _, l := range h.lines {
		switch l.kind {
		case kindContext:
			sb.WriteString(" " + l.text + "\n")
		case kindAdd:
			sb.WriteString("+" + l.text + "\n")
		case kindDel:
			sb.WriteString("-" + l.text + "\n")
		}
	}
	return sb.String()
}

func unifiedDiff(old, new string) string {
	oldLines := splitLines(old)
	newLines := splitLines(new)
	if equalSlices(oldLines, newLines) {
		return ""
	}
	lines := myersDiff(oldLines, newLines)
	hunks := unifiedHunks(lines, 3)
	if len(hunks) == 0 {
		return ""
	}
	var sb strings.Builder
	for _, h := range hunks {
		sb.WriteString(formatHunk(h))
	}
	return sb.String()
}

func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	s = strings.ReplaceAll(s, "\r\n", "\n")
	lines := strings.Split(s, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

func equalSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// toolCallInfo holds key fields parsed from tool input JSON (for approval preview / diff).
type toolCallInfo struct {
	path        string
	oldString   string
	newString   string
	command     string
	description string // task tool subtask label
	prompt      string // task tool full instructions for the sub-agent
}

// parseToolInput parses tool input JSON, extracting fields relevant for approval / diff.
// write: {path, content} (newString=content); edit: {path, old_string, new_string};
// bash: {command}; other tools: best-effort path extraction.
func parseToolInput(name, input string) toolCallInfo {
	var info toolCallInfo
	if input == "" {
		return info
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(input), &m); err != nil {
		return info
	}
	info.path, _ = m["path"].(string)
	info.command, _ = m["command"].(string)
	info.oldString, _ = m["old_string"].(string)
	info.newString, _ = m["new_string"].(string)
	info.description, _ = m["description"].(string)
	info.prompt, _ = m["prompt"].(string)
	if info.newString == "" {
		if c, ok := m["content"].(string); ok {
			info.newString = c
		}
	}
	return info
}

func fullFileDiff(info toolCallInfo) string {
	if info.oldString == "" && info.newString == "" {
		return ""
	}
	if info.path != "" && info.oldString != "" {
		if data, err := os.ReadFile(info.path); err == nil {
			oldContent := string(data)
			if idx := strings.Index(oldContent, info.oldString); idx >= 0 {
				newContent := oldContent[:idx] + info.newString + oldContent[idx+len(info.oldString):]
				return unifiedDiff(oldContent, newContent)
			}
		}
		// File not found / old_string not found -> fallback to fragment diff
		return unifiedDiff(info.oldString, info.newString)
	}
	if info.path != "" {
		oldContent := ""
		if data, err := os.ReadFile(info.path); err == nil {
			oldContent = string(data)
		}
		return unifiedDiff(oldContent, info.newString)
	}
	return unifiedDiff(info.oldString, info.newString)
}

// renderDiffHunked returns the unified-diff text, truncated per line by width (no wrapping,
// to avoid misalignment). Coloring is applied by the render layer based on the +/-/@@ prefixes,
// so this function is pure (no styling dependency).
func renderDiffHunked(diff string, width int) string {
	if diff == "" {
		return "(no changes)"
	}
	if width <= 0 {
		width = 80
	}
	var sb strings.Builder
	for _, line := range strings.Split(strings.TrimRight(diff, "\n"), "\n") {
		wrapped := wrapDiffLine(line, width-1)
		for _, wl := range wrapped {
			sb.WriteString(wl)
			sb.WriteString("\n")
		}
	}
	return strings.TrimRight(sb.String(), "\n")
}

// wrapDiffLine word-wraps a single diff line into display lines that fit within maxW columns.
// Continuation lines are prefixed with "  " so they visually align under the original line.
// The diff prefix (+, -, @, space) is preserved on the first line only. Width is measured in
// display columns using grapheme clusters (strW), so CJK/emoji content wraps correctly instead
// of overflowing. Iterates grapheme clusters (not runes) so ZWJ emoji sequences are measured as
// a single unit with width 2.
func wrapDiffLine(line string, maxW int) []string {
	if maxW <= 0 {
		return []string{line}
	}
	if strW(line) <= maxW {
		return []string{line}
	}
	const contPrefix = "  "
	contW := maxW - strW(contPrefix)
	if contW <= 0 {
		contW = maxW
	}
	var out []string
	iter := graphemes.FromString(line)
	var cur strings.Builder
	curW := 0
	limit := maxW
	prefix := ""
	for iter.Next() {
		cluster := iter.Value()
		cw := strW(cluster)
		if curW+cw > limit {
			out = append(out, prefix+cur.String())
			cur.Reset()
			curW = 0
			limit = contW
			prefix = contPrefix
		}
		cur.WriteString(cluster)
		curW += cw
	}
	if cur.Len() > 0 {
		out = append(out, prefix+cur.String())
	}
	if len(out) == 0 {
		return []string{line}
	}
	return out
}
