package middlewares

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/skys-mission/creator-agent/core"
)

// ===== DefaultPolicy branches =====

// TestPolicyAskWritesReadonlyAllowed verifies that read-only tools are allowed under the ask_writes policy.
func TestPolicyAskWritesReadonlyAllowed(t *testing.T) {
	mw := NewPermission(PermissionConfig{
		DefaultPolicy: PolicyAskWrites,
		ToolInfos: []core.ToolInfo{
			{Name: "read", ReadOnly: true},
			{Name: "write", ReadOnly: false},
		},
	})
	dec, _ := mw.check("read", json.RawMessage(`{}`))
	if dec.Effect != EffectAllow {
		t.Errorf("readonly tool under ask_writes = %v, want Allow", dec.Effect)
	}
}

// TestPolicyAskWritesWriteToolAsks verifies that write tools trigger Ask under the ask_writes policy.
func TestPolicyAskWritesWriteToolAsks(t *testing.T) {
	mw := NewPermission(PermissionConfig{
		DefaultPolicy: PolicyAskWrites,
		ToolInfos:     []core.ToolInfo{{Name: "write", ReadOnly: false}},
	})
	dec, _ := mw.check("write", json.RawMessage(`{"path":"x","content":"y"}`))
	if dec.Effect != EffectAsk {
		t.Errorf("write tool under ask_writes = %v, want Ask", dec.Effect)
	}
}

// TestPolicyAskAll verifies that all tools trigger Ask under the ask_all policy, including read-only ones.
func TestPolicyAskAll(t *testing.T) {
	mw := NewPermission(PermissionConfig{
		DefaultPolicy: PolicyAskAll,
		ToolInfos:     []core.ToolInfo{{Name: "read", ReadOnly: true}},
	})
	dec, _ := mw.check("read", json.RawMessage(`{}`))
	if dec.Effect != EffectAsk {
		t.Errorf("readonly under ask_all = %v, want Ask", dec.Effect)
	}
}

// TestPolicyAllowAll verifies that all tools are allowed under the allow_all policy.
func TestPolicyAllowAll(t *testing.T) {
	mw := NewPermission(PermissionConfig{
		DefaultPolicy: PolicyAllowAll,
		ToolInfos:     []core.ToolInfo{{Name: "write", ReadOnly: false}},
	})
	dec, _ := mw.check("write", json.RawMessage(`{}`))
	if dec.Effect != EffectAllow {
		t.Errorf("write under allow_all = %v, want Allow", dec.Effect)
	}
}

// TestPolicyDefaultIsAskWrites verifies that the default policy (empty DefaultPolicy) equals ask_writes.
func TestPolicyDefaultIsAskWrites(t *testing.T) {
	mw := NewPermission(PermissionConfig{
		ToolInfos: []core.ToolInfo{
			{Name: "read", ReadOnly: true},
			{Name: "bash", ReadOnly: false},
		},
		// DefaultPolicy left empty
	})
	ro, _ := mw.check("read", json.RawMessage(`{}`))
	wr, _ := mw.check("bash", json.RawMessage(`{"command":"ls"}`))
	if ro.Effect != EffectAllow {
		t.Errorf("default readonly = %v, want Allow", ro.Effect)
	}
	if wr.Effect != EffectAsk {
		t.Errorf("default write = %v, want Ask", wr.Effect)
	}
}

// TestPolicyRulesOverrideDefault verifies that explicit Allow/Deny rules override the DefaultPolicy.
func TestPolicyRulesOverrideDefault(t *testing.T) {
	mw := NewPermission(PermissionConfig{
		Allow:         []string{"write:*"}, // explicitly allow write
		DefaultPolicy: PolicyAskWrites,
		ToolInfos:     []core.ToolInfo{{Name: "write", ReadOnly: false}},
	})
	dec, _ := mw.check("write", json.RawMessage(`{}`))
	if dec.Effect != EffectAllow {
		t.Errorf("explicit allow rule should override default: %v", dec.Effect)
	}
}

// TestPolicyUnknownToolTreatedAsWrite verifies that unknown tools (not in ToolInfos) are treated as write operations under ask_writes (fail-closed).
func TestPolicyUnknownToolTreatedAsWrite(t *testing.T) {
	mw := NewPermission(PermissionConfig{
		DefaultPolicy: PolicyAskWrites,
		ToolInfos:     []core.ToolInfo{{Name: "read", ReadOnly: true}}, // does not include "unknown"
	})
	dec, _ := mw.check("unknown", json.RawMessage(`{}`))
	if dec.Effect != EffectAsk {
		t.Errorf("unknown tool = %v, want Ask (fail-closed)", dec.Effect)
	}
}

// TestWrapToolReadonlySkipsResolver verifies that read-only tools bypass the resolver under ask_writes.
func TestWrapToolReadonlySkipsResolver(t *testing.T) {
	resolverCalled := false
	mw := NewPermission(PermissionConfig{
		DefaultPolicy: PolicyAskWrites,
		ToolInfos:     []core.ToolInfo{{Name: "read", ReadOnly: true}},
		Resolve: func(_ context.Context, name, input string) bool {
			resolverCalled = true
			return true
		},
	})
	tool := &countTool{}
	wrapped := mw.WrapTool("read", func(ctx context.Context, in json.RawMessage) (core.ToolResult, error) {
		tool.n++
		return core.ToolResult{Content: "ok"}, nil
	})
	res, err := wrapped(context.Background(), json.RawMessage(`{}`))
	if err != nil || res.IsError {
		t.Errorf("readonly should pass without resolver: %+v", res)
	}
	if resolverCalled {
		t.Error("resolver should NOT be called for readonly tool")
	}
}

// countTool is reused from permission_test.go (same package).
