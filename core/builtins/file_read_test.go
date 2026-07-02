package builtins

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadToolOffsetLimit(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	var sb strings.Builder
	for i := 1; i <= 10; i++ {
		fmt.Fprintf(&sb, "line%d\n", i)
	}
	if err := os.WriteFile("f.txt", []byte(sb.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	rt := NewReadTool()

	// offset=3, limit=2 => lines 3 and 4, plus a truncation hint (more remain).
	res, err := rt.Exec(context.Background(), mustJSON(t, `{"path":"f.txt","offset":3,"limit":2}`))
	if err != nil || res.IsError {
		t.Fatalf("read failed: %+v %v", res, err)
	}
	if !strings.Contains(res.Content, "3\tline3\n") || !strings.Contains(res.Content, "4\tline4\n") {
		t.Errorf("expected lines 3-4, got %q", res.Content)
	}
	if strings.Contains(res.Content, "line5") {
		t.Errorf("limit not honored, got %q", res.Content)
	}
	if !strings.Contains(res.Content, "offset=5") {
		t.Errorf("expected continuation hint, got %q", res.Content)
	}
}

func TestReadToolOffsetPastEnd(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	if err := os.WriteFile("f.txt", []byte("a\nb\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	rt := NewReadTool()
	res, _ := rt.Exec(context.Background(), mustJSON(t, `{"path":"f.txt","offset":99}`))
	if !res.IsError {
		t.Errorf("offset past end should be an error, got %q", res.Content)
	}
}

func TestReadToolBinaryRejected(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	if err := os.WriteFile("bin", []byte{0x00, 0x01, 0x02, 'a', 'b'}, 0o644); err != nil {
		t.Fatal(err)
	}
	rt := NewReadTool()
	res, _ := rt.Exec(context.Background(), mustJSON(t, `{"path":"bin"}`))
	if !res.IsError || !strings.Contains(res.Content, "binary") {
		t.Errorf("binary file should be rejected, got %+v", res)
	}
}

func TestEditReplaceAll(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	path := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(path, []byte("foo foo foo"), 0o644); err != nil {
		t.Fatal(err)
	}
	et := NewEditTool()

	// Without replace_all a multi-match edit is refused.
	res, _ := et.Exec(context.Background(), mustJSON(t, `{"path":"`+path+`","old_string":"foo","new_string":"bar"}`))
	if !res.IsError {
		t.Fatalf("multi-match without replace_all should error, got %q", res.Content)
	}

	// With replace_all every occurrence is replaced.
	res, err := et.Exec(context.Background(), mustJSON(t, `{"path":"`+path+`","old_string":"foo","new_string":"bar","replace_all":true}`))
	if err != nil || res.IsError {
		t.Fatalf("replace_all failed: %+v %v", res, err)
	}
	data, _ := os.ReadFile(path)
	if string(data) != "bar bar bar" {
		t.Errorf("content = %q, want %q", string(data), "bar bar bar")
	}
	if !strings.Contains(res.Content, "3 replacements") {
		t.Errorf("expected replacement count, got %q", res.Content)
	}
}
