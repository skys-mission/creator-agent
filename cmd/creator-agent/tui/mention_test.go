package tui

// mention_test.go covers submit-time @path / @path#range content injection: full-file, range slice,
// missing/oversized skip, and the <path>/<content> envelope format.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeMentionFile is a small test helper that writes content to path (creating parents).
func writeMentionFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestResolveMentionsFullFile verifies a plain @path injects the whole file with line numbers.
func TestResolveMentionsFullFile(t *testing.T) {
	dir := t.TempDir()
	cwd, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(cwd)

	writeMentionFile(t, "demo.txt", "alpha\nbeta\ngamma\n")
	got := resolveMentions("see @demo.txt for details")
	if !strings.Contains(got, "Referenced files:") {
		t.Errorf("expected a referenced-files appendix; got:\n%s", got)
	}
	if !strings.Contains(got, "<path>demo.txt</path>") {
		t.Errorf("expected <path>demo.txt</path> block; got:\n%s", got)
	}
	for _, want := range []string{"1: alpha", "2: beta", "3: gamma"} {
		if !strings.Contains(got, want) {
			t.Errorf("expected %q in injected content; got:\n%s", want, got)
		}
	}
	if !strings.HasPrefix(got, "see @demo.txt for details") {
		t.Errorf("original text should be preserved at the start; got:\n%s", got)
	}
}

// TestResolveMentionsRange verifies @path#start-end injects only that inclusive line range.
func TestResolveMentionsRange(t *testing.T) {
	dir := t.TempDir()
	cwd, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(cwd)

	writeMentionFile(t, "f.txt", "line1\nline2\nline3\nline4\nline5\n")
	got := resolveMentions("@f.txt#2-4")
	if strings.Contains(got, "1: line1") {
		t.Errorf("line1 should be excluded by #2-4; got:\n%s", got)
	}
	if strings.Contains(got, "5: line5") {
		t.Errorf("line5 should be excluded by #2-4; got:\n%s", got)
	}
	for _, want := range []string{"2: line2", "3: line3", "4: line4"} {
		if !strings.Contains(got, want) {
			t.Errorf("expected %q in range injection; got:\n%s", want, got)
		}
	}
}

// TestResolveMentionsSingleLine verifies @path#N injects just that one line.
func TestResolveMentionsSingleLine(t *testing.T) {
	dir := t.TempDir()
	cwd, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(cwd)

	writeMentionFile(t, "f.txt", "line1\nline2\nline3\n")
	got := resolveMentions("@f.txt#2")
	if !strings.Contains(got, "2: line2") {
		t.Errorf("expected line 2; got:\n%s", got)
	}
	if strings.Contains(got, "1: line1") || strings.Contains(got, "3: line3") {
		t.Errorf("single-line #2 should exclude lines 1 and 3; got:\n%s", got)
	}
}

// TestResolveMentionsMissingFileSkipped verifies a missing @path is silently skipped (no fatal).
func TestResolveMentionsMissingFileSkipped(t *testing.T) {
	dir := t.TempDir()
	cwd, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(cwd)

	got := resolveMentions("see @does-not-exist.go")
	if strings.Contains(got, "Referenced files:") {
		t.Errorf("missing file should not inject an appendix; got:\n%s", got)
	}
	if got != "see @does-not-exist.go" {
		t.Errorf("text should be unchanged when nothing resolves; got %q", got)
	}
}

// TestResolveMentionsOversizedSkipped verifies a file beyond the cap is skipped.
func TestResolveMentionsOversizedSkipped(t *testing.T) {
	dir := t.TempDir()
	cwd, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(cwd)

	big := strings.Repeat("x", mentionFileCap+1)
	writeMentionFile(t, "big.txt", big)
	got := resolveMentions("@big.txt")
	if strings.Contains(got, "Referenced files:") {
		t.Errorf("oversized file should be skipped; got:\n%s", got)
	}
}

// TestResolveMentionsDirectorySkipped verifies a directory reference (@dir/) injects nothing.
func TestResolveMentionsDirectorySkipped(t *testing.T) {
	dir := t.TempDir()
	cwd, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(cwd)

	if err := os.MkdirAll("sub", 0o755); err != nil {
		t.Fatal(err)
	}
	got := resolveMentions("@sub/")
	if strings.Contains(got, "Referenced files:") {
		t.Errorf("directory reference should inject nothing; got:\n%s", got)
	}
}

// TestResolveMentionsMultipleReferences verifies several @path in one message each resolve.
func TestResolveMentionsMultipleReferences(t *testing.T) {
	dir := t.TempDir()
	cwd, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(cwd)

	writeMentionFile(t, "a.txt", "A1\n")
	writeMentionFile(t, "b.txt", "B1\n")
	got := resolveMentions("@a.txt and @b.txt")
	if !strings.Contains(got, "1: A1") || !strings.Contains(got, "1: B1") {
		t.Errorf("both files should be injected; got:\n%s", got)
	}
}
