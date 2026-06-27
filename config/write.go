package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// NeedsSetup reports whether first-run setup is required: no config file was loaded and no API key is
// available from the environment or the -api-key flag. In that situation the agent has no usable
// credentials, so the caller either runs the setup wizard (TTY) or prints the text guide (non-TTY).
func NeedsSetup(loadedPath, flagAPIKey string) bool {
	return loadedPath == "" &&
		strings.TrimSpace(os.Getenv("OPENAI_API_KEY")) == "" &&
		strings.TrimSpace(flagAPIKey) == ""
}

// WriteInitialConfig atomically writes a fresh config.toml to the global config path containing a
// single profile (named profileName) marked as the default. It fails if the file already exists, so
// an interrupted/rerun setup never clobbers a config written in the meantime. Returns the written
// path.
func WriteInitialConfig(p Profile, profileName string) (string, error) {
	gp, err := GlobalConfigPath()
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(gp); err == nil {
		return "", fmt.Errorf("config already exists: %s", gp)
	} else if !os.IsNotExist(err) {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(gp), 0o755); err != nil {
		return "", err
	}
	content := renderInitialConfig(p, profileName)
	if err := atomicWrite(gp, []byte(content), 0o600); err != nil {
		return "", err
	}
	return gp, nil
}

// renderInitialConfig builds the first-run config.toml text. Values are emitted as TOML basic strings
// (quoted/escaped) so keys/secrets with special characters stay valid.
func renderInitialConfig(p Profile, profileName string) string {
	var sb strings.Builder
	sb.WriteString("# creator-agent config (TOML). See https://github.com/skys-mission/creator-agent\n")
	sb.WriteString("# Docs: docs/config.md\n\n")
	sb.WriteString("version = 1\n\n")
	sb.WriteString("# The profile used when no -profile flag is given.\n")
	fmt.Fprintf(&sb, "default = %s\n\n", tomlString(profileName))
	fmt.Fprintf(&sb, "[profiles.%s]\n", tomlKey(profileName))
	fmt.Fprintf(&sb, "type = %s\n", tomlString(p.NormalizedType()))
	fmt.Fprintf(&sb, "base_url = %s\n", tomlString(p.BaseURL))
	fmt.Fprintf(&sb, "api_key = %s\n", tomlString(p.APIKey))
	fmt.Fprintf(&sb, "model = %s\n", tomlString(p.Model))
	return sb.String()
}

// atomicWrite writes data to a temp file in the same directory and renames it into place, so a reader
// never observes a half-written config (rename is atomic on POSIX filesystems).
func atomicWrite(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".config-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op after a successful rename
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(perm); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

// tomlString renders a Go string as a TOML basic string (double-quoted, escaped). strconv.Quote
// produces escaping compatible with the TOML basic-string grammar for the inputs we emit.
func tomlString(s string) string { return strconv.Quote(s) }

// tomlKey renders a profile name as a TOML key: bare when it is a simple identifier, quoted otherwise.
func tomlKey(s string) string {
	if isBareKey(s) {
		return s
	}
	return strconv.Quote(s)
}

// isBareKey reports whether s is a valid TOML bare key (ASCII letters, digits, '-', '_').
func isBareKey(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
		default:
			return false
		}
	}
	return true
}
