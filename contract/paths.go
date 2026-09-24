package contract

import (
	"fmt"
	"os"
	"path/filepath"
)

// On-disk locations the TUI touches directly. These stay here as leaf helpers (stdlib only) so the
// TUI, crash diagnostics and the future core agree on one layout instead of re-spelling paths.
//
// Layout: the data dir is ~/.creator and holds runtime data (sessions, logs, memory, history,
// skills). Functions here never create the directory — callers decide.

// AppDir is the directory name under the user's home holding all runtime data.
const AppDir = ".creator"

// File base names (kept here so callers never spell them inline).
const (
	HistoryFileName   = "history.json"
	LastReplyFileName = "last-reply.md"
)

// DataDir returns the runtime data directory (~/.creator). It does not create the directory.
func DataDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("get home dir: %w", err)
	}
	return filepath.Join(home, AppDir), nil
}

// inData joins name under the data dir.
func inData(name string) (string, error) {
	dir, err := DataDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, name), nil
}

// LogDir returns the directory for logs and crash reports (data dir root).
func LogDir() (string, error) { return DataDir() }

// HistoryFile returns the TUI input history file path (data dir).
func HistoryFile() (string, error) { return inData(HistoryFileName) }

// LastReplyFile returns the path used by /copy to save the last assistant reply (data dir).
func LastReplyFile() (string, error) { return inData(LastReplyFileName) }
