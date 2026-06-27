package builtins

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// --- read: oversized file should IsError (do not read into memory) ---

func TestReadToolRejectsOversizedFile(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	path := "big.txt"
	// Write a file just over maxReadBytes (use truncate to avoid real memory usage).
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(int64(maxReadBytes) + 1); err != nil {
		t.Fatal(err)
	}
	f.Close()

	rt := NewReadTool()
	out, err := rt.Exec(context.Background(), rawJSON(t, map[string]string{"path": path}))
	if err != nil {
		t.Fatalf("Exec error: %v", err)
	}
	if !out.IsError {
		t.Error("oversized file should yield IsError")
	}
	if !strings.Contains(out.Content, "too large") {
		t.Errorf("unexpected error content: %q", out.Content)
	}
}

// --- read: normal-sized file should still be readable ---

func TestReadToolNormalFile(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	path := "small.txt"
	if err := os.WriteFile(path, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	rt := NewReadTool()
	out, err := rt.Exec(context.Background(), rawJSON(t, map[string]string{"path": path}))
	if err != nil {
		t.Fatalf("Exec error: %v", err)
	}
	if out.IsError {
		t.Errorf("small file should read ok: %q", out.Content)
	}
	if out.Content != "hello" {
		t.Errorf("content mismatch: %q", out.Content)
	}
}

// rawJSON builds tool input JSON (test helper).
func rawJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
