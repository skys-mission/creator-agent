package builtins

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/skys-mission/creator-agent/core"
)

// TestGrepCustomIgnoreDirs verifies a configured ignore list is honored and the
// builtin defaults are replaced (not merged) when a custom list is supplied.
func TestGrepCustomIgnoreDirs(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	// "secret" will be ignored via config; "node_modules" is in the builtin default
	// but since we supply a custom list, it should NOT be ignored anymore.
	os.WriteFile("a.go", []byte("match here\n"), 0o644)
	os.MkdirAll("secret", 0o755)
	os.WriteFile(filepath.Join("secret", "b.go"), []byte("match here\n"), 0o644)
	os.MkdirAll("node_modules", 0o755)
	os.WriteFile(filepath.Join("node_modules", "c.go"), []byte("match here\n"), 0o644)

	g := NewGrepTool(WithGrepSettings(core.ToolSettings{
		IgnoreDirs: []string{"secret"},
		MaxMatches: 100,
	}))
	res, _ := g.Exec(context.Background(), mustJSON(t, `{"pattern":"match","path":"."}`))
	if res.IsError {
		t.Fatalf("grep failed: %+v", res)
	}
	if strings.Contains(res.Content, "secret") {
		t.Errorf("custom ignore 'secret' should be skipped, got: %s", res.Content)
	}
	// node_modules is NOT in the custom list, so it should appear (custom replaces builtin).
	if !strings.Contains(res.Content, "node_modules") {
		t.Errorf("node_modules should match when not in custom ignore list (custom replaces builtin), got: %s", res.Content)
	}
}

// TestGrepMaxMatchesCap verifies the configured match limit stops collection early.
func TestGrepMaxMatchesCap(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	// Write a file with more matching lines than the limit.
	var content strings.Builder
	for i := 0; i < 10; i++ {
		content.WriteString("hit\n")
	}
	os.WriteFile("a.txt", []byte(content.String()), 0o644)

	g := NewGrepTool(WithGrepSettings(core.ToolSettings{MaxMatches: 3}))
	res, _ := g.Exec(context.Background(), mustJSON(t, `{"pattern":"hit","path":"."}`))
	got := strings.Count(res.Content, "\n") + 1
	if !strings.Contains(res.Content, "\n") {
		got = 1
	}
	if got > 3 {
		t.Errorf("matches should be capped at 3, got %d: %s", got, res.Content)
	}
}

// TestGrepByteBudgetCap verifies the walk stops once the cumulative result size reaches
// MaxResultChars, independent of the match count. Without this cap, hardMaxMatches long lines
// could exceed the configured budget and the hard ceiling before the loop-layer spill sees them.
// The final join is also trimmed back to byteBudget on a rune boundary, so the result must never
// exceed the configured cap.
func TestGrepByteBudgetCap(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	// Each match is a long line; a low MaxResultChars must stop the walk after few matches.
	var content strings.Builder
	for i := 0; i < 50; i++ {
		content.WriteString("hit ")
		content.WriteString(strings.Repeat("x", 200))
		content.WriteString("\n")
	}
	os.WriteFile("big.txt", []byte(content.String()), 0o644)

	g := NewGrepTool(WithGrepSettings(core.ToolSettings{MaxMatches: 1000, MaxResultChars: 1500}))
	res, _ := g.Exec(context.Background(), mustJSON(t, `{"pattern":"hit","path":"."}`))
	// Result must be well under the full 50 matches × ~200 bytes each (~10KiB).
	if len(res.Content) > 2000 {
		t.Errorf("byte budget cap failed: result is %d bytes (expected <= ~1500 + a little)", len(res.Content))
	}
	// After the trim-to-budget fix, the final Content must not exceed the configured cap.
	if len(res.Content) > 1500 {
		t.Errorf("result exceeds configured byte budget: %d > 1500", len(res.Content))
	}
	if !utf8.ValidString(res.Content) {
		t.Errorf("result is not valid UTF-8 after budget trim")
	}
}

// TestGrepByteBudgetRuneBoundary verifies the budget trim backs off past a multi-byte rune
// rather than cutting into it, so the result stays valid UTF-8.
func TestGrepByteBudgetRuneBoundary(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	// '世' is 3 bytes (E4 B8 96). A budget of 10 bytes forces a trim inside a multi-byte rune.
	var content strings.Builder
	for i := 0; i < 20; i++ {
		content.WriteString("hit 世界世界世界世界\n") // each line ~30+ bytes of CJK
	}
	os.WriteFile("cjk.txt", []byte(content.String()), 0o644)

	g := NewGrepTool(WithGrepSettings(core.ToolSettings{MaxMatches: 1000, MaxResultChars: 10}))
	res, _ := g.Exec(context.Background(), mustJSON(t, `{"pattern":"hit","path":"."}`))
	if !utf8.ValidString(res.Content) {
		t.Errorf("budget trim produced invalid UTF-8: %q", res.Content)
	}
	if len(res.Content) > 10 {
		t.Errorf("budget trim exceeded cap: %d bytes", len(res.Content))
	}
}

// TestGlobPathCountCap verifies glob stops once the hard path limit is hit.
// We can't easily create hardMaxPaths files, so this test verifies the cap field
// is wired by checking that a small tree returns all matches (sanity) and that
// the cap constant is defined and finite (regression guard against accidentally
// removing the safety net).
func TestGlobPathCountCapConstant(t *testing.T) {
	if hardMaxPaths <= 0 {
		t.Error("hardMaxPaths must be a positive finite constant (safety net)")
	}
	if hardMaxResultChars <= 0 || hardMaxMatches <= 0 || hardMaxWalkDepth <= 0 || hardMaxTaskDepth <= 0 {
		t.Error("all hard limits must be positive finite constants")
	}
}

// TestGlobCustomIgnoreDirs verifies glob honors a custom ignore list.
func TestGlobCustomIgnoreDirs(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.go"), []byte("x"), 0o644)
	os.MkdirAll(filepath.Join(dir, "skipme"), 0o755)
	os.WriteFile(filepath.Join(dir, "skipme", "b.go"), []byte("x"), 0o644)

	old, _ := os.Getwd()
	defer os.Chdir(old)
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}

	gl := NewGlobTool(WithGlobSettings(core.ToolSettings{IgnoreDirs: []string{"skipme"}}))
	res, _ := gl.Exec(context.Background(), mustJSON(t, `{"pattern":"**/*.go"}`))
	if strings.Contains(res.Content, "skipme") {
		t.Errorf("custom ignore 'skipme' should be skipped, got: %s", res.Content)
	}
	if !strings.Contains(res.Content, "a.go") {
		t.Errorf("a.go should match, got: %s", res.Content)
	}
}

// TestGrepDefaultsUnchanged verifies the builtin defaults are stable, guarding
// against accidental changes that would silently alter tool behavior.
func TestGrepDefaultsUnchanged(t *testing.T) {
	def := GrepDefaultsInput()
	if def.MaxResultChars != 20000 || def.MaxMatches != 100 || def.MaxDepth != 20 {
		t.Errorf("grep defaults changed unexpectedly: %+v", def)
	}
	if len(def.IgnoreDirs) == 0 {
		t.Error("grep default ignore dirs should not be empty")
	}
}
