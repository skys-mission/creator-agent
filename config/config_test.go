package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// isolateConfigEnv points config discovery at a temp HOME and disables XDG so a test cannot read the
// developer's real config. Returns the temp home dir.
func isolateConfigEnv(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	return home
}

// chdirTemp changes into a fresh temp dir for the test (so the project ./.creator/config.toml lookup
// is isolated) and restores the previous working directory on cleanup. Returns the temp dir.
func chdirTemp(t *testing.T) string {
	t.Helper()
	tmp := t.TempDir()
	old, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(tmp); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(old) })
	return tmp
}

// mustWrite writes content to path, creating parent directories as needed.
func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func TestExpandHome(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("no home dir: %v", err)
	}
	cases := map[string]string{
		"":             "",
		"~":            home,
		"~/skills":     filepath.Join(home, "skills"),
		"~/a/b":        filepath.Join(home, "a", "b"),
		"/abs/path":    "/abs/path",
		"relative/dir": "relative/dir",
		"~user/x":      "~user/x", // other users are left untouched
	}
	for in, want := range cases {
		if got := ExpandHome(in); got != want {
			t.Errorf("ExpandHome(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestExpandPathsAppliedOnConfig(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("no home dir: %v", err)
	}
	c := &Config{
		Memory:  MemoryConfig{Dir: "~/mem"},
		Skills:  SkillsConfig{Dirs: []string{"~/skills", "/abs"}},
		Sandbox: SandboxConfig{AllowDirs: []string{"~/extra"}},
	}
	c.expandPaths()
	if want := filepath.Join(home, "mem"); c.Memory.Dir != want {
		t.Errorf("Memory.Dir = %q, want %q", c.Memory.Dir, want)
	}
	if want := filepath.Join(home, "skills"); c.Skills.Dirs[0] != want {
		t.Errorf("Skills.Dirs[0] = %q, want %q", c.Skills.Dirs[0], want)
	}
	if c.Skills.Dirs[1] != "/abs" {
		t.Errorf("Skills.Dirs[1] = %q, want /abs", c.Skills.Dirs[1])
	}
	if want := filepath.Join(home, "extra"); c.Sandbox.AllowDirs[0] != want {
		t.Errorf("Sandbox.AllowDirs[0] = %q, want %q", c.Sandbox.AllowDirs[0], want)
	}
}

func TestResolveProfileFromConfig(t *testing.T) {
	cfg := &Config{
		Default: "deepseek",
		Profiles: map[string]Profile{
			"deepseek": {BaseURL: "https://api.deepseek.com", APIKey: "cfg-key", Model: "cfg-model"},
		},
	}
	p, err := cfg.Resolve("", Profile{})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if p.APIKey != "cfg-key" || p.Model != "cfg-model" || p.BaseURL != "https://api.deepseek.com" {
		t.Errorf("profile = %+v, want from config", p)
	}
}

func TestResolveEnvOverridesProfile(t *testing.T) {
	cfg := &Config{
		Default: "deepseek",
		Profiles: map[string]Profile{
			"deepseek": {BaseURL: "https://api.deepseek.com", APIKey: "cfg-key", Model: "cfg-model"},
		},
	}
	t.Setenv("OPENAI_API_KEY", "env-key")
	t.Setenv("OPENAI_MODEL", "env-model")

	p, err := cfg.Resolve("", Profile{})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if p.APIKey != "env-key" {
		t.Errorf("api key = %q, want env-key (env should override profile)", p.APIKey)
	}
	if p.Model != "env-model" {
		t.Errorf("model = %q, want env-model", p.Model)
	}
	if p.BaseURL != "https://api.deepseek.com" {
		t.Errorf("base url = %q, want profile value", p.BaseURL)
	}
}

func TestResolveFlagOverridesAll(t *testing.T) {
	cfg := &Config{
		Default: "deepseek",
		Profiles: map[string]Profile{
			"deepseek": {APIKey: "cfg-key"},
		},
	}
	t.Setenv("OPENAI_API_KEY", "env-key")

	p, err := cfg.Resolve("", Profile{APIKey: "flag-key", Model: "flag-model", BaseURL: "flag-url"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if p.APIKey != "flag-key" {
		t.Errorf("api key = %q, want flag-key (flag should win)", p.APIKey)
	}
	if p.Model != "flag-model" || p.BaseURL != "flag-url" {
		t.Errorf("flag did not override: %+v", p)
	}
}

func TestResolveMissingAPIKey(t *testing.T) {
	cfg := &Config{Profiles: map[string]Profile{}}
	t.Setenv("OPENAI_API_KEY", "")
	_, err := cfg.Resolve("", Profile{})
	if err == nil {
		t.Error("expected error for missing API key")
	}
}

func TestResolveUnknownProfile(t *testing.T) {
	cfg := &Config{Profiles: map[string]Profile{"a": {APIKey: "k"}}}
	if _, err := cfg.Resolve("missing", Profile{}); err == nil {
		t.Error("expected error for unknown profile name")
	}
}

func TestLoadTOML(t *testing.T) {
	home := isolateConfigEnv(t)
	chdirTemp(t)
	mustWrite(t, filepath.Join(home, ".creator", "config.toml"), `
default = "ds"
[profiles.ds]
base_url = "https://api.ds.com"
api_key = "sk-real-1234567890abcdef"
model = "ds-chat"
`)
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Default != "ds" {
		t.Errorf("default = %q, want ds", cfg.Default)
	}
	p := cfg.Profiles["ds"]
	if p.APIKey != "sk-real-1234567890abcdef" || p.Model != "ds-chat" {
		t.Errorf("profile ds = %+v", p)
	}
}

// TestResolvePlaceholderKey: regression test for unfilled template placeholder keys.
func TestResolvePlaceholderKey(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")
	cases := []string{
		"sk-xxx", "sk-...", "sk-your-key", "sk-xxxxx", "sk-test", "sk-demo",
		"sk-example", "sk-123456", "your-api-key", "<your-key>", "<api-key>",
		"YOUR_API_KEY", "REPLACE_ME",
	}
	for _, key := range cases {
		cfg := &Config{
			Default: "deepseek",
			Profiles: map[string]Profile{
				"deepseek": {BaseURL: "https://api.deepseek.com", APIKey: key, Model: "deepseek-chat"},
			},
		}
		_, err := cfg.Resolve("", Profile{})
		if err == nil {
			t.Errorf("key %q should be detected as placeholder", key)
			continue
		}
		if !strings.Contains(err.Error(), "placeholder") {
			t.Errorf("key %q: error should mention placeholder, got: %v", key, err)
		}
	}
}

func TestResolveRealKeyNotFlagged(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")
	cases := []string{
		"sk-deadbeefcafef00d1234567890abcdef",
		"sk-proj-abc123def456ghi789jkl012mno",
		"sk-ant-api03-XXXXXXXXXXXXXXXXXXXXXXXX",
	}
	for _, key := range cases {
		cfg := &Config{
			Default: "deepseek",
			Profiles: map[string]Profile{
				"deepseek": {BaseURL: "https://api.deepseek.com", APIKey: key, Model: "deepseek-chat"},
			},
		}
		if _, err := cfg.Resolve("", Profile{}); err != nil {
			t.Errorf("real key %q... must NOT be flagged: %v", key[:12], err)
		}
	}
}

func TestPlaceholderKeyHintUnit(t *testing.T) {
	for _, k := range []string{"sk-xxx", "sk-...", "your-api-key", "  sk-xxx  "} {
		if placeholderKeyHint(k, "p", "cfg.toml") == "" {
			t.Errorf("placeholderKeyHint(%q) should return hint", k)
		}
	}
	for _, k := range []string{"", "sk-deadbeefcafef00d1234567890abcdef", "sk-proj-realkey123"} {
		if h := placeholderKeyHint(k, "p", "cfg.toml"); h != "" {
			t.Errorf("placeholderKeyHint(%q) should be empty, got: %s", k, h)
		}
	}
}

func TestHostOf(t *testing.T) {
	cases := map[string]string{
		"":                          "api.openai.com",
		"https://api.deepseek.com":  "api.deepseek.com",
		"http://localhost:8080":     "localhost:8080",
		"https://api.openai.com/v1": "api.openai.com",
		"localhost:11434/v1":        "localhost:11434",
	}
	for in, want := range cases {
		if got := HostOf(in); got != want {
			t.Errorf("HostOf(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestProfileNamesSorted(t *testing.T) {
	names := profileNames(map[string]Profile{"zeta": {}, "alpha": {}, "beta": {}})
	if got := strings.Join(names, ","); got != "alpha,beta,zeta" {
		t.Errorf("profileNames sorted = %q", got)
	}
}

func TestNormalizedType(t *testing.T) {
	cases := map[string]string{"": "openai", "openai": "openai", "anthropic": "anthropic", "custom": "custom"}
	for in, want := range cases {
		if got := (Profile{Type: in}).NormalizedType(); got != want {
			t.Errorf("NormalizedType(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestHasProfile(t *testing.T) {
	cfg := &Config{Profiles: map[string]Profile{"deepseek": {APIKey: "k"}}}
	if !cfg.HasProfile("deepseek") {
		t.Error("HasProfile(deepseek) should be true")
	}
	if cfg.HasProfile("openai") {
		t.Error("HasProfile(openai) should be false")
	}
}

// TestGlobalConfigPathHomeFallback: with XDG unset, falls back to ~/.creator/config.toml.
func TestGlobalConfigPathHomeFallback(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("HOME", "/tmp/fake-home")
	gp, err := GlobalConfigPath()
	if err != nil {
		t.Fatalf("GlobalConfigPath: %v", err)
	}
	wantSuffix := filepath.Join(".creator", "config.toml")
	if !strings.HasSuffix(gp, wantSuffix) {
		t.Errorf("GlobalConfigPath = %q, want suffix %q", gp, wantSuffix)
	}
}

// TestGlobalConfigPathXDG: with XDG set, uses $XDG_CONFIG_HOME/creator/config.toml.
func TestGlobalConfigPathXDG(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/tmp/xdg")
	gp, err := GlobalConfigPath()
	if err != nil {
		t.Fatalf("GlobalConfigPath: %v", err)
	}
	want := filepath.Join("/tmp/xdg", "creator", "config.toml")
	if gp != want {
		t.Errorf("GlobalConfigPath = %q, want %q", gp, want)
	}
}

// TestLoadGlobalOnly: Load reads the global config.toml.
func TestLoadGlobalOnly(t *testing.T) {
	home := isolateConfigEnv(t)
	chdirTemp(t)
	globalPath := filepath.Join(home, ".creator", "config.toml")
	mustWrite(t, globalPath, `
default = "ds"
[profiles.ds]
api_key = "global-key"
model = "global-model"
base_url = "https://global.example.com"
`)
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.LoadedPath() == "" {
		t.Error("LoadedPath should be set after Load")
	}
	if cfg.Default != "ds" || !cfg.HasProfile("ds") {
		t.Errorf("global config not loaded: default=%q", cfg.Default)
	}
	if p := cfg.Profiles["ds"]; p.APIKey != "global-key" || p.Model != "global-model" {
		t.Errorf("global profile fields wrong: %+v", p)
	}
}

// TestLoadProjectDeepMergesGlobal pins the NEW deep-merge semantics: a project profile that sets
// only `model` inherits the global `api_key` (per-field merge), and the project file wins where it
// sets a value.
func TestLoadProjectDeepMergesGlobal(t *testing.T) {
	home := isolateConfigEnv(t)
	tmp := chdirTemp(t)
	mustWrite(t, filepath.Join(home, ".creator", "config.toml"), `
default = "ds"
[profiles.ds]
api_key = "global-key"
model = "global-model"
base_url = "https://global.example.com"
`)
	mustWrite(t, filepath.Join(tmp, ".creator", "config.toml"), `
[profiles.ds]
model = "project-model"
`)
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	p := cfg.Profiles["ds"]
	if p.Model != "project-model" {
		t.Errorf("model = %q, want project-model (project overrides)", p.Model)
	}
	if p.APIKey != "global-key" {
		t.Errorf("api_key = %q, want global-key (inherited via deep merge)", p.APIKey)
	}
	if p.BaseURL != "https://global.example.com" {
		t.Errorf("base_url = %q, want inherited global value", p.BaseURL)
	}
	wantProject := filepath.Join(".creator", "config.toml")
	if cfg.LoadedPath() != wantProject {
		t.Errorf("LoadedPath = %q, want %q", cfg.LoadedPath(), wantProject)
	}
}

func TestLoadMissingFileOK(t *testing.T) {
	isolateConfigEnv(t)
	chdirTemp(t)
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load with no files should not error: %v", err)
	}
	if cfg.LoadedPath() != "" {
		t.Errorf("LoadedPath should be empty when no file loaded, got %q", cfg.LoadedPath())
	}
}

func TestEnsureConfigFileCreates(t *testing.T) {
	isolateConfigEnv(t)
	path, err := EnsureConfigFile()
	if err != nil {
		t.Fatalf("EnsureConfigFile: %v", err)
	}
	if path == "" {
		t.Fatal("should return created path")
	}
	if !strings.HasSuffix(path, ".toml") {
		t.Errorf("created path should be .toml, got %q", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("file not written: %v", err)
	}
	if !strings.Contains(string(data), "api_key") || !strings.Contains(string(data), "[profiles.") {
		t.Errorf("template missing expected fields: %q", string(data))
	}
	// The generated template must parse cleanly and load back.
	chdirTemp(t)
	if _, err := Load(); err != nil {
		t.Errorf("generated template should load: %v", err)
	}
}

func TestEnsureConfigFileIdempotent(t *testing.T) {
	isolateConfigEnv(t)
	if path, _ := EnsureConfigFile(); path == "" {
		t.Fatal("first call should create")
	}
	gp, _ := GlobalConfigPath()
	_ = os.WriteFile(gp, []byte("# user custom content\n"), 0o600)
	if path, _ := EnsureConfigFile(); path != "" {
		t.Errorf("second call should return empty path (file exists), got %q", path)
	}
	data, _ := os.ReadFile(gp)
	if !strings.Contains(string(data), "user custom content") {
		t.Error("EnsureConfigFile overwrote existing user config")
	}
}

func TestLoadToolSettings(t *testing.T) {
	home := isolateConfigEnv(t)
	chdirTemp(t)
	mustWrite(t, filepath.Join(home, ".creator", "config.toml"), `
[tools_defaults]
max_result_chars = 15000
ignore_dirs = [".git", "vendor", "node_modules"]
max_depth = 25

[tools.grep]
max_matches = 500
[tools.bash]
timeout = "2m"
[tools.task]
max_depth = 3
`)
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.ToolsDefaults.MaxResultChars != 15000 {
		t.Errorf("ToolsDefaults.MaxResultChars = %d, want 15000", cfg.ToolsDefaults.MaxResultChars)
	}
	if len(cfg.ToolsDefaults.IgnoreDirs) != 3 {
		t.Errorf("ToolsDefaults.IgnoreDirs len = %d, want 3", len(cfg.ToolsDefaults.IgnoreDirs))
	}
	if cfg.ToolsDefaults.MaxDepth != 25 {
		t.Errorf("ToolsDefaults.MaxDepth = %d, want 25", cfg.ToolsDefaults.MaxDepth)
	}
	if cfg.Tools["grep"].MaxMatches != 500 {
		t.Errorf("Tools[grep].MaxMatches = %d, want 500", cfg.Tools["grep"].MaxMatches)
	}
	if cfg.Tools["bash"].Timeout != "2m" {
		t.Errorf("Tools[bash].Timeout = %q, want 2m", cfg.Tools["bash"].Timeout)
	}
	if cfg.Tools["task"].MaxDepth != 3 {
		t.Errorf("Tools[task].MaxDepth = %d, want 3", cfg.Tools["task"].MaxDepth)
	}
}

func TestLoadAppearanceTheme(t *testing.T) {
	home := isolateConfigEnv(t)
	chdirTemp(t)
	mustWrite(t, filepath.Join(home, ".creator", "config.toml"), `
[appearance]
theme = "light"
`)
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Appearance.Theme != "light" {
		t.Errorf("Appearance.Theme = %q, want light", cfg.Appearance.Theme)
	}
	if got := cfg.Appearance.NormalizedTheme(); got != "light" {
		t.Errorf("NormalizedTheme() = %q, want light", got)
	}
}

func TestAppearanceNormalizedTheme(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", "dark"}, {"dark", "dark"}, {"light", "light"},
		{"Dark", "dark"}, {" LIGHT ", "light"}, {"bogus", "dark"},
	}
	for _, tc := range cases {
		if got := (AppearanceConfig{Theme: tc.in}).NormalizedTheme(); got != tc.want {
			t.Errorf("NormalizedTheme(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestAppearanceNormalizedLanguage(t *testing.T) {
	t.Setenv("LANG", "en_US.UTF-8")
	t.Setenv("LC_ALL", "")
	t.Setenv("LC_CTYPE", "")
	cases := []struct{ in, want string }{
		{"", "en"}, {"en", "en"}, {"zh", "zh"}, {"zh-CN", "zh"},
		{"zh_TW", "zh"}, {"ZH", "zh"}, {"bogus", "en"},
	}
	for _, tc := range cases {
		if got := (AppearanceConfig{Language: tc.in}).NormalizedLanguage(); got != tc.want {
			t.Errorf("NormalizedLanguage(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestAppearanceNormalizedLanguageLocaleAutoDetect(t *testing.T) {
	t.Setenv("LANG", "zh_CN.UTF-8")
	t.Setenv("LC_ALL", "")
	t.Setenv("LC_CTYPE", "")
	if got := (AppearanceConfig{}).NormalizedLanguage(); got != "zh" {
		t.Errorf("auto-detect with LANG=zh_CN = %q, want zh", got)
	}
}

// TestLoadTOMLVariants verifies a profile's variants parse with all override fields.
func TestLoadTOMLVariants(t *testing.T) {
	home := isolateConfigEnv(t)
	chdirTemp(t)
	mustWrite(t, filepath.Join(home, ".creator", "config.toml"), `
default = "openai"
[profiles.openai]
base_url = "https://api.openai.com/v1"
api_key = "sk-x"
model = "gpt-4o"
variant = "fast"

[profiles.openai.variants.fast]
temperature = 0.2
max_tokens = 1024
[profiles.openai.variants.fast.headers]
X-Tag = "fast"
[profiles.openai.variants.fast.body]
presence_penalty = 0.5

[profiles.openai.variants.long]
top_p = 0.9
max_tokens = 8192
`)
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	p := cfg.Profiles["openai"]
	if p.Variant != "fast" {
		t.Errorf("profile default variant = %q, want fast", p.Variant)
	}
	if len(p.Variants) != 2 {
		t.Fatalf("variants = %d, want 2: %+v", len(p.Variants), p.Variants)
	}
	fast := p.Variants["fast"]
	if fast.Temperature == nil || *fast.Temperature != 0.2 {
		t.Errorf("fast.temperature = %v, want 0.2", fast.Temperature)
	}
	if fast.MaxTokens == nil || *fast.MaxTokens != 1024 {
		t.Errorf("fast.max_tokens = %v, want 1024", fast.MaxTokens)
	}
	if fast.Headers["X-Tag"] != "fast" {
		t.Errorf("fast.headers[X-Tag] = %q, want fast", fast.Headers["X-Tag"])
	}
	if fast.Body["presence_penalty"] != 0.5 {
		t.Errorf("fast.body[presence_penalty] = %v, want 0.5", fast.Body["presence_penalty"])
	}
	long := p.Variants["long"]
	if long.TopP == nil || *long.TopP != 0.9 {
		t.Errorf("long.top_p = %v, want 0.9", long.TopP)
	}
	names := p.VariantNames()
	if len(names) != 2 || names[0] != "fast" || names[1] != "long" {
		t.Errorf("VariantNames() = %+v, want [fast long]", names)
	}
	if (&Profile{}).VariantNames() != nil {
		t.Errorf("empty profile VariantNames() should be nil")
	}
}

// TestValidateWarnings covers enum/version/default validation surfaced as non-fatal warnings.
func TestValidateWarnings(t *testing.T) {
	home := isolateConfigEnv(t)
	chdirTemp(t)
	mustWrite(t, filepath.Join(home, ".creator", "config.toml"), `
version = 999
default = "nope"

[profiles.a]
type = "weird"
api_key = "k"

[permissions]
mode = "bogus"

[sandbox]
mode = "wild"
`)
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	joined := strings.Join(cfg.Warnings(), "\n")
	for _, want := range []string{"version", "default profile", "unknown type", "permissions.mode", "sandbox.mode"} {
		if !strings.Contains(joined, want) {
			t.Errorf("warnings missing %q; got:\n%s", want, joined)
		}
	}
}

// TestLoadUnknownKeyWarns verifies a typo'd key produces a non-fatal warning (load still succeeds).
func TestLoadUnknownKeyWarns(t *testing.T) {
	home := isolateConfigEnv(t)
	chdirTemp(t)
	mustWrite(t, filepath.Join(home, ".creator", "config.toml"), `
[profiles.a]
api_key = "k"
modle = "typo"
`)
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load should not fail on unknown key: %v", err)
	}
	if !strings.Contains(strings.Join(cfg.Warnings(), "\n"), "unknown config key") {
		t.Errorf("expected unknown-key warning, got: %v", cfg.Warnings())
	}
}

// TestToHooksConversion verifies the config DTO converts to the middleware HooksConfig.
func TestToHooksConversion(t *testing.T) {
	cfg := &Config{Hooks: HooksConfig{
		PreToolUse: []HookEntry{{Matcher: "bash", Command: "a"}},
		Stop:       []HookEntry{{Command: "b"}},
	}}
	h := cfg.ToHooks()
	if len(h.PreToolUse) != 1 || h.PreToolUse[0].Matcher != "bash" || h.PreToolUse[0].Command != "a" {
		t.Errorf("PreToolUse conversion wrong: %+v", h.PreToolUse)
	}
	if len(h.Stop) != 1 || h.Stop[0].Command != "b" {
		t.Errorf("Stop conversion wrong: %+v", h.Stop)
	}
	if len(h.PostToolUse) != 0 {
		t.Errorf("PostToolUse should be empty, got %+v", h.PostToolUse)
	}
}

// TestLoadMCPServers verifies JSON parsing, env-map flattening, and tool-override conversion.
func TestLoadMCPServers(t *testing.T) {
	home := isolateConfigEnv(t)
	chdirTemp(t)
	mustWrite(t, filepath.Join(home, ".creator", "mcp.json"), `{
  "mcpServers": {
    "fs": {
      "type": "stdio", "command": "npx", "args": ["-y", "pkg"],
      "env": {"K": "V"},
      "tools": {"read_file": {"readOnly": true, "concurrencySafe": true, "maxResultChars": 5}}
    }
  }
}`)
	servers, warnings, err := LoadMCPServers()
	if err != nil {
		t.Fatalf("LoadMCPServers: %v", err)
	}
	if len(warnings) != 0 {
		t.Errorf("unexpected warnings: %v", warnings)
	}
	if len(servers) != 1 {
		t.Fatalf("servers = %d, want 1", len(servers))
	}
	s := servers[0]
	if s.Name != "fs" || s.Cmd != "npx" || s.Type != "stdio" {
		t.Errorf("server fields wrong: %+v", s)
	}
	if len(s.Args) != 2 || s.Args[0] != "-y" {
		t.Errorf("args wrong: %+v", s.Args)
	}
	if len(s.Env) != 1 || s.Env[0] != "K=V" {
		t.Errorf("env wrong: %+v", s.Env)
	}
	if len(s.Tools) != 1 || s.Tools[0].Name != "read_file" || !s.Tools[0].ReadOnly || s.Tools[0].MaxResultChars != 5 {
		t.Errorf("tool override wrong: %+v", s.Tools)
	}
}

// TestLoadMCPServersProjectMerge verifies a project mcp.json deep-merges over global by server name,
// and that "disabled": true drops a globally-defined server.
func TestLoadMCPServersProjectMerge(t *testing.T) {
	home := isolateConfigEnv(t)
	tmp := chdirTemp(t)
	mustWrite(t, filepath.Join(home, ".creator", "mcp.json"), `{
  "mcpServers": {
    "fs": {"type": "stdio", "command": "global-cmd"},
    "weather": {"type": "stdio", "command": "weather-cmd"}
  }
}`)
	mustWrite(t, filepath.Join(tmp, ".creator", "mcp.json"), `{
  "mcpServers": {
    "fs": {"command": "project-cmd"},
    "weather": {"disabled": true}
  }
}`)
	servers, _, err := LoadMCPServers()
	if err != nil {
		t.Fatalf("LoadMCPServers: %v", err)
	}
	if len(servers) != 1 {
		t.Fatalf("servers = %d, want 1 (weather disabled)", len(servers))
	}
	if servers[0].Name != "fs" || servers[0].Cmd != "project-cmd" {
		t.Errorf("expected fs overridden to project-cmd, got %+v", servers[0])
	}
	if servers[0].Type != "stdio" {
		t.Errorf("type should be inherited from global, got %q", servers[0].Type)
	}
}

// TestDeepMergeMap unit-tests the merge primitive: tables merge recursively, scalars/arrays replace.
func TestDeepMergeMap(t *testing.T) {
	dst := map[string]any{
		"scalar": "old",
		"arr":    []any{1, 2},
		"table":  map[string]any{"a": 1, "b": 2},
	}
	src := map[string]any{
		"scalar": "new",
		"arr":    []any{9},
		"table":  map[string]any{"b": 20, "c": 30},
		"added":  true,
	}
	deepMergeMap(dst, src)
	if dst["scalar"] != "new" {
		t.Errorf("scalar = %v, want new", dst["scalar"])
	}
	if arr, _ := dst["arr"].([]any); len(arr) != 1 || arr[0] != 9 {
		t.Errorf("arr = %v, want [9] (replace)", dst["arr"])
	}
	tbl, _ := dst["table"].(map[string]any)
	if tbl["a"] != 1 || tbl["b"] != 20 || tbl["c"] != 30 {
		t.Errorf("table merge wrong: %+v", tbl)
	}
	if dst["added"] != true {
		t.Errorf("added key missing")
	}
}
