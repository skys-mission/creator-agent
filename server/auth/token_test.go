package auth

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEnsureCreatesTokenFile0600(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "rpc-token")
	s := New(path)
	if err := s.Ensure(); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat token file: %v", err)
	}
	if perm := fi.Mode().Perm(); perm != 0o600 {
		t.Fatalf("token file perm = %o, want 600", perm)
	}
	if got := len(s.Value()); got != 64 {
		t.Fatalf("token length = %d hex chars, want 64", got)
	}
}

func TestEnsureReloadsExistingToken(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rpc-token")
	a := New(path)
	if err := a.Ensure(); err != nil {
		t.Fatalf("Ensure #1: %v", err)
	}
	b := New(path)
	if err := b.Ensure(); err != nil {
		t.Fatalf("Ensure #2: %v", err)
	}
	if a.Value() != b.Value() {
		t.Fatalf("reloaded token differs: %q vs %q", a.Value(), b.Value())
	}
}

func TestEnsureRejectsEmptyTokenFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rpc-token")
	if err := os.WriteFile(path, []byte("\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	s := New(path)
	if err := s.Ensure(); err == nil {
		t.Fatal("empty token file: want error, got nil")
	}
}

func TestCheck(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rpc-token")
	s := New(path)
	if s.Check("anything") {
		t.Fatal("unensured store must accept nothing")
	}
	if err := s.Ensure(); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	if !s.Check(s.Value()) {
		t.Fatal("Check(valid) = false, want true")
	}
	if s.Check("") {
		t.Fatal("Check(empty) = true, want false")
	}
	if s.Check(s.Value() + "x") {
		t.Fatal("Check(near-miss) = true, want false")
	}
}
