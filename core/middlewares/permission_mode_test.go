package middlewares

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/skys-mission/creator-agent/core"
)

// modeMiddleware builds a mode-enabled permission middleware for tests.
func modeMiddleware(t *testing.T, mode Mode, ws string, allow, deny []string, infos []core.ToolInfo) *PermissionMiddleware {
	t.Helper()
	return NewPermission(PermissionConfig{
		Mode:          NewModeController(mode),
		WorkspaceRoot: ws,
		Allow:         allow,
		Deny:          deny,
		ToolInfos:     infos,
	})
}

func defaultToolInfos() []core.ToolInfo {
	return []core.ToolInfo{
		{Name: "read", ReadOnly: true},
		{Name: "grep", ReadOnly: true},
		{Name: "glob", ReadOnly: true},
		{Name: "write", ReadOnly: false},
		{Name: "edit", ReadOnly: false},
		{Name: "bash", ReadOnly: false},
		{Name: "todo_write", ReadOnly: false},
	}
}

func bashInput(t *testing.T, cmd string) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(map[string]string{"command": cmd})
	if err != nil {
		t.Fatalf("marshal bash input: %v", err)
	}
	return b
}

func writeInput(t *testing.T, path string) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(map[string]string{"path": path, "content": "x"})
	if err != nil {
		t.Fatalf("marshal write input: %v", err)
	}
	return b
}

// ===== NormalizeMode =====

func TestNormalizeMode(t *testing.T) {
	cases := map[Mode]Mode{
		"":          ModeDefault,
		"default":   ModeDefault,
		"ASK":       ModeDefault,
		"trust":     ModeTrust,
		"Trusted":   ModeTrust,
		"auto":      ModeAuto,
		"YOLO":      ModeAuto,
		"readonly":  ModeReadonly,
		"read-only": ModeReadonly,
		"safe":      ModeReadonly,
		"garbage":   ModeDefault,
	}
	for in, want := range cases {
		if got := NormalizeMode(in); got != want {
			t.Errorf("NormalizeMode(%q)=%q want %q", in, got, want)
		}
	}
}

// ===== modeDecision threshold matrix =====
// modeDecision is the pure threshold function; catastrophic interception happens earlier in
// decideByMode, so the matrix reflects only the threshold semantics per mode.

func TestModeDecisionMatrix(t *testing.T) {
	cases := []struct {
		mode Mode
		risk Risk
		want Effect
	}{
		// readonly: only Safe passes; everything else denied
		{ModeReadonly, RiskSafe, EffectAllow},
		{ModeReadonly, RiskNormal, EffectDeny},
		{ModeReadonly, RiskRisky, EffectDeny},
		{ModeReadonly, RiskCatastrophic, EffectDeny},
		// default: Safe allowed; writes/risky ask
		{ModeDefault, RiskSafe, EffectAllow},
		{ModeDefault, RiskNormal, EffectAsk},
		{ModeDefault, RiskRisky, EffectAsk},
		// trust: routine (Safe/Normal) allowed; risky asks
		{ModeTrust, RiskSafe, EffectAllow},
		{ModeTrust, RiskNormal, EffectAllow},
		{ModeTrust, RiskRisky, EffectAsk},
		// auto: all allowed at the threshold layer (catastrophic intercepted earlier)
		{ModeAuto, RiskSafe, EffectAllow},
		{ModeAuto, RiskNormal, EffectAllow},
		{ModeAuto, RiskRisky, EffectAllow},
		{ModeAuto, RiskCatastrophic, EffectAllow},
	}
	for _, c := range cases {
		got, _ := modeDecision(c.mode, c.risk)
		if got != c.want {
			t.Errorf("modeDecision(%s, risk=%d)=%v want %v", c.mode, c.risk, got, c.want)
		}
	}
}

// ===== classifyRisk =====

func TestClassifyRiskBash(t *testing.T) {
	cases := []struct {
		cmd  string
		want Risk
	}{
		{"ls", RiskNormal},
		{"git status", RiskNormal},
		{"git commit -m x", RiskNormal},
		{"git pull", RiskNormal},
		{"git push", RiskRisky},
		{"git push --force", RiskRisky},
		{"git reset --hard HEAD~1", RiskRisky},
		{"git clean -fd", RiskRisky},
		{"rm x", RiskRisky},
		{"rm -rf /tmp/x", RiskRisky},
		{"sudo ls", RiskRisky},
		{"curl http://x", RiskRisky},
		{"npm install", RiskRisky},
		{"npm test", RiskNormal},
		{"pip install x", RiskRisky},
		{"pip list", RiskNormal},
		{"docker ps", RiskNormal},
		{"docker run x", RiskRisky},
		{"go build", RiskNormal},
		{"go install x@latest", RiskRisky},
		{"make test", RiskNormal},
		{"echo hi", RiskNormal},
		{"rm -rf /", RiskCatastrophic},
		{"rm -fr /", RiskCatastrophic},
		{"rm -r -f /", RiskCatastrophic},
		{"rm --recursive --force /", RiskCatastrophic},
		{"rm -rf -- /", RiskCatastrophic},
		{"rm -rf /*", RiskCatastrophic},
		{"rm -rf ~/", RiskCatastrophic},
		{"rm -rf ~/project", RiskRisky},
		{"rm -rf /;echo done", RiskCatastrophic},
		{"rm -rf /|cat", RiskCatastrophic},
		{"rm -rf /&&echo", RiskCatastrophic},
		{"rm -rf /&", RiskCatastrophic},
		{"mkfs /dev/sda", RiskCatastrophic},
		{":(){:|:&};:", RiskCatastrophic},
	}
	for _, c := range cases {
		got := classifyRisk("bash", c.cmd, false, "")
		if got != c.want {
			t.Errorf("classifyRisk(bash %q)=%v want %v", c.cmd, got, c.want)
		}
	}
}

func TestClassifyRiskReadOnlyTool(t *testing.T) {
	if got := classifyRisk("read", "{}", true, ""); got != RiskSafe {
		t.Errorf("read-only tool = %v want Safe", got)
	}
}

func TestClassifyRiskWrite(t *testing.T) {
	ws := "/repo"
	cases := []struct {
		path string
		want Risk
	}{
		{filepath.Join(ws, "a.txt"), RiskNormal},
		{filepath.Join(ws, "sub", "b.txt"), RiskNormal},
		{"/etc/passwd", RiskRisky},
		{"/other", RiskRisky},
		{"", RiskNormal}, // empty path degrades to normal
	}
	for _, c := range cases {
		got := classifyRisk("write", c.path, false, ws)
		if got != c.want {
			t.Errorf("classifyRisk(write %q, ws=%q)=%v want %v", c.path, ws, got, c.want)
		}
	}
	// Empty workspace degrades everything to normal (unknown workspace).
	if got := classifyRisk("write", "/etc/passwd", false, ""); got != RiskNormal {
		t.Errorf("empty workspace should degrade to Normal, got %v", got)
	}
}

// ===== decideByMode integration (via check) =====

func TestDecideReadonlyBlocksWritesEvenWithAllow(t *testing.T) {
	ws := t.TempDir()
	mw := modeMiddleware(t, ModeReadonly, ws, []string{"write:*"}, nil, defaultToolInfos())

	dec, _ := mw.check("write", writeInput(t, filepath.Join(ws, "a.txt")))
	if dec.Effect != EffectDeny {
		t.Errorf("readonly + write (allow rule) = %v, want Deny (readonly overrides allow)", dec.Effect)
	}
	dec, _ = mw.check("bash", bashInput(t, "ls"))
	if dec.Effect != EffectDeny {
		t.Errorf("readonly + bash = %v, want Deny", dec.Effect)
	}
	dec, _ = mw.check("read", json.RawMessage(`{}`))
	if dec.Effect != EffectAllow {
		t.Errorf("readonly + read = %v, want Allow", dec.Effect)
	}
}

func TestDecideDefaultEquivalentToAskWrites(t *testing.T) {
	ws := t.TempDir()
	mw := modeMiddleware(t, ModeDefault, ws, nil, nil, defaultToolInfos())

	if dec, _ := mw.check("read", json.RawMessage(`{}`)); dec.Effect != EffectAllow {
		t.Errorf("default + read = %v, want Allow", dec.Effect)
	}
	if dec, _ := mw.check("write", writeInput(t, filepath.Join(ws, "a.txt"))); dec.Effect != EffectAsk {
		t.Errorf("default + write = %v, want Ask", dec.Effect)
	}
	if dec, _ := mw.check("bash", bashInput(t, "ls")); dec.Effect != EffectAsk {
		t.Errorf("default + bash = %v, want Ask", dec.Effect)
	}
}

func TestDecideTrust(t *testing.T) {
	ws := t.TempDir()
	mw := modeMiddleware(t, ModeTrust, ws, nil, nil, defaultToolInfos())

	// routine local edit → Allow
	if dec, _ := mw.check("write", writeInput(t, filepath.Join(ws, "a.txt"))); dec.Effect != EffectAllow {
		t.Errorf("trust + workspace write = %v, want Allow", dec.Effect)
	}
	// outside-workspace write → Ask
	if dec, _ := mw.check("write", writeInput(t, "/etc/passwd")); dec.Effect != EffectAsk {
		t.Errorf("trust + outside write = %v, want Ask", dec.Effect)
	}
	// benign command → Allow
	if dec, _ := mw.check("bash", bashInput(t, "ls")); dec.Effect != EffectAllow {
		t.Errorf("trust + benign bash = %v, want Allow", dec.Effect)
	}
	// risky command → Ask
	if dec, _ := mw.check("bash", bashInput(t, "rm x")); dec.Effect != EffectAsk {
		t.Errorf("trust + rm = %v, want Ask", dec.Effect)
	}
	// read → Allow
	if dec, _ := mw.check("read", json.RawMessage(`{}`)); dec.Effect != EffectAllow {
		t.Errorf("trust + read = %v, want Allow", dec.Effect)
	}
}

func TestDecideAutoAllowsAllButCatastrophic(t *testing.T) {
	ws := t.TempDir()
	mw := modeMiddleware(t, ModeAuto, ws, nil, nil, defaultToolInfos())

	if dec, _ := mw.check("write", writeInput(t, filepath.Join(ws, "a.txt"))); dec.Effect != EffectAllow {
		t.Errorf("auto + write = %v, want Allow", dec.Effect)
	}
	if dec, _ := mw.check("bash", bashInput(t, "rm x")); dec.Effect != EffectAllow {
		t.Errorf("auto + rm = %v, want Allow", dec.Effect)
	}
	if dec, _ := mw.check("bash", bashInput(t, "sudo ls")); dec.Effect != EffectAllow {
		t.Errorf("auto + sudo = %v, want Allow", dec.Effect)
	}
	// catastrophic is always blocked, even in auto
	if dec, _ := mw.check("bash", bashInput(t, "rm -rf /")); dec.Effect != EffectDeny {
		t.Errorf("auto + rm -rf / = %v, want Deny (catastrophic)", dec.Effect)
	}
	if dec, _ := mw.check("bash", bashInput(t, "mkfs /dev/sda")); dec.Effect != EffectDeny {
		t.Errorf("auto + mkfs = %v, want Deny (catastrophic)", dec.Effect)
	}
}

func TestDenyRuleWinsInAnyMode(t *testing.T) {
	ws := t.TempDir()
	mw := modeMiddleware(t, ModeAuto, ws, nil, []string{"bash:rm *"}, defaultToolInfos())

	if dec, _ := mw.check("bash", bashInput(t, "rm x")); dec.Effect != EffectDeny {
		t.Errorf("auto + deny rule rm = %v, want Deny", dec.Effect)
	}
}

func TestAllowRuleLiftsDefault(t *testing.T) {
	ws := t.TempDir()
	mw := modeMiddleware(t, ModeDefault, ws, []string{"bash:ls"}, nil, defaultToolInfos())

	if dec, _ := mw.check("bash", bashInput(t, "ls")); dec.Effect != EffectAllow {
		t.Errorf("default + allow rule ls = %v, want Allow (lift)", dec.Effect)
	}
	// non-allowed bash still asks
	if dec, _ := mw.check("bash", bashInput(t, "cat x")); dec.Effect != EffectAsk {
		t.Errorf("default + non-allowed bash = %v, want Ask", dec.Effect)
	}
}

// ===== bash compound under a mode (aggregate picks strictest) =====

func TestBashCompoundInTrustMode(t *testing.T) {
	ws := t.TempDir()
	mw := modeMiddleware(t, ModeTrust, ws, nil, nil, defaultToolInfos())

	// normal + risky → Ask
	if dec, _ := mw.check("bash", bashInput(t, "ls && rm x")); dec.Effect != EffectAsk {
		t.Errorf("trust compound ls&&rm = %v, want Ask", dec.Effect)
	}
	// all normal → Allow
	if dec, _ := mw.check("bash", bashInput(t, "ls && echo hi")); dec.Effect != EffectAllow {
		t.Errorf("trust compound ls&&echo = %v, want Allow", dec.Effect)
	}
	// any catastrophic → Deny
	if dec, _ := mw.check("bash", bashInput(t, "ls && rm -rf /")); dec.Effect != EffectDeny {
		t.Errorf("trust compound with catastrophic = %v, want Deny", dec.Effect)
	}
}

// ===== ModeController runtime switch =====

func TestModeControllerSwitchTakesEffectImmediately(t *testing.T) {
	ctl := NewModeController(ModeDefault)
	mw := NewPermission(PermissionConfig{Mode: ctl, ToolInfos: defaultToolInfos()})

	if dec, _ := mw.check("bash", bashInput(t, "ls")); dec.Effect != EffectAsk {
		t.Errorf("default bash = %v, want Ask", dec.Effect)
	}
	ctl.Set(ModeAuto)
	if dec, _ := mw.check("bash", bashInput(t, "ls")); dec.Effect != EffectAllow {
		t.Errorf("after Set(Auto) bash = %v, want Allow", dec.Effect)
	}
	ctl.Set(ModeReadonly)
	if dec, _ := mw.check("bash", bashInput(t, "ls")); dec.Effect != EffectDeny {
		t.Errorf("after Set(Readonly) bash = %v, want Deny", dec.Effect)
	}
}

// ===== BeforeModel system-prompt injection =====

func TestBeforeModelInjectsModeBlock(t *testing.T) {
	ctl := NewModeController(ModeTrust)
	mw := NewPermission(PermissionConfig{Mode: ctl, ToolInfos: defaultToolInfos()})

	st := &core.RunState{Messages: []core.Message{core.SystemMessage("base")}}
	if err := mw.BeforeModel(context.Background(), st); err != nil {
		t.Fatalf("BeforeModel: %v", err)
	}
	content := st.Messages[0].Content
	if !strings.Contains(content, permissionModeStart) {
		t.Errorf("mode block not injected: %q", content)
	}
	if !strings.Contains(content, "trust") {
		t.Errorf("mode block missing active mode: %q", content)
	}
	// idempotent: a second call must not duplicate the block
	if err := mw.BeforeModel(context.Background(), st); err != nil {
		t.Fatalf("BeforeModel second: %v", err)
	}
	if strings.Count(st.Messages[0].Content, permissionModeStart) != 1 {
		t.Errorf("mode block duplicated: %q", st.Messages[0].Content)
	}
}

func TestBeforeModelReflectsCurrentMode(t *testing.T) {
	ctl := NewModeController(ModeDefault)
	mw := NewPermission(PermissionConfig{Mode: ctl, ToolInfos: defaultToolInfos()})

	st := &core.RunState{Messages: []core.Message{core.SystemMessage("base")}}
	mw.BeforeModel(context.Background(), st)
	if !strings.Contains(st.Messages[0].Content, "default") {
		t.Errorf("expected default mode in block")
	}

	ctl.Set(ModeAuto)
	st2 := &core.RunState{Messages: []core.Message{core.SystemMessage("base")}}
	mw.BeforeModel(context.Background(), st2)
	c := st2.Messages[0].Content
	if !strings.Contains(c, "auto") {
		t.Errorf("expected auto mode after switch: %q", c)
	}
	if !strings.Contains(c, "Do NOT suggest") {
		t.Errorf("auto block must instruct not to suggest switching: %q", c)
	}
}

func TestBeforeModelNilModeNoOp(t *testing.T) {
	mw := NewPermission(PermissionConfig{ToolInfos: defaultToolInfos()}) // no Mode (legacy)
	st := &core.RunState{Messages: []core.Message{core.SystemMessage("base")}}
	if err := mw.BeforeModel(context.Background(), st); err != nil {
		t.Fatalf("BeforeModel: %v", err)
	}
	if st.Messages[0].Content != "base" {
		t.Errorf("nil mode should not modify system prompt, got %q", st.Messages[0].Content)
	}
}

// ===== buildModeBlock content =====

func TestBuildModeBlockContent(t *testing.T) {
	if !strings.Contains(buildModeBlock(ModeAuto), "Do NOT suggest") {
		t.Error("auto block must tell the model not to suggest switching")
	}
	if !strings.Contains(buildModeBlock(ModeReadonly), "blocked") {
		t.Error("readonly block must mention writes are blocked")
	}
	if !strings.Contains(buildModeBlock(ModeTrust), "risky") {
		t.Error("trust block must mention risky operations still ask")
	}
	if !strings.Contains(buildModeBlock(ModeDefault), "asks for approval") {
		t.Error("default block must mention approval prompts")
	}
	for _, m := range []Mode{ModeDefault, ModeTrust, ModeAuto, ModeReadonly} {
		b := buildModeBlock(m)
		if !strings.Contains(b, "cannot switch the mode yourself") {
			t.Errorf("mode %s block must state the model cannot switch modes", m)
		}
	}
}
