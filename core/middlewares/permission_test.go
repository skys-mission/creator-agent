package middlewares

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/skys-mission/creator-agent/core"
)

// ===== Rule matching =====

func TestRuleMatchesToolOnly(t *testing.T) {
	if !ruleMatches("read", "read", "anything") {
		t.Error(`"read" should match tool read`)
	}
	if ruleMatches("read", "write", "x") {
		t.Error(`"read" should not match tool write`)
	}
}

func TestRuleMatchesWithSpec(t *testing.T) {
	cases := []struct {
		rule, tool, spec string
		want             bool
	}{
		{"bash:git *", "bash", "git push", true},
		{"bash:git *", "bash", "git", false}, // "git *" requires a space after git
		{"bash:rm *", "bash", "rm -rf /", true},
		{"bash:ls", "bash", "ls", true},
		{"bash:ls", "bash", "ls -la", false}, // exact "ls" does not match "ls -la"
		{"*:safe", "anytool", "safe", true},  // wildcard tool
		{"read:*", "read", "any/path", true}, // wildcard spec
		{"read:*", "write", "x", false},
		{"read", "read", "/etc/passwd", true}, // no spec = match any invocation of this tool
	}
	for _, c := range cases {
		if got := ruleMatches(c.rule, c.tool, c.spec); got != c.want {
			t.Errorf("ruleMatches(%q, %q, %q) = %v, want %v", c.rule, c.tool, c.spec, got, c.want)
		}
	}
}

// ===== Bash compound command splitting =====

func TestSplitBashCommand(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"ls", []string{"ls"}},
		{"ls && rm x", []string{"ls", "rm x"}},
		{"a || b", []string{"a", "b"}},
		{"a | b | c", []string{"a", "b", "c"}},
		{"a; b; c", []string{"a", "b", "c"}},
		{"git pull\ngit push", []string{"git pull", "git push"}},
		{"  ls  ", []string{"ls"}},         // trim
		{"a &&  && b", []string{"a", "b"}}, // empty segments ignored
		{"", nil},                          // empty
		{"   ", nil},                       // all whitespace
	}
	for _, c := range cases {
		got := splitBashCommand(c.in)
		if len(got) != len(c.want) {
			t.Errorf("split(%q) = %v, want %v", c.in, got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("split(%q)[%d] = %q, want %q", c.in, i, got[i], c.want[i])
			}
		}
	}
}

// ===== globMatch (glob across /) =====

func TestGlobMatch(t *testing.T) {
	cases := []struct {
		pat, s string
		want   bool
	}{
		{"*", "anything", true},
		{"*", "rm -rf /tmp/x", true}, // * crosses /
		{"git *", "git push", true},
		{"git *", "git", false},
		{"rm -rf *", "rm -rf /", true},
		{"rm -rf *", "rm -rf /tmp/x/y", true},
		{"ls", "ls", true},
		{"ls", "ls -la", false},
		{"ab*c", "abxxxc", true},
		{"ab*c", "abxxx", false},
		{"a*b*c", "axxbxxc", true},
		{"", "", true},
		{"", "x", false},
	}
	for _, c := range cases {
		if got := globMatch(c.pat, c.s); got != c.want {
			t.Errorf("globMatch(%q, %q) = %v, want %v", c.pat, c.s, got, c.want)
		}
	}
}

// ===== extractBashCommand =====

func TestExtractBashCommand(t *testing.T) {
	if got := extractBashCommand(json.RawMessage(`{"command":"ls -la"}`)); got != "ls -la" {
		t.Errorf("extract = %q", got)
	}
	if got := extractBashCommand(json.RawMessage(`{}`)); got != "" {
		t.Errorf("empty cmd = %q", got)
	}
	if got := extractBashCommand(nil); got != "" {
		t.Errorf("nil input = %q", got)
	}
}

// ===== Middleware behavior (Effect branches) =====

// countTool counts invocations to verify allow/deny/ask behavior.
type countTool struct{ n int }

func (c *countTool) Info() core.ToolInfo {
	return core.ToolInfo{Name: "bash", InputSchema: json.RawMessage(`{"type":"object"}`)}
}
func (c *countTool) Exec(ctx context.Context, input json.RawMessage) (core.ToolResult, error) {
	c.n++
	return core.ToolResult{Content: "ran"}, nil
}

// TestPermissionAllow verifies that Allow rules pass through.
func TestPermissionAllow(t *testing.T) {
	mw := NewPermission(PermissionConfig{
		Allow: []string{"bash:ls *"},
	})
	tool := &countTool{}
	wrapped := mw.WrapTool("bash", func(ctx context.Context, in json.RawMessage) (core.ToolResult, error) {
		tool.n++
		return core.ToolResult{Content: "ran"}, nil
	})
	res, err := wrapped(context.Background(), json.RawMessage(`{"command":"ls -la"}`))
	if err != nil || res.IsError {
		t.Errorf("allow should pass: %+v %v", res, err)
	}
	if tool.n != 1 {
		t.Errorf("tool not called once: n=%d", tool.n)
	}
}

// TestPermissionDeny verifies that Deny rules intercept (return IsError, tool not called).
func TestPermissionDeny(t *testing.T) {
	mw := NewPermission(PermissionConfig{
		Allow: []string{"bash:ls *"},
		Deny:  []string{"bash:rm *"},
	})
	tool := &countTool{}
	wrapped := mw.WrapTool("bash", func(ctx context.Context, in json.RawMessage) (core.ToolResult, error) {
		tool.n++
		return core.ToolResult{Content: "ran"}, nil
	})
	res, err := wrapped(context.Background(), json.RawMessage(`{"command":"rm -rf /tmp/x"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Error("deny should return IsError")
	}
	if tool.n != 0 {
		t.Errorf("denied tool was called: n=%d", tool.n)
	}
}

// TestPermissionCompoundDenyBypass verifies that a compound command containing a denied subcommand is denied overall.
func TestPermissionCompoundDenyBypass(t *testing.T) {
	mw := NewPermission(PermissionConfig{
		Allow: []string{"bash:ls *"},
		Deny:  []string{"bash:rm *"},
	})
	tool := &countTool{}
	wrapped := mw.WrapTool("bash", func(ctx context.Context, in json.RawMessage) (core.ToolResult, error) {
		tool.n++
		return core.ToolResult{Content: "ran"}, nil
	})
	// ls is allowed, but rm is denied → overall must be denied
	res, _ := wrapped(context.Background(), json.RawMessage(`{"command":"ls && rm -rf /tmp/x"}`))
	if !res.IsError {
		t.Error("compound with denied subcommand should be denied (bypass prevention)")
	}
	if tool.n != 0 {
		t.Errorf("tool was called despite denied subcommand: n=%d", tool.n)
	}
}

// TestPermissionCompoundAllowNoBypass verifies that a compound command with an allowed subcommand but an unapproved one is not bypassed.
func TestPermissionCompoundAllowNoBypass(t *testing.T) {
	mw := NewPermission(PermissionConfig{
		Allow:         []string{"bash:ls"},
		DefaultPolicy: PolicyAskWrites,
		ToolInfos:     []core.ToolInfo{{Name: "bash"}}, // bash is not read-only → treated as write under ask_writes
	})
	tool := &countTool{}
	wrapped := mw.WrapTool("bash", func(ctx context.Context, in json.RawMessage) (core.ToolResult, error) {
		tool.n++
		return core.ToolResult{Content: "ran"}, nil
	})
	// ls is allowed, but rm -rf / is not matched by allow → ask_writes treats it as write; nil resolver → deny
	res, _ := wrapped(context.Background(), json.RawMessage(`{"command":"ls && rm -rf /"}`))
	if !res.IsError {
		t.Error("compound with unapproved subcommand should not be allowed (bypass)")
	}
	if tool.n != 0 {
		t.Errorf("tool was called despite unapproved subcommand: n=%d", tool.n)
	}
}

// TestPermissionAskDefaultDeny verifies that Ask with no resolver defaults to deny.
func TestPermissionAskDefaultDeny(t *testing.T) {
	mw := NewPermission(PermissionConfig{}) // no resolver = deny all
	tool := &countTool{}
	wrapped := mw.WrapTool("bash", func(ctx context.Context, in json.RawMessage) (core.ToolResult, error) {
		tool.n++
		return core.ToolResult{Content: "ran"}, nil
	})
	res, _ := wrapped(context.Background(), json.RawMessage(`{"command":"something"}`))
	if !res.IsError {
		t.Error("ask with no resolver should deny")
	}
}

// TestPermissionAskResolverAllow verifies that Ask with a resolver returning true allows the tool.
func TestPermissionAskResolverAllow(t *testing.T) {
	mw := NewPermission(PermissionConfig{
		Resolve: func(_ context.Context, name, input string) bool { return true },
	})
	tool := &countTool{}
	wrapped := mw.WrapTool("bash", func(ctx context.Context, in json.RawMessage) (core.ToolResult, error) {
		tool.n++
		return core.ToolResult{Content: "ran"}, nil
	})
	res, _ := wrapped(context.Background(), json.RawMessage(`{"command":"anything"}`))
	if res.IsError {
		t.Error("ask resolver returning true should allow")
	}
	if tool.n != 1 {
		t.Errorf("tool not called: n=%d", tool.n)
	}
}

// TestPermissionNonBashMatch verifies that non-bash tools are matched against the whole input (not split).
func TestPermissionNonBashMatch(t *testing.T) {
	mw := NewPermission(PermissionConfig{
		Allow: []string{"read:*"},
	})
	tool := &countTool{}
	wrapped := mw.WrapTool("read", func(ctx context.Context, in json.RawMessage) (core.ToolResult, error) {
		tool.n++
		return core.ToolResult{Content: "read"}, nil
	})
	res, _ := wrapped(context.Background(), json.RawMessage(`{"path":"/etc/passwd"}`))
	if res.IsError {
		t.Error("read:* should allow read of any path")
	}
}

// TestPermissionDenyOverridesAllow verifies that Deny rules take priority over Allow rules.
func TestPermissionDenyOverridesAllow(t *testing.T) {
	mw := NewPermission(PermissionConfig{
		Allow: []string{"bash:*"},
		Deny:  []string{"bash:rm *"},
	})
	tool := &countTool{}
	wrapped := mw.WrapTool("bash", func(ctx context.Context, in json.RawMessage) (core.ToolResult, error) {
		tool.n++
		return core.ToolResult{Content: "ran"}, nil
	})
	res, _ := wrapped(context.Background(), json.RawMessage(`{"command":"rm x"}`))
	if !res.IsError {
		t.Error("deny should override allow")
	}
}

func TestApproveKey(t *testing.T) {
	cases := []struct {
		tool, input, want string
	}{
		{"bash", `{"command":"rm -rf /tmp"}`, "bash:rm"},
		{"bash", `{"command":"git push"}`, "bash:git"},
		{"bash", `{"command":"mkdir /tmp/x"}`, "bash:mkdir"},
		{"write", `{"path":"/tmp/x"}`, "write:/tmp/x"},
		{"edit", `{"path":"/tmp/y"}`, "edit:/tmp/y"},
		{"read", `{"path":"/etc/passwd"}`, "read"},
		{"bash", `{}`, "bash"},
		{"write", `{}`, "write"},
	}
	for _, c := range cases {
		if got := ApproveKey(c.tool, c.input); got != c.want {
			t.Errorf("ApproveKey(%q,%q) = %q, want %q", c.tool, c.input, got, c.want)
		}
	}
}

func TestIsDangerous(t *testing.T) {
	dangerous := []string{
		"rm -rf /", "rm -rf /*", "rm -rf ~", "rm -rf $HOME",
		"rm -fr /", "rm -fr /*", "rm -fr ~",
		"mkfs.ext4 /dev/sda", "dd if=/dev/zero of=/dev/sda",
		"chmod -R 777 /", ":(){:|:&};:",
	}
	for _, cmd := range dangerous {
		if !isDangerous(cmd) {
			t.Errorf("isDangerous(%q) = false, want true", cmd)
		}
	}
	safe := []string{"ls -la", "git push", "mkdir /tmp/x", "echo hello", "rm file.txt", "rm -rf /tmp/x"}
	for _, cmd := range safe {
		if isDangerous(cmd) {
			t.Errorf("isDangerous(%q) = true, want false (false positive)", cmd)
		}
	}
}

func TestCheckDangerousDeny(t *testing.T) {
	mw := NewPermission(PermissionConfig{})
	// rm -rf / → built-in deny
	dec, _ := mw.check("bash", json.RawMessage(`{"command":"rm -rf /"}`))
	if dec.Effect != EffectDeny {
		t.Errorf("rm -rf / should be denied (built-in), got %v", dec.Effect)
	}
	// Normal command is not denied
	dec2, _ := mw.check("bash", json.RawMessage(`{"command":"ls -la"}`))
	if dec2.Effect == EffectDeny {
		t.Errorf("ls -la should not be denied, got %v", dec2.Effect)
	}
	// rm -rf /tmp should not be blocked (legitimate operation)
	dec3, _ := mw.check("bash", json.RawMessage(`{"command":"rm -rf /tmp/x"}`))
	if dec3.Effect == EffectDeny {
		t.Errorf("rm -rf /tmp/x should not be denied (false positive), got %v", dec3.Effect)
	}
	// rm -fr / → flag-order variant also built-in denied
	dec4, _ := mw.check("bash", json.RawMessage(`{"command":"rm -fr /"}`))
	if dec4.Effect != EffectDeny {
		t.Errorf("rm -fr / should be denied (built-in), got %v", dec4.Effect)
	}
	// rm -fr /tmp should not be blocked (legitimate operation)
	dec5, _ := mw.check("bash", json.RawMessage(`{"command":"rm -fr /tmp/x"}`))
	if dec5.Effect == EffectDeny {
		t.Errorf("rm -fr /tmp/x should not be denied (false positive), got %v", dec5.Effect)
	}
}

// ===== Input-glob permission matching (write/edit path-based rules) =====

// TestPermissionWritePathGlobAllow verifies write:/tmp/* allows writing to /tmp paths.
func TestPermissionWritePathGlobAllow(t *testing.T) {
	mw := NewPermission(PermissionConfig{
		Allow:         []string{"write:/tmp/*"},
		DefaultPolicy: PolicyAskAll,
	})
	dec, _ := mw.check("write", json.RawMessage(`{"path":"/tmp/test.txt","content":"x"}`))
	if dec.Effect != EffectAllow {
		t.Errorf("write:/tmp/test.txt should be allowed by write:/tmp/*; got %v (%s)", dec.Effect, dec.Reason)
	}
}

// TestPermissionWritePathGlobDeny verifies write to non-/tmp paths is not allowed by write:/tmp/*.
func TestPermissionWritePathGlobDeny(t *testing.T) {
	mw := NewPermission(PermissionConfig{
		Allow:         []string{"write:/tmp/*"},
		Deny:          []string{"write:/etc/*"},
		DefaultPolicy: PolicyAskAll,
	})
	// /home/x is not matched by /tmp/* → falls through to default (Ask).
	dec, _ := mw.check("write", json.RawMessage(`{"path":"/home/x.txt","content":"x"}`))
	if dec.Effect != EffectAsk {
		t.Errorf("write:/home/x.txt should be Ask (no allow rule matches); got %v", dec.Effect)
	}
	// /etc/passwd is explicitly denied.
	dec2, _ := mw.check("write", json.RawMessage(`{"path":"/etc/passwd","content":"x"}`))
	if dec2.Effect != EffectDeny {
		t.Errorf("write:/etc/passwd should be denied by write:/etc/*; got %v", dec2.Effect)
	}
}

// TestPermissionEditPathGlob verifies edit tools also support path glob matching.
func TestPermissionEditPathGlob(t *testing.T) {
	mw := NewPermission(PermissionConfig{
		Allow:         []string{"edit:/src/*"},
		DefaultPolicy: PolicyAskAll,
	})
	dec, _ := mw.check("edit", json.RawMessage(`{"path":"/src/main.go","old_string":"a","new_string":"b"}`))
	if dec.Effect != EffectAllow {
		t.Errorf("edit:/src/main.go should be allowed by edit:/src/*; got %v (%s)", dec.Effect, dec.Reason)
	}
}

// TestExtractFilePath verifies the path extraction from write/edit/read tool inputs.
func TestExtractFilePath(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{"path field", `{"path":"/tmp/x"}`, "/tmp/x"},
		{"file_path field", `{"file_path":"/src/y.go"}`, "/src/y.go"},
		{"empty", `{}`, ""},
		{"invalid json", `{bad`, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := extractFilePath(json.RawMessage(c.input))
			if got != c.want {
				t.Errorf("extractFilePath(%s) = %q, want %q", c.name, got, c.want)
			}
		})
	}
}
