package config

import (
	"os"
	"path/filepath"
)

// configTemplate is the TOML written on first run. It is deliberately minimal (just enough to get
// running): one default profile plus a commented alternative. Every other section has sensible
// built-in defaults and is documented in docs/config.md.
const configTemplate = `# creator-agent config (TOML). See https://github.com/skys-mission/creator-agent
# Docs: docs/config.md

version = 1

# The profile used when no -profile flag is given.
default = "deepseek"

# Model profiles. type: openai (default, OpenAI-compatible) | anthropic (not yet implemented in v0.1).
[profiles.deepseek]
type = "openai"
base_url = "https://api.deepseek.com"
api_key = "sk-xxx"
model = "deepseek-chat"

[profiles.openai]
type = "openai"
base_url = "https://api.openai.com/v1"
api_key = "sk-xxx"
model = "gpt-4o-mini"
`

// EnsureConfigFile writes the template if the global config file does not exist (first-run guidance).
// Returns the written path (empty = already existed, nothing written).
func EnsureConfigFile() (string, error) {
	gp, err := GlobalConfigPath()
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(gp); err == nil {
		return "", nil // already exists
	} else if !os.IsNotExist(err) {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(gp), 0o755); err != nil {
		return "", err
	}
	if err := atomicWrite(gp, []byte(configTemplate), 0o600); err != nil {
		return "", err
	}
	return gp, nil
}
