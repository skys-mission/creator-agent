package tui

// filemention_test.go covers the @-mention file autocomplete: the file index builder, the trigger
// predicate, live refine, and commit (inserting "@<path> "). File-index-dependent behavior is
// tested by seeding a.cache directly for determinism; buildFileIndex is exercised against a
// real tempdir.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// newAppWithFileIndex builds a sim App with a pre-seeded file index, so refineFileCompletions has
// deterministic data without touching the real working directory. The cache is marked valid by
// stamping the real "." mtime, so getFileIndex's validation passes and returns the seed.
func newAppWithFileIndex(t *testing.T, w, h int, files []string) *App {
	t.Helper()
	a, _ := newAppWithSim(t, w, h)
	a.fileIndex = files
	a.fileIndexDir = "."
	if info, err := os.Stat("."); err == nil {
		a.fileIndexMtime = info.ModTime()
	}
	return a
}

// TestMentionTriggerIndex covers the @ trigger rules (position 0 or whitespace-prefixed; no
// whitespace between @ and end of input).
func TestMentionTriggerIndex(t *testing.T) {
	cases := []struct {
		in   string
		want int // index of triggering @, or -1
	}{
		{"", -1},
		{"@", 0},
		{"@foo", 0},
		{"hello", -1},    // no @
		{"foo@bar", -1},  // @ not at start and not preceded by whitespace
		{" @foo", 1},     // space before @ -> triggers
		{"a @b", 2},      // space before @ -> triggers
		{"@foo bar", -1}, // whitespace after @ -> closed
		{"x @foo", 2},    // space before @ -> triggers
		{"\t@x", 1},      // tab before @ -> triggers
	}
	for _, tc := range cases {
		if got := mentionTriggerIndex(tc.in); got != tc.want {
			t.Errorf("mentionTriggerIndex(%q) = %d, want %d", tc.in, got, tc.want)
		}
	}
}

// TestExtractMentionQuery covers query extraction (text after the triggering @).
func TestExtractMentionQuery(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"@", ""},
		{"@foo", "foo"},
		{"x @bar", "bar"},
		{"@foo bar", ""}, // no trigger (whitespace) -> empty
		{"foo@bar", ""},  // no trigger (mid-word) -> empty
	}
	for _, tc := range cases {
		if got := extractMentionQuery(tc.in); got != tc.want {
			t.Errorf("extractMentionQuery(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestBuildFileIndex walks a tempdir and verifies it returns relative paths, skips ignored dirs,
// and respects the cap.
func TestBuildFileIndex(t *testing.T) {
	dir := t.TempDir()
	// Create a few files.
	mustWriteFile(t, filepath.Join(dir, "main.go"), "package main")
	mustWriteFile(t, filepath.Join(dir, "README.md"), "readme")
	mustWriteFile(t, filepath.Join(dir, "sub", "util.go"), "package sub")
	// An ignored dir should be skipped entirely.
	mustWriteFile(t, filepath.Join(dir, "node_modules", "pkg", "index.js"), "x")
	mustWriteFile(t, filepath.Join(dir, ".git", "config"), "x")
	// A noisy file should be skipped.
	mustWriteFile(t, filepath.Join(dir, ".DS_Store"), "x")

	got := buildFileIndex(dir)
	gotStr := strings.Join(got, "\n")
	for _, want := range []string{"main.go", "README.md", "sub/util.go"} {
		// Normalize separators for Windows just in case.
		wantNorm := filepath.ToSlash(want)
		if !strings.Contains(gotStr, wantNorm) {
			t.Errorf("index missing %s; got:\n%s", wantNorm, gotStr)
		}
	}
	// Ignored/noisy entries must be absent.
	for _, bad := range []string{"node_modules", ".git", ".DS_Store"} {
		if strings.Contains(gotStr, bad) {
			t.Errorf("index should skip %s; got:\n%s", bad, gotStr)
		}
	}
}

// TestBuildFileIndexCap verifies the index stops at the cap.
func TestBuildFileIndexCap(t *testing.T) {
	dir := t.TempDir()
	// Create more files than the cap (use a local cap by creating fileIndexCap+50).
	for i := 0; i < fileIndexCap+50; i++ {
		mustWriteFile(t, filepath.Join(dir, "f"+padNum(i, 4)+".txt"), "x")
	}
	got := buildFileIndex(dir)
	if len(got) > fileIndexCap {
		t.Errorf("index length = %d, want <= cap %d", len(got), fileIndexCap)
	}
	if len(got) != fileIndexCap {
		t.Errorf("index length = %d, want exactly cap %d (walk should stop there)", len(got), fileIndexCap)
	}
}

// TestFileMentionOpensOnAt verifies typing @ opens the popup seeded with files.
func TestFileMentionOpensOnAt(t *testing.T) {
	a := newAppWithFileIndex(t, 80, 24, []string{"main.go", "readme.md", "sub/util.go"})
	injectRune(a, '@')
	if len(a.completions) == 0 {
		t.Fatalf("typing '@' should open the file-mention menu; got empty")
	}
	if a.completionsKind != compFile {
		t.Errorf("completionsKind = %v, want compFile", a.completionsKind)
	}
}

// TestFileMentionDoesNotOpenMidWord verifies "foo@bar" does not trigger.
func TestFileMentionDoesNotOpenMidWord(t *testing.T) {
	a := newAppWithFileIndex(t, 80, 24, []string{"main.go"})
	for _, r := range "foo@bar" {
		injectRune(a, r)
	}
	if a.completionsKind == compFile {
		t.Errorf("'foo@bar' should not trigger file mention; menu opened with %v", a.completions)
	}
}

// TestFileMentionLiveRefine verifies typing after @ narrows the list to matching files.
func TestFileMentionLiveRefine(t *testing.T) {
	a := newAppWithFileIndex(t, 80, 24, []string{"main.go", "main_test.go", "readme.md", "sub/util.go"})
	injectRune(a, '@')
	injectRune(a, 'm')
	injectRune(a, 'a')
	// All shown entries must fuzzy-match "ma": main.go, main_test.go (and not readme.md/util.go).
	for _, c := range a.completions {
		if !strings.Contains(c, "main") {
			t.Errorf("refine 'ma' kept non-matching %q; menu = %v", c, a.completions)
		}
	}
	// At least the two main*.go files should be present.
	got := strings.Join(a.completions, ",")
	if !strings.Contains(got, "main.go") {
		t.Errorf("refine 'ma' should keep main.go; got %s", got)
	}
	if a.compIdx != 0 {
		t.Errorf("compIdx after refine = %d, want 0", a.compIdx)
	}
}

// TestFileMentionCommitInsertsPath verifies committing inserts "@<path> " with a trailing space.
func TestFileMentionCommitInsertsPath(t *testing.T) {
	a := newAppWithFileIndex(t, 80, 24, []string{"main.go", "readme.md"})
	injectRune(a, '@')
	injectRune(a, 'm')
	// Select main.go (should be the top match for "m").
	if len(a.completions) == 0 || a.completions[0] != "main.go" {
		t.Fatalf("precondition: top match = %v, want main.go", a.completions)
	}
	injectKey(a, KeyEnter)
	if a.completionsKind != compNone {
		t.Errorf("commit should close the menu; kind = %v", a.completionsKind)
	}
	if got, want := a.input.Value(), "@main.go "; got != want {
		t.Errorf("input after commit = %q, want %q", got, want)
	}
}

// TestFileMentionCommitPreservesPrefix verifies text before the @ is kept on commit.
func TestFileMentionCommitPreservesPrefix(t *testing.T) {
	a := newAppWithFileIndex(t, 80, 24, []string{"main.go"})
	// "see @m" -> trigger at index 4.
	for _, r := range "see " {
		injectRune(a, r)
	}
	injectRune(a, '@')
	injectRune(a, 'm')
	if a.completionsKind != compFile {
		t.Fatalf("precondition: file menu should be open for 'see @m'; kind=%v", a.completionsKind)
	}
	injectKey(a, KeyEnter)
	if got, want := a.input.Value(), "see @main.go "; got != want {
		t.Errorf("input after commit = %q, want %q (prefix preserved)", got, want)
	}
}

// TestFileMentionClosesOnSpace verifies a space closes the file menu.
func TestFileMentionClosesOnSpace(t *testing.T) {
	a := newAppWithFileIndex(t, 80, 24, []string{"main.go"})
	injectRune(a, '@')
	injectRune(a, 'm')
	if a.completionsKind != compFile {
		t.Fatalf("precondition: menu open")
	}
	injectRune(a, ' ')
	if a.completionsKind != compNone {
		t.Errorf("space should close the file menu; kind = %v", a.completionsKind)
	}
}

// TestFileMentionBackspaceCloses verifies deleting past @ closes the menu.
func TestFileMentionBackspaceCloses(t *testing.T) {
	a := newAppWithFileIndex(t, 80, 24, []string{"main.go"})
	injectRune(a, '@')
	injectRune(a, 'm')
	injectKey(a, KeyBackspace) // -> "@"
	if a.completionsKind != compFile {
		t.Errorf("menu should stay open at bare '@'; kind = %v", a.completionsKind)
	}
	injectKey(a, KeyBackspace) // -> "" (deleted @)
	if a.completionsKind != compNone {
		t.Errorf("deleting '@' should close the menu; kind = %v", a.completionsKind)
	}
	if a.input.Value() != "" {
		t.Errorf("input should be empty; got %q", a.input.Value())
	}
}

// TestFileMentionNavigation verifies Up/Down move the selection in the file menu.
func TestFileMentionNavigation(t *testing.T) {
	a := newAppWithFileIndex(t, 80, 24, []string{"a.go", "b.go", "c.go"})
	injectRune(a, '@') // empty query -> all 3
	n := len(a.completions)
	if n < 2 {
		t.Fatalf("need >=2 completions; got %d", n)
	}
	if a.compIdx != 0 {
		t.Fatalf("initial compIdx = %d, want 0", a.compIdx)
	}
	injectKey(a, KeyDown)
	if a.compIdx != 1 {
		t.Errorf("after Down, compIdx = %d, want 1", a.compIdx)
	}
	injectKey(a, KeyUp)
	if a.compIdx != 0 {
		t.Errorf("after Up, compIdx = %d, want 0", a.compIdx)
	}
}

// TestMatchFilesFuzzyUnit covers the file matcher directly.
func TestMatchFilesFuzzyUnit(t *testing.T) {
	files := []string{"main.go", "main_test.go", "readme.md", "sub/util.go"}
	// Empty query returns up to cap (10 here).
	all := matchFilesFuzzy(files, "")
	if len(all) != 4 {
		t.Errorf("empty query should return all 4; got %d (%v)", len(all), all)
	}
	// "ma" matches main.go / main_test.go (name subsequence), not readme.md / util.go.
	ma := matchFilesFuzzy(files, "ma")
	got := strings.Join(ma, ",")
	if strings.Contains(got, "readme.md") || strings.Contains(got, "util.go") {
		t.Errorf("'ma' should not match readme/util; got %s", got)
	}
	if !strings.Contains(got, "main.go") {
		t.Errorf("'ma' should match main.go; got %s", got)
	}
	// No match.
	none := matchFilesFuzzy(files, "zzzzz")
	if len(none) != 0 {
		t.Errorf("'zzzzz' should match nothing; got %v", none)
	}
}

// mustWriteFile creates a file with content, creating parent dirs. Fatal on error.
func mustWriteFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// padNum zero-pads n to width digits (helper for generating many distinct filenames).
func padNum(n, width int) string {
	return fmt.Sprintf("%0*d", width, n)
}

// TestBuildFileIndexIncludesDirectories verifies subdirectories are indexed with a trailing '/' so
// the @ popup can offer Tab drill-in.
func TestBuildFileIndexIncludesDirectories(t *testing.T) {
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "main.go"), "package main")
	mustWriteFile(t, filepath.Join(dir, "sub", "util.go"), "package sub")
	mustWriteFile(t, filepath.Join(dir, "sub", "deep", "x.go"), "package deep")
	got := buildFileIndex(dir)
	gotStr := strings.Join(got, "\n")
	// Subdirectories appear with a trailing '/'.
	for _, want := range []string{"sub/", "sub/deep/"} {
		if !strings.Contains(gotStr, want) {
			t.Errorf("index should include directory %q; got:\n%s", want, gotStr)
		}
	}
}

// TestParseMentionRange covers the #N / #N-M / #N- suffix parsing.
func TestParseMentionRange(t *testing.T) {
	cases := []struct {
		query      string
		wantBase   string
		wantStart  int
		wantEnd    int
		wantHasRng bool
	}{
		{"main.go", "main.go", 0, 0, false},
		{"main.go#10", "main.go", 10, 0, true}, // single line: end stays 0
		{"main.go#10-20", "main.go", 10, 20, true},
		{"main.go#10-", "main.go", 10, 0, true},     // open-ended: end 0
		{"main.go#abc", "main.go#abc", 0, 0, false}, // non-numeric -> no range
		{"main.go#L10", "main.go#L10", 0, 0, false}, // L prefix rejected
		{"sub/deep.go#5-8", "sub/deep.go", 5, 8, true},
	}
	for _, tc := range cases {
		base, start, end, ok := parseMentionRange(tc.query)
		if base != tc.wantBase || start != tc.wantStart || end != tc.wantEnd || ok != tc.wantHasRng {
			t.Errorf("parseMentionRange(%q) = (%q,%d,%d,%v), want (%q,%d,%d,%v)",
				tc.query, base, start, end, ok, tc.wantBase, tc.wantStart, tc.wantEnd, tc.wantHasRng)
		}
	}
}

// TestFileMentionCommitPreservesRange verifies committing @main.go#10-20 keeps the range suffix.
func TestFileMentionCommitPreservesRange(t *testing.T) {
	a := newAppWithFileIndex(t, 80, 24, []string{"main.go"})
	injectRune(a, '@')
	for _, r := range "main.go#10-20" {
		injectRune(a, r)
	}
	if a.completionsKind != compFile {
		t.Fatalf("precondition: file menu open; kind=%v", a.completionsKind)
	}
	injectKey(a, KeyEnter)
	if got, want := a.input.Value(), "@main.go#10-20 "; got != want {
		t.Errorf("commit should preserve range; got %q, want %q", got, want)
	}
}

// TestFileMentionTabDrillsIntoDirectory verifies Tab on a directory entry rewrites the token to
// @<dir>/ and re-refines (drill-in).
func TestFileMentionTabDrillsIntoDirectory(t *testing.T) {
	// Seed an index where "sub/" is a directory (trailing /) and "sub/util.go" a file under it.
	a := newAppWithFileIndex(t, 80, 24, []string{"main.go", "sub/", "sub/util.go", "sub/deep/"})
	injectRune(a, '@')
	for _, r := range "sub" {
		injectRune(a, r)
	}
	// Select the "sub/" directory entry (it should be among the matches).
	found := false
	for i, c := range a.completions {
		if c == "sub/" {
			a.compIdx = i
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("precondition: 'sub/' not in completions: %+v", a.completions)
	}
	injectKey(a, KeyTab)
	if got := a.input.Value(); got != "@sub/" {
		t.Errorf("Tab on directory should rewrite token to '@sub/'; got %q", got)
	}
	// Drill-in should re-open the menu filtered to sub/'s children.
	if a.completionsKind != compFile {
		t.Errorf("menu should re-open after drill-in; kind=%v", a.completionsKind)
	}
}
