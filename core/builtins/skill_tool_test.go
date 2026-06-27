package builtins

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/skys-mission/creator-agent/core"
)

// fakeSkillProvider is a test helper that returns preset skills.
type fakeSkillProvider struct {
	skills map[string]core.SkillInfo
}

func (f fakeSkillProvider) LookupSkill(name string) (core.SkillInfo, bool) {
	s, ok := f.skills[name]
	return s, ok
}

func TestSkillToolInfo(t *testing.T) {
	st := NewSkillTool(fakeSkillProvider{})
	info := st.Info()
	if info.Name != "skill" {
		t.Errorf("name = %q", info.Name)
	}
	if !info.ReadOnly || !info.ConcurrencySafe {
		t.Error("skill tool should be readonly + concurrency-safe (pure lookup)")
	}
}

func TestSkillToolLoadsBody(t *testing.T) {
	provider := fakeSkillProvider{
		skills: map[string]core.SkillInfo{
			"refactor": {Name: "refactor", Body: "# Refactoring Guide\n1. Run tests first"},
		},
	}
	st := NewSkillTool(provider)
	res, err := st.Exec(context.Background(), json.RawMessage(`{"name":"refactor"}`))
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Errorf("should succeed: %+v", res)
	}
	if res.Content != "# Refactoring Guide\n1. Run tests first" {
		t.Errorf("body = %q", res.Content)
	}
}

func TestSkillToolNotFound(t *testing.T) {
	st := NewSkillTool(fakeSkillProvider{skills: map[string]core.SkillInfo{}})
	res, _ := st.Exec(context.Background(), json.RawMessage(`{"name":"missing"}`))
	if !res.IsError {
		t.Error("missing skill should return IsError")
	}
}

// TestSkillToolSurfacesToolWhitelist verifies that when a skill declares a 'tools' whitelist, the
// SkillTool output includes an "[Allowed tools...]" header so the model knows the intended scope.
func TestSkillToolSurfacesToolWhitelist(t *testing.T) {
	provider := fakeSkillProvider{
		skills: map[string]core.SkillInfo{
			"scoped": {Name: "scoped", Body: "do the thing", Tools: []string{"read", "grep"}},
		},
	}
	st := NewSkillTool(provider)
	res, err := st.Exec(context.Background(), json.RawMessage(`{"name":"scoped"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Content, "Allowed tools") || !strings.Contains(res.Content, "read") || !strings.Contains(res.Content, "grep") {
		t.Errorf("output should surface the tool whitelist; got %q", res.Content)
	}
	if !strings.Contains(res.Content, "do the thing") {
		t.Errorf("output should still contain the body; got %q", res.Content)
	}
}

func TestSkillToolEmptyName(t *testing.T) {
	st := NewSkillTool(fakeSkillProvider{})
	res, _ := st.Exec(context.Background(), json.RawMessage(`{"name":""}`))
	if !res.IsError {
		t.Error("empty name should return IsError")
	}
}

func TestSkillToolNilProvider(t *testing.T) {
	st := NewSkillTool(nil)
	res, _ := st.Exec(context.Background(), json.RawMessage(`{"name":"x"}`))
	if !res.IsError {
		t.Error("nil provider should return IsError")
	}
}

func TestSkillToolInvalidInput(t *testing.T) {
	st := NewSkillTool(fakeSkillProvider{})
	res, _ := st.Exec(context.Background(), json.RawMessage(`not json`))
	if !res.IsError {
		t.Error("invalid JSON should return IsError")
	}
}

func TestSkillToolEmptyBody(t *testing.T) {
	provider := fakeSkillProvider{
		skills: map[string]core.SkillInfo{"empty": {Name: "empty", Body: ""}},
	}
	st := NewSkillTool(provider)
	res, _ := st.Exec(context.Background(), json.RawMessage(`{"name":"empty"}`))
	// empty body is not an error, just a hint
	if res.IsError {
		t.Error("empty body should not be IsError")
	}
}

// Ensure SkillTool implements core.Tool and fakeSkillProvider implements core.SkillProvider.
var _ core.Tool = (*SkillTool)(nil)
var _ core.SkillProvider = fakeSkillProvider{}
