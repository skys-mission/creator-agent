//go:build !windows

package builtins

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAtomicWriteFileRejectsSymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target.txt")
	link := filepath.Join(dir, "link.txt")
	os.WriteFile(target, []byte("original"), 0o644)
	if err := os.Symlink(target, link); err != nil {
		t.Skip("symlink not supported:", err)
	}

	err := atomicWriteFile(link, []byte("new"), 0o644)
	if err == nil {
		t.Fatal("expected error writing through symlink")
	}
	// Target should remain unchanged.
	data, _ := os.ReadFile(target)
	if string(data) != "original" {
		t.Errorf("target was modified: %q", data)
	}
}
