package tui

// diff_test.go covers the stdlib-pure Myers diff engine + parseToolInput + renderDiffHunked
// (now a pure string transform) + the markdown helper. No tcell Screen needed.

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestMyersDiffAllEqual(t *testing.T) {
	lines := myersDiff([]string{"a", "b", "c"}, []string{"a", "b", "c"})
	for _, l := range lines {
		if l.kind != kindContext {
			t.Errorf("expected all context, got %v", l.kind)
		}
	}
}

func TestMyersDiffAddOnly(t *testing.T) {
	lines := myersDiff([]string{"a"}, []string{"a", "b", "c"})
	inserts := 0
	for _, l := range lines {
		if l.kind == kindAdd {
			inserts++
		}
	}
	if inserts != 2 {
		t.Errorf("expected 2 inserts, got %d", inserts)
	}
}

func TestMyersDiffDelOnly(t *testing.T) {
	lines := myersDiff([]string{"a", "b", "c"}, []string{"a"})
	dels := 0
	for _, l := range lines {
		if l.kind == kindDel {
			dels++
		}
	}
	if dels != 2 {
		t.Errorf("expected 2 deletes, got %d", dels)
	}
}

func TestMyersDiffMixed(t *testing.T) {
	lines := myersDiff([]string{"x", "b", "c"}, []string{"a", "b", "d"})
	var kinds []diffKind
	for _, l := range lines {
		kinds = append(kinds, l.kind)
	}
	// Expect at least one delete (x) and one insert (a, d adjustments).
	hasDel, hasAdd := false, false
	for _, k := range kinds {
		if k == kindDel {
			hasDel = true
		}
		if k == kindAdd {
			hasAdd = true
		}
	}
	if !hasDel || !hasAdd {
		t.Errorf("expected mixed del+add, got %v", kinds)
	}
}

func TestMyersDiffHunkLineNumbers(t *testing.T) {
	out := unifiedDiff("a\nb\nc", "a\nB\nc")
	// Should contain a @@ hunk header.
	if !strings.Contains(out, "@@") {
		t.Errorf("expected hunk header, got %q", out)
	}
}

func TestParseToolInputWrite(t *testing.T) {
	info := parseToolInput("write", `{"path":"/tmp/x","content":"new"}`)
	if info.path != "/tmp/x" || info.newString != "new" {
		t.Errorf("write parse wrong: %+v", info)
	}
}

func TestParseToolInputEdit(t *testing.T) {
	info := parseToolInput("edit", `{"path":"/tmp/y","old_string":"a","new_string":"b"}`)
	if info.path != "/tmp/y" || info.oldString != "a" || info.newString != "b" {
		t.Errorf("edit parse wrong: %+v", info)
	}
}

func TestParseToolInputBash(t *testing.T) {
	info := parseToolInput("bash", `{"command":"ls -la"}`)
	if info.command != "ls -la" {
		t.Errorf("bash parse wrong: %+v", info)
	}
}

func TestParseToolInputInvalidJSON(t *testing.T) {
	info := parseToolInput("write", `{bad`)
	if info.path != "" {
		t.Errorf("expected empty on bad JSON, got %+v", info)
	}
}

func TestParseToolInputEmpty(t *testing.T) {
	info := parseToolInput("write", "")
	if info.path != "" {
		t.Errorf("expected empty on empty input, got %+v", info)
	}
}

func TestFullFileDiffWriteNewFile(t *testing.T) {
	info := toolCallInfo{path: "/nonexistent/xyz", newString: "hello"}
	out := fullFileDiff(info)
	// New file -> content lines are inserts; the @@ hunk header is allowed.
	for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		if line == "" {
			continue
		}
		if !strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "@@") {
			t.Errorf("new file diff should be +/@@ only, got %q", line)
		}
	}
}

func TestRenderDiffHunkedPure(t *testing.T) {
	// renderDiffHunked is now pure (no styling); verify it returns the lines truncated.
	diff := "+a\n-b\nc"
	out := renderDiffHunked(diff, 80)
	if !strings.Contains(out, "+a") || !strings.Contains(out, "-b") {
		t.Errorf("renderDiffHunked lost content: %q", out)
	}
}

func TestRenderDiffHunkedTruncation(t *testing.T) {
	long := "+" + strings.Repeat("x", 200)
	out := renderDiffHunked(long, 50)
	// Width 50 -> each line fits within width-1 = 49 columns (long lines wrap, not truncate).
	for _, line := range strings.Split(out, "\n") {
		if len([]rune(line)) > 60 {
			t.Errorf("line not wrapped: %d runes", len([]rune(line)))
		}
	}
}

func TestWrapDiffLine(t *testing.T) {
	// ASCII: first line fills maxW, continuation lines are 2-space indented.
	got := wrapDiffLine(strings.Repeat("x", 10), 4)
	// 10 chars at width 4 -> "xxxx","  xx","  xx","  xx".
	if len(got) != 4 || got[0] != "xxxx" || got[1] != "  xx" {
		t.Fatalf("ascii wrap wrong: %#v", got)
	}
	// Short line passes through unchanged.
	if out := wrapDiffLine("abc", 10); len(out) != 1 || out[0] != "abc" {
		t.Fatalf("short line should pass through: %#v", out)
	}
	// CJK regression: 6 runes = width 12, so width-12 fits in one line.
	cjk := "中文测试文本" // 6 runes, display width 12
	if out := wrapDiffLine(cjk, 12); len(out) != 1 {
		t.Fatalf("cjk width-12 should fit one line, got %#v", out)
	}
	// At width 7 it must wrap, and crucially no output line may exceed 7 columns — the prior
	// rune-count implementation measured runes (6 <= 7) and left the line un-wrapped, so it
	// overflowed the viewport edge instead of wrapping.
	for i, ln := range wrapDiffLine(cjk, 7) {
		if w := strW(ln); w > 7 {
			t.Errorf("cjk line %d overran: width=%d %q", i, w, ln)
		}
	}
}

func TestParseMarkdownHeading(t *testing.T) {
	lines := parseMarkdown("# Title\nbody")
	if len(lines) < 2 {
		t.Fatalf("expected >=2 lines, got %d", len(lines))
	}
	// Heading line should have at least one run.
	if len(lines[0].runs) == 0 {
		t.Errorf("heading line empty")
	}
}

func TestParseMarkdownCodeFence(t *testing.T) {
	src := "```go\nfmt.Println(\"hi\")\n```\nafter"
	lines := parseMarkdown(src)
	// Expect: code line, then "after" line (blank fence-open line dropped).
	found := false
	for _, l := range lines {
		for _, r := range l.runs {
			if strings.Contains(r.text, "Println") {
				found = true
			}
		}
	}
	if !found {
		t.Errorf("code fence content lost")
	}
}

func TestParseMarkdownInlineCode(t *testing.T) {
	lines := parseMarkdown("use `code` here")
	// Should contain a run with text "code" in accent style.
	found := false
	for _, l := range lines {
		for _, r := range l.runs {
			if r.text == "code" {
				found = true
			}
		}
	}
	if !found {
		t.Errorf("inline code not split out")
	}
}

func TestParseMarkdownBulletList(t *testing.T) {
	lines := parseMarkdown("- one\n- two")
	// Two list lines.
	if len(lines) < 2 {
		t.Fatalf("expected >=2 list lines, got %d", len(lines))
	}
}

func TestMatchCommandsPrefix(t *testing.T) {
	c := matchCommands("/mo")
	has := false
	for _, s := range c {
		if s == "/model" {
			has = true
		}
	}
	if !has {
		t.Errorf("/mo should match /model, got %v", c)
	}
}

func TestAppendHistoryDedupTail(t *testing.T) {
	h := appendHistory(nil, "a")
	h = appendHistory(h, "a") // dup tail -> ignored
	if len(h) != 1 {
		t.Errorf("dup tail not ignored: %v", h)
	}
	h = appendHistory(h, "b")
	if len(h) != 2 || h[0] != "a" || h[1] != "b" {
		t.Errorf("history wrong: %v", h)
	}
}

func TestAppendHistoryCap1000(t *testing.T) {
	h := make([]string, 0, 1001)
	for i := 0; i < 1001; i++ {
		h = appendHistory(h, string(rune('a'+i%26))+itoa(i))
	}
	if len(h) > 1000 {
		t.Errorf("history not capped: %d", len(h))
	}
}

// itoa is a tiny helper to vary history entries without importing strconv in the test data.
func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}

func TestTruncateStrRuneBoundary(t *testing.T) {
	// Multi-byte: '世' is 3 bytes. Truncating at byte 2 must back off to a rune boundary.
	s := "世世世"
	got := truncateStr(s, 2)
	// Must be valid UTF-8 (no mid-rune cut) and end with ASCII ellipsis "...". (The single-rune
	// ellipsis '…' is ambiguous-width; truncateStr suffix uses ASCII "..." to avoid width drift.)
	if !strings.HasSuffix(got, "...") {
		t.Errorf("expected ellipsis suffix \"...\", got %q", got)
	}
	// Length: should be ellipsis only (1 byte boundary before first rune) since byte 2 is mid-rune.
	if len([]rune(got)) > 3 {
		t.Errorf("expected at most 3 runes (the ellipsis), got %q", got)
	}
}

func TestTruncateStrW(t *testing.T) {
	// Fits: unchanged.
	if got := truncateStrW("abc", 5); got != "abc" {
		t.Errorf("no-truncation case wrong: %q", got)
	}
	// ASCII truncation respects display width and appends ellipsis.
	if got := truncateStrW("abcdef", 4); got != "abc…" {
		t.Errorf("ascii truncation wrong: %q", got)
	}
	// CJK: 4 runes = width 8. Budget 5 keeps 2 runes (width 4) + "…" (width 1). The byte-based
	// truncateStr would miscount and overrun here.
	got := truncateStrW("中文测试文本", 5)
	if strW(got) > 5 {
		t.Errorf("cjk truncated width overran: %q (width %d)", got, strW(got))
	}
	if !strings.HasSuffix(got, "…") {
		t.Errorf("expected ellipsis suffix, got %q", got)
	}
	// Non-positive budget is safe.
	if got := truncateStrW("abc", 0); got != "" {
		t.Errorf("zero budget should be empty: %q", got)
	}
}

// TestTruncateBytesMaxRuneBoundary verifies truncateBytesMax never emits invalid UTF-8 by
// splitting a multi-byte rune, and returns a prefix no longer than the byte budget.
func TestTruncateBytesMaxRuneBoundary(t *testing.T) {
	// '世' is 3 bytes (E4 B8 96). Budget of 4 bytes must back off to byte 3 (one full rune),
	// not cut into the second rune.
	s := "世世世"
	got := truncateBytesMax(s, 4)
	if !utf8.ValidString(got) {
		t.Errorf("truncateBytesMax produced invalid UTF-8: %q", got)
	}
	if len(got) > 4 {
		t.Errorf("truncateBytesMax exceeded budget: %d bytes for budget 4 (%q)", len(got), got)
	}
	// Budget within the string: short input is returned unchanged.
	if got := truncateBytesMax("abc", 10); got != "abc" {
		t.Errorf("short input should be unchanged: %q", got)
	}
	// Zero/negative budget is empty.
	if got := truncateBytesMax("abc", 0); got != "" {
		t.Errorf("zero budget should be empty: %q", got)
	}
}

func TestRenderChangesListDedup(t *testing.T) {
	changes := []changeRecord{
		{path: "/a", tool: "edit"},
		{path: "/a", tool: "write"},
		{path: "/b", tool: "edit"},
	}
	out := renderChangesList(changes)
	if !strings.Contains(out, "2 files") || !strings.Contains(out, "3 operations") {
		t.Errorf("dedup summary wrong: %q", out)
	}
}

func TestRenderChangesListEmpty(t *testing.T) {
	out := renderChangesList(nil)
	if !strings.Contains(out, "No file changes") {
		t.Errorf("empty changes wrong: %q", out)
	}
}

// TestInputMultibyteInsert verifies multi-byte (Chinese) input does not corrupt the buffer.
// Regression for the byte-slice-with-rune-offset bug that produced mojibake.
func TestInputMultibyteInsert(t *testing.T) {
	var b inputBuffer
	b.SetWidth(80)
	// Insert CJK runes one at a time (mimics typing 你好世界).
	for _, r := range "你好世界" {
		b.InsertRune(r)
	}
	got := b.Value()
	if got != "你好世界" {
		t.Errorf("after inserting 你好世界 one rune at a time, got %q", got)
	}
	// Cursor at end.
	if b.cur != 4 {
		t.Errorf("cursor at %d, want 4", b.cur)
	}
}

// TestInputMultibyteInsertMidBuffer inserts ASCII then CJK then ASCII, exercising byte/rune
// indexing across a mixed string.
func TestInputMultibyteInsertMidBuffer(t *testing.T) {
	var b inputBuffer
	b.SetWidth(80)
	b.InsertString("ab") // cursor at end (2)
	if b.Value() != "ab" {
		t.Fatalf("setup: got %q", b.Value())
	}
	b.CursorLeft()    // cursor at 1 (between a and b)
	b.InsertRune('中') // insert CJK between -> "a中b"
	if got := b.Value(); got != "a中b" {
		t.Errorf("mid insert: got %q want a中b", got)
	}
	if b.cur != 2 {
		t.Errorf("cursor at %d, want 2", b.cur)
	}
}

// TestInputMultibyteBackspace deletes a CJK rune and checks no leftover bytes.
func TestInputMultibyteBackspace(t *testing.T) {
	var b inputBuffer
	b.SetWidth(80)
	b.InsertString("你好")
	b.Backspace() // delete 你's neighbor 好
	if got := b.Value(); got != "你" {
		t.Errorf("backspace: got %q want 你", got)
	}
}

// TestInputCursorPosMultibyte checks the cursor visual position counts display width (not runes).
// CJK runes are width 2 each, so after 你好 the cursor sits at column 4.
func TestInputCursorPosMultibyte(t *testing.T) {
	var b inputBuffer
	b.SetWidth(80)
	b.InsertString("你好") // 2 runes, width 2 each -> display width 4
	line, col := b.cursorPos()
	if line != 0 || col != 4 {
		t.Errorf("cursorPos after 2 CJK runes: line=%d col=%d, want 0,4", line, col)
	}
}

// TestInputCursorPosMixed verifies mixed ASCII + CJK cursor column sums display widths.
func TestInputCursorPosMixed(t *testing.T) {
	var b inputBuffer
	b.SetWidth(80)
	b.InsertString("ab你") // ab=2, 你=2 -> total display width 4
	_, col := b.cursorPos()
	if col != 4 {
		t.Errorf("cursorPos after 'ab你': col=%d, want 4", col)
	}
}

// TestInputCursorPosWidth2InMiddle ensures cursor after inserting CJK mid-buffer lands right.
func TestInputCursorPosAfterCJKMidBuffer(t *testing.T) {
	var b inputBuffer
	b.SetWidth(80)
	b.InsertString("a中b") // a=1, 中=2, b=1 -> at end col 4
	b.CursorLeft()        // before 'b' -> col 3
	_, col := b.cursorPos()
	if col != 3 {
		t.Errorf("cursor before 'b' (after a中): col=%d, want 3", col)
	}
}
