package tui

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/skys-mission/creator-agent/contract"
)

func TestModelStoreRoundtrip(t *testing.T) {
	s := modelStore{path: filepath.Join(t.TempDir(), "models.json")}
	if got := s.load(); got != nil {
		t.Fatalf("load() on missing file = %v, want nil", got)
	}

	m1 := contract.Model{Name: "a", Protocol: contract.ProtocolOpenAIChat, ModelID: "m1", APIKey: "sk-1"}
	m2 := contract.Model{Name: "b", Protocol: contract.ProtocolOpenAIChat, ModelID: "m2"}
	if n, err := s.add(m1); err != nil || n != 1 {
		t.Fatalf("add(m1) = (%d, %v), want (1, nil)", n, err)
	}
	if n, err := s.add(m2); err != nil || n != 2 {
		t.Fatalf("add(m2) = (%d, %v), want (2, nil)", n, err)
	}

	got := s.load()
	want := []contract.Model{m1, m2}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("load() = %+v, want %+v", got, want)
	}

	// The file carries API keys: it must be created 0600 (owner-only).
	info, err := os.Stat(s.path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("store file mode = %o, want 600", perm)
	}
}

func TestModelStoreCorruptFileIsIgnored(t *testing.T) {
	p := filepath.Join(t.TempDir(), "models.json")
	if err := os.WriteFile(p, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	s := modelStore{path: p}
	if got := s.load(); got != nil {
		t.Fatalf("load() on corrupt file = %v, want nil", got)
	}
}

func TestModelStoreWriteError(t *testing.T) {
	// A regular file where the parent directory should be: MkdirAll must fail cleanly.
	blocker := filepath.Join(t.TempDir(), "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	s := modelStore{path: filepath.Join(blocker, "models.json")}
	if _, err := s.add(contract.Model{Protocol: contract.ProtocolOpenAIChat, ModelID: "m1"}); err == nil {
		t.Fatal("add() into a blocked path = nil, want error")
	}
}
