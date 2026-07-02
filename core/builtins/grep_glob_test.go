package builtins

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGrepCaseInsensitiveAndLiteral(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	if err := os.WriteFile("a.txt", []byte("Hello World\na.b.c\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	g := NewGrepTool()

	// Case-sensitive default misses "hello".
	res, _ := g.Exec(context.Background(), mustJSON(t, `{"pattern":"hello"}`))
	if !strings.Contains(res.Content, "no matches") {
		t.Errorf("case-sensitive should miss, got %q", res.Content)
	}
	// case_insensitive matches.
	res, _ = g.Exec(context.Background(), mustJSON(t, `{"pattern":"hello","case_insensitive":true}`))
	if !strings.Contains(res.Content, "Hello World") {
		t.Errorf("case_insensitive should match, got %q", res.Content)
	}
	// literal treats "a.b.c" dots as literal (a regex "a.b.c" would also match, so test a non-match case).
	res, _ = g.Exec(context.Background(), mustJSON(t, `{"pattern":"a.b.c","literal":true}`))
	if !strings.Contains(res.Content, "a.b.c") {
		t.Errorf("literal should match the literal string, got %q", res.Content)
	}
	res, _ = g.Exec(context.Background(), mustJSON(t, `{"pattern":"axbxc","literal":true}`))
	if !strings.Contains(res.Content, "no matches") {
		t.Errorf("literal axbxc should not match a.b.c, got %q", res.Content)
	}
}

func TestGrepContextCanceled(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	if err := os.WriteFile("a.txt", []byte("match\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	g := NewGrepTool()
	_, err := g.Exec(ctx, mustJSON(t, `{"pattern":"match"}`))
	if err == nil {
		t.Error("canceled context should return an error")
	}
}

func TestGlobPathParam(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	if err := os.MkdirAll(filepath.Join("sub", "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join("sub", "nested", "x.go"), []byte("package x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("root.go", []byte("package main"), 0o644); err != nil {
		t.Fatal(err)
	}
	g := NewGlobTool()

	// Search scoped to sub/: pattern matches relative to that root, so root.go is not found.
	res, err := g.Exec(context.Background(), mustJSON(t, `{"pattern":"**/*.go","path":"sub"}`))
	if err != nil || res.IsError {
		t.Fatalf("glob failed: %+v %v", res, err)
	}
	if !strings.Contains(res.Content, "nested/x.go") {
		t.Errorf("expected nested/x.go relative to sub, got %q", res.Content)
	}
	if strings.Contains(res.Content, "root.go") {
		t.Errorf("root.go should be outside the search path, got %q", res.Content)
	}
}
