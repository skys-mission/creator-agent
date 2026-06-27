// Package paths is the single source of truth for creator-agent's on-disk locations.
//
// It is a dependency-free (stdlib-only) leaf package so every layer (config, core, cmd) can import
// it without creating cycles, keeping the application's directory layout defined in exactly one place.
//
// Layout:
//
//   - Config dir: $XDG_CONFIG_HOME/creator when XDG_CONFIG_HOME is set, otherwise ~/.creator.
//     Holds user-edited config files (config.toml, mcp.json).
//   - Data dir: ~/.creator (always). Holds runtime data: sessions, logs, memory, history, skills,
//     and the global AGENTS.md.
//
// When XDG_CONFIG_HOME is unset both resolve to ~/.creator, so the default single-directory layout
// is preserved. Project-level files live under ./<ProjectDir>/ (see ProjectDir).
package paths

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	// AppDir is the directory name under the user's home holding all runtime data.
	AppDir = ".creator"
	// XDGApp is the directory name under $XDG_CONFIG_HOME for config files.
	XDGApp = "creator"
	// ProjectDir is the per-project subdirectory (relative to the project/CWD) holding project-level
	// config.toml, mcp.json, skills/, and AGENTS.md.
	ProjectDir = ".creator"
)

// File / subdirectory base names (kept here so callers never spell them inline).
const (
	ConfigFileName    = "config.toml"
	MCPFileName       = "mcp.json"
	AgentsMdFileName  = "AGENTS.md"
	HistoryFileName   = "history.json"
	LastReplyFileName = "last-reply.md"
	SessionsDirName   = "sessions"
	SkillsDirName     = "skills"
	MemoryDirName     = "memory"
)

// DataDir returns the runtime data directory (~/.creator). It does not create the directory.
func DataDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("get home dir: %w", err)
	}
	return filepath.Join(home, AppDir), nil
}

// ConfigDir returns the config directory: $XDG_CONFIG_HOME/creator when XDG_CONFIG_HOME is set,
// otherwise the data dir (~/.creator). It does not create the directory.
func ConfigDir() (string, error) {
	if xdg := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME")); xdg != "" {
		return filepath.Join(xdg, XDGApp), nil
	}
	return DataDir()
}

// inConfig joins name under the config dir.
func inConfig(name string) (string, error) {
	dir, err := ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, name), nil
}

// inData joins name under the data dir.
func inData(name string) (string, error) {
	dir, err := DataDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, name), nil
}

// ConfigFile returns the global config.toml path (config dir).
func ConfigFile() (string, error) { return inConfig(ConfigFileName) }

// MCPFile returns the global mcp.json path (config dir).
func MCPFile() (string, error) { return inConfig(MCPFileName) }

// SessionsDir returns the session store directory (data dir).
func SessionsDir() (string, error) { return inData(SessionsDirName) }

// SkillsDir returns the global skills directory (data dir).
func SkillsDir() (string, error) { return inData(SkillsDirName) }

// MemoryDir returns the auto-memory directory (data dir).
func MemoryDir() (string, error) { return inData(MemoryDirName) }

// LogDir returns the directory for logs and crash reports (data dir root).
func LogDir() (string, error) { return DataDir() }

// HistoryFile returns the REPL/TUI input history file path (data dir).
func HistoryFile() (string, error) { return inData(HistoryFileName) }

// LastReplyFile returns the path used by /copy to save the last assistant reply (data dir).
func LastReplyFile() (string, error) { return inData(LastReplyFileName) }

// GlobalAgentsMd returns the global AGENTS.md path (data dir).
func GlobalAgentsMd() (string, error) { return inData(AgentsMdFileName) }

// ProjectConfigFile is the project-level config path relative to the current working directory.
const ProjectConfigFile = ProjectDir + "/" + ConfigFileName

// ProjectMCPFile is the project-level mcp.json path relative to the current working directory.
const ProjectMCPFile = ProjectDir + "/" + MCPFileName
