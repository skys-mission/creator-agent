package builtins

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// chdir changes the working directory to dir for the duration of the test.
func chdir(t *testing.T, dir string) {
	t.Helper()
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(old) })
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
}

// mustJSON wraps a string into json.RawMessage (tool Exec input).
func mustJSON(t *testing.T, s string) json.RawMessage {
	t.Helper()
	if !json.Valid([]byte(s)) {
		t.Fatalf("invalid json: %s", s)
	}
	return json.RawMessage(s)
}

func TestReadTool(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(path, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	chdir(t, dir)
	r := NewReadTool()

	// Normal read (relative path inside working directory)
	res, err := r.Exec(context.Background(), mustJSON(t, `{"path":"f.txt"}`))
	if err != nil || res.IsError {
		t.Fatalf("read failed: %+v %v", res, err)
	}
	if res.Content != "hello" {
		t.Errorf("content = %q, want hello", res.Content)
	}

	// Missing file -> IsError (fail-closed, no panic)
	res, _ = r.Exec(context.Background(), mustJSON(t, `{"path":"f.txt/nope"}`))
	if !res.IsError {
		t.Error("missing file should return IsError")
	}
}

func TestWriteTool(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	w := NewWriteTool()

	res, err := w.Exec(context.Background(), mustJSON(t, `{"path":"out.txt","content":"data"}`))
	if err != nil || res.IsError {
		t.Fatalf("write failed: %+v", res)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "out.txt"))
	if string(data) != "data" {
		t.Errorf("file content = %q, want data", data)
	}
}

func TestEditTool(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	path := "e.txt"
	os.WriteFile(path, []byte("foo bar baz"), 0o644)
	e := NewEditTool()

	// Normal replacement
	res, _ := e.Exec(context.Background(), mustJSON(t, `{"path":"e.txt","old_string":"bar","new_string":"QUX"}`))
	if res.IsError {
		t.Fatalf("edit failed: %+v", res)
	}
	data, _ := os.ReadFile(filepath.Join(dir, path))
	if string(data) != "foo QUX baz" {
		t.Errorf("after edit = %q", data)
	}

	// old_string not found -> IsError
	res, _ = e.Exec(context.Background(), mustJSON(t, `{"path":"e.txt","old_string":"zzz","new_string":"x"}`))
	if !res.IsError {
		t.Error("missing old_string should be IsError")
	}

	// Multiple matches -> IsError (requires uniqueness)
	os.WriteFile(filepath.Join(dir, path), []byte("a a a"), 0o644)
	res, _ = e.Exec(context.Background(), mustJSON(t, `{"path":"e.txt","old_string":"a","new_string":"b"}`))
	if !res.IsError {
		t.Error("multiple matches should be IsError (require unique)")
	}

	// Empty old_string -> explicit IsError (instead of the confusing "matches N times" message)
	os.WriteFile(filepath.Join(dir, path), []byte("content"), 0o644)
	res, _ = e.Exec(context.Background(), mustJSON(t, `{"path":"e.txt","old_string":"","new_string":"x"}`))
	if !res.IsError {
		t.Error("empty old_string should be IsError")
	}
	if !strings.Contains(res.Content, "empty") {
		t.Errorf("empty old_string error should mention empty, got %q", res.Content)
	}
}

// TestReadToolRejectsDirectory: models often mistake a directory for a file; return an actionable hint instead of raw "is a directory".
func TestReadToolRejectsDirectory(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	r := NewReadTool()
	res, _ := r.Exec(context.Background(), mustJSON(t, `{"path":"."}`))
	if !res.IsError {
		t.Error("reading a directory should be IsError")
	}
	if !strings.Contains(res.Content, "directory") {
		t.Errorf("directory error should guide model, got %q", res.Content)
	}
}

func TestGrepTool(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.go"), []byte("package x\nfunc Foo() {}\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "b.txt"), []byte("no match here\n"), 0o644)
	chdir(t, dir)
	g := NewGrepTool()

	res, _ := g.Exec(context.Background(), mustJSON(t, `{"pattern":"func Foo","path":"."}`))
	if res.IsError {
		t.Fatalf("grep failed: %+v", res)
	}
	if !strings.Contains(res.Content, "a.go") {
		t.Errorf("should match a.go: %s", res.Content)
	}
	if strings.Contains(res.Content, "b.txt") {
		t.Errorf("should not match b.txt: %s", res.Content)
	}
}

func TestGlobTool(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.go"), []byte("x"), 0o644)
	os.WriteFile(filepath.Join(dir, "b.txt"), []byte("x"), 0o644)
	os.MkdirAll(filepath.Join(dir, "sub"), 0o755)
	os.WriteFile(filepath.Join(dir, "sub", "c.go"), []byte("x"), 0o644)

	// glob uses relative paths (cwd), so switch to tempdir
	old, _ := os.Getwd()
	defer os.Chdir(old)
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}

	gl := NewGlobTool()
	res, _ := gl.Exec(context.Background(), mustJSON(t, `{"pattern":"**/*.go"}`))
	if res.IsError {
		t.Fatalf("glob failed: %+v", res)
	}
	if !strings.Contains(res.Content, "a.go") {
		t.Errorf("should match a.go: %s", res.Content)
	}
	if !strings.Contains(res.Content, "sub/c.go") {
		t.Errorf("should match sub/c.go (**): %s", res.Content)
	}
	if strings.Contains(res.Content, "b.txt") {
		t.Errorf("should not match b.txt: %s", res.Content)
	}
}

// TestCollapseDoubleStar: consecutive ** segments are merged to prevent exponential backtracking in glob.
func TestCollapseDoubleStar(t *testing.T) {
	cases := []struct {
		in, name string
		want     []string
	}{
		{"**/**/**", "collapse3", []string{"**"}},
		{"**/foo/**", "keep middle", []string{"**", "foo", "**"}},
		{"**/**/foo/**/**/bar", "interspersed", []string{"**", "foo", "**", "bar"}},
		{"a/b/c", "no stars", []string{"a", "b", "c"}},
		{"**", "single", []string{"**"}},
	}
	for _, c := range cases {
		got := collapseDoubleStar(strings.Split(c.in, "/"))
		if fmt.Sprint(got) != fmt.Sprint(c.want) {
			t.Errorf("%s: collapse(%q) = %v, want %v", c.name, c.in, got, c.want)
		}
	}
}

// TestGlobDoubleStarNoBlowup: pathological multi-** patterns should not be exponentially slow
// (old implementation: 8x** vs 20 segments = 105ms/path).
// Uses a timer assertion: after collapse, a deep non-matching path should fail in milliseconds.
func TestGlobDoubleStarNoBlowup(t *testing.T) {
	pat := "**/**/**/**/**/**/*.go" // 6x**, old implementation would backtrack exponentially
	collapsed := collapseDoubleStar(strings.Split(pat, "/"))
	if len(collapsed) != 2 { // collapsed to [**, *.go]
		t.Fatalf("collapse produced %d segs, want 2: %v", len(collapsed), collapsed)
	}
	// Build a deep non-matching path (.txt does not match *.go)
	name := []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j", "k", "l", "m", "n", "o", "p", "q", "r", "s", "t.txt"}
	start := time.Now()
	if matchSegments(collapsed, name) {
		t.Error("should not match .txt against *.go")
	}
	if elapsed := time.Since(start); elapsed > 50*time.Millisecond {
		t.Errorf("collapsed glob took %v, want <50ms (no exponential blowup)", elapsed)
	}
}

// TestWriteToolRejectsDirectory: writing to a directory path should return a clear hint
// instead of the obscure Rename error from atomicWrite.
func TestWriteToolRejectsDirectory(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	w := NewWriteTool()
	res, _ := w.Exec(context.Background(), mustJSON(t, `{"path":".","content":"x"}`))
	if !res.IsError {
		t.Error("writing to a directory should be IsError")
	}
	if !strings.Contains(res.Content, "directory") {
		t.Errorf("directory write error should guide model, got %q", res.Content)
	}
}

// Verify tool capability declarations (fail-closed).
func TestToolCapabilities(t *testing.T) {
	if NewReadTool().Info().ReadOnly != true {
		t.Error("read should be ReadOnly")
	}
	if NewReadTool().Info().ConcurrencySafe != true {
		t.Error("read should be ConcurrencySafe")
	}
	if NewBashTool().Info().ReadOnly != false {
		t.Error("bash should NOT be ReadOnly (write op)")
	}
	if NewBashTool().Info().ConcurrencySafe != false {
		t.Error("bash should NOT be ConcurrencySafe")
	}
	if NewWriteTool().Info().ReadOnly != false {
		t.Error("write should NOT be ReadOnly")
	}
}
