package tui

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/skys-mission/creator-agent/contract"
)

// modelStore persists the model objects created in the TUI (~/.creator/models.json, written 0600).
//
// The file contains resolved API keys. That is sanctioned: secrets live only in environment
// variables and non-tracked local files, and this is such a local file — it must never be
// committed, copied into docs, or logged (Model.String masks the key; nothing prints the file).
//
// The path is injectable for tests; the zero value resolves contract.ModelsFile(). The collection
// is small and always rewritten wholesale.
type modelStore struct {
	path string // empty = contract.ModelsFile()
}

func (s modelStore) resolve() (string, error) {
	if s.path != "" {
		return s.path, nil
	}
	return contract.ModelsFile()
}

// load returns the saved models, or nil when the file is missing or unreadable/corrupt (the
// collection is advisory state — a bad file must never block startup).
func (s modelStore) load() []contract.Model {
	p, err := s.resolve()
	if err != nil {
		return nil
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return nil
	}
	var ms []contract.Model
	if json.Unmarshal(data, &ms) != nil {
		return nil
	}
	return ms
}

// save writes the whole collection (0600 — the file carries API keys).
func (s modelStore) save(models []contract.Model) error {
	p, err := s.resolve()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return fmt.Errorf("mkdir: %w", err)
	}
	data, err := json.MarshalIndent(models, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	if err := os.WriteFile(p, data, 0o600); err != nil {
		return fmt.Errorf("write: %w", err)
	}
	return nil
}

// add appends one model to the collection and returns the new size.
func (s modelStore) add(m contract.Model) (int, error) {
	models := append(s.load(), m)
	if err := s.save(models); err != nil {
		return 0, err
	}
	return len(models), nil
}

// removeAt deletes the i-th model and returns how many are left. The model's API key exists only
// in this file and dies with the entry, so callers must confirm with the user first.
func (s modelStore) removeAt(i int) (int, error) {
	models := s.load()
	if i < 0 || i >= len(models) {
		return len(models), fmt.Errorf("no model at index %d", i)
	}
	models = append(models[:i], models[i+1:]...)
	if err := s.save(models); err != nil {
		return len(models), err
	}
	return len(models), nil
}
