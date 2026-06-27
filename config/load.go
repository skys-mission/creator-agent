package config

import (
	"bytes"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	toml "github.com/pelletier/go-toml/v2"

	"github.com/skys-mission/creator-agent/paths"
)

// projectConfigFile is the project-level config file path, resolved relative to the current working
// directory (./.creator/config.toml). Walking up parent directories is intentionally not done yet
// (reserved for the future).
const projectConfigFile = paths.ProjectConfigFile

// GlobalConfigPath returns the global config file path: $XDG_CONFIG_HOME/creator/config.toml when
// XDG_CONFIG_HOME is set, otherwise ~/.creator/config.toml. Delegates to the paths package so the
// directory layout lives in one place.
func GlobalConfigPath() (string, error) {
	return paths.ConfigFile()
}

// source is one config file read into memory (path kept for diagnostics and merge ordering).
type source struct {
	path string
	data []byte
}

// Load reads the global + project config and deep-merges them (project overrides global, per-field).
// Missing files are not an error. The merge is performed on the parsed TOML trees so that only keys
// actually present in the project file override the global file (e.g. a project profile that sets
// only `model` still inherits the global `api_key`). Unknown keys produce non-fatal warnings.
func Load() (*Config, error) {
	cfg := &Config{Profiles: map[string]Profile{}}

	sources, err := readSources()
	if err != nil {
		return nil, err
	}
	if len(sources) == 0 {
		return cfg, nil
	}

	merged := map[string]any{}
	for _, s := range sources {
		var tree map[string]any
		if err := toml.Unmarshal(s.data, &tree); err != nil {
			return nil, fmt.Errorf("parse %s: %w", s.path, err)
		}
		deepMergeMap(merged, tree)
		cfg.warnings = append(cfg.warnings, unknownKeyWarnings(s.data, s.path)...)
	}

	mergedBytes, err := toml.Marshal(merged)
	if err != nil {
		return nil, fmt.Errorf("merge config: %w", err)
	}
	if err := toml.Unmarshal(mergedBytes, cfg); err != nil {
		return nil, fmt.Errorf("decode merged config: %w", err)
	}
	if cfg.Profiles == nil {
		cfg.Profiles = map[string]Profile{}
	}

	cfg.loadedPath = sources[len(sources)-1].path // most specific file that was loaded
	cfg.warnings = append(cfg.warnings, cfg.validate()...)
	cfg.expandPaths()
	return cfg, nil
}

// readSources reads the global then project config files (in increasing priority). A missing file is
// skipped silently; any other read error is fatal.
func readSources() ([]source, error) {
	var sources []source

	if gp, err := GlobalConfigPath(); err == nil {
		if data, rerr := os.ReadFile(gp); rerr == nil {
			sources = append(sources, source{path: gp, data: data})
		} else if !os.IsNotExist(rerr) {
			return nil, fmt.Errorf("read global config %s: %w", gp, rerr)
		}
	}

	if data, rerr := os.ReadFile(projectConfigFile); rerr == nil {
		sources = append(sources, source{path: projectConfigFile, data: data})
	} else if !os.IsNotExist(rerr) {
		return nil, fmt.Errorf("read project config %s: %w", projectConfigFile, rerr)
	}

	return sources, nil
}

// deepMergeMap merges src into dst in place. Nested tables (map[string]any) merge recursively so
// individual fields override; scalars and arrays are replaced wholesale by src. This drives the
// global/project layering: project keys win, everything else is inherited.
func deepMergeMap(dst, src map[string]any) {
	for k, sv := range src {
		if dv, ok := dst[k]; ok {
			if dm, dok := dv.(map[string]any); dok {
				if sm, sok := sv.(map[string]any); sok {
					deepMergeMap(dm, sm)
					continue
				}
			}
		}
		dst[k] = sv
	}
}

// unknownKeyWarnings strict-decodes a single file to surface unknown keys (typos) with their source
// position. It is best-effort: syntax was already validated by the map decode in Load, so only the
// strict "unknown field" case is reported here; anything else is ignored to avoid double-reporting.
func unknownKeyWarnings(data []byte, path string) []string {
	dec := toml.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var throwaway Config
	err := dec.Decode(&throwaway)
	if err == nil {
		return nil
	}
	var strictErr *toml.StrictMissingError
	if errors.As(err, &strictErr) {
		return []string{fmt.Sprintf("%s: unknown config key(s) ignored:\n%s", path, strictErr.String())}
	}
	return nil
}

// expandPaths resolves "~" in user-supplied directory settings (done once after load so every
// consumer receives ready-to-use paths). Empty values are left untouched so each consumer's own
// default logic still applies.
func (c *Config) expandPaths() {
	c.Memory.Dir = ExpandHome(c.Memory.Dir)
	for i, d := range c.Skills.Dirs {
		c.Skills.Dirs[i] = ExpandHome(d)
	}
	for i, d := range c.Sandbox.AllowDirs {
		c.Sandbox.AllowDirs[i] = ExpandHome(d)
	}
}

// LoadedPath returns the most specific loaded config file path (empty = no config file loaded).
func (c *Config) LoadedPath() string { return c.loadedPath }

// Warnings returns non-fatal warnings collected during Load (unknown keys, invalid enum values, a
// schema version newer than supported, etc.). The caller decides how to surface them.
func (c *Config) Warnings() []string { return c.warnings }

// HasProfile checks whether a profile exists.
func (c *Config) HasProfile(name string) bool {
	_, ok := c.Profiles[name]
	return ok
}

// ExpandHome expands a leading "~" (alone or "~/...") to the current user's home directory. Other
// paths (empty, absolute, relative, or "~user") are returned unchanged. When the home directory
// cannot be resolved, the original path is returned so callers degrade gracefully.
func ExpandHome(path string) string {
	if path != "~" && !strings.HasPrefix(path, "~/") {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return path
	}
	if path == "~" {
		return home
	}
	return filepath.Join(home, path[2:])
}

// HostOf returns the host of base_url (for display). When base_url is empty, falls back to the
// official OpenAI host. Kept here as a pure function of the base_url string so main and tui share one
// implementation.
func HostOf(baseURL string) string {
	s := strings.TrimSpace(baseURL)
	if s == "" {
		return "api.openai.com"
	}
	if u, err := url.Parse(s); err == nil && u.Host != "" {
		return u.Host
	}
	if !strings.Contains(s, "://") {
		if u, err := url.Parse("http://" + s); err == nil && u.Host != "" {
			return u.Host
		}
	}
	s = strings.TrimPrefix(strings.TrimPrefix(s, "https://"), "http://")
	if i := strings.IndexByte(s, '/'); i >= 0 {
		s = s[:i]
	}
	return s
}
