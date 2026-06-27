package builtins

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveToolPath(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)

	cases := []struct {
		name    string
		path    string
		wantErr string
	}{
		{"relative file", "foo.txt", ""},
		{"relative nested", "sub/foo.txt", ""},
		{"cwd", ".", ""},
		{"absolute inside cwd", filepath.Join(dir, "foo.txt"), ""},
		{"parent traversal", "../foo.txt", "outside the working directory"},
		{"nested traversal", "sub/../../foo.txt", "outside the working directory"},
		{"absolute outside cwd", "/etc/passwd", "outside the working directory"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := resolveToolPath(c.path)
			if c.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil (resolved=%q)", c.wantErr, got)
				}
				if !strings.Contains(err.Error(), c.wantErr) {
					t.Fatalf("error %q does not contain %q", err.Error(), c.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !filepath.IsAbs(got) {
				t.Errorf("resolved path %q is not absolute", got)
			}
		})
	}
}

func TestResolveToolPathEmpty(t *testing.T) {
	_, err := resolveToolPath("")
	if err == nil {
		t.Fatal("expected error for empty path")
	}
}

func TestReadToolRejectsPathOutsideCwd(t *testing.T) {
	outside := t.TempDir()
	path := filepath.Join(outside, "secret.txt")
	os.WriteFile(path, []byte("secret"), 0o644)

	rt := NewReadTool()
	res, _ := rt.Exec(nil, mustJSON(t, `{"path":"`+path+`"}`))
	if !res.IsError {
		t.Fatal("expected IsError for path outside cwd")
	}
	if !strings.Contains(res.Content, "outside the working directory") {
		t.Errorf("error should mention working directory, got %q", res.Content)
	}
}
