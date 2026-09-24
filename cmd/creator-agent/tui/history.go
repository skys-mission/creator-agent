package tui

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/skys-mission/creator-agent/contract"
)

func historyPath() (string, bool) {
	p, err := contract.HistoryFile()
	if err != nil {
		return "", false
	}
	return p, true
}

// loadHistory loads input history from disk (nil on missing/corrupt).
func loadHistory() []string {
	p, ok := historyPath()
	if !ok {
		return nil
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return nil
	}
	var h []string
	if json.Unmarshal(data, &h) != nil {
		return nil
	}
	return h
}

// saveHistory persists input history (best-effort; failures are logged for diagnosis).
func saveHistory(h []string) {
	p, ok := historyPath()
	if !ok {
		return
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		contract.Warnf("save TUI history: mkdir: %v", err)
		return
	}
	data, err := json.Marshal(h)
	if err != nil {
		contract.Warnf("save TUI history: marshal: %v", err)
		return
	}
	if err := os.WriteFile(p, data, 0o600); err != nil {
		contract.Warnf("save TUI history: write: %v", err)
	}
}

func appendHistory(h []string, s string) []string {
	if s == "" {
		return h
	}
	if len(h) > 0 && h[len(h)-1] == s {
		return h
	}
	h = append(h, s)
	if len(h) > 1000 {
		h = h[len(h)-1000:]
	}
	return h
}
