package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

// TestSpillNoLimit verifies: MaxResultChars <=0 -> no limit, return unchanged.
func TestSpillNoLimit(t *testing.T) {
	info := ToolInfo{Name: "t", MaxResultChars: 0}
	r := ToolResult{Content: strings.Repeat("x", 100000)}
	out := maybeSpillResult(info, r)
	if out.Content != r.Content {
		t.Error("MaxResultChars<=0 should pass through unchanged")
	}
}

// TestSpillUnderLimit verifies: content <= MaxResultChars -> unchanged, no spill.
func TestSpillUnderLimit(t *testing.T) {
	info := ToolInfo{Name: "t", MaxResultChars: 1000}
	r := ToolResult{Content: "short"}
	out := maybeSpillResult(info, r)
	if out.Content != "short" {
		t.Errorf("under-limit content changed: %q", out.Content)
	}
}

// TestSpillOverLimit verifies: exceeding limit -> spill + preview + path hint.
func TestSpillOverLimit(t *testing.T) {
	info := ToolInfo{Name: "t", MaxResultChars: 100}
	big := strings.Repeat("y", 5000)
	r := ToolResult{Content: big}
	out := maybeSpillResult(info, r)

	if len(out.Content) >= len(big) {
		t.Errorf("content not shrunk: got %d bytes", len(out.Content))
	}
	if !strings.Contains(out.Content, "spilled to") {
		t.Errorf("missing spill path hint: %q", out.Content)
	}
	idx := strings.LastIndex(out.Content, "spilled to ")
	if idx < 0 {
		t.Fatal("no path marker")
	}
	tail := out.Content[idx+len("spilled to "):]
	tail = strings.TrimRight(tail, ")")
	path := strings.TrimSpace(tail)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("spill file unreadable: %v", err)
	}
	if string(data) != big {
		t.Errorf("spilled content mismatch: got %d bytes, want %d", len(data), len(big))
	}
	if !strings.HasPrefix(filepath.Base(path), "creator-agent-t-") {
		t.Errorf("spill filename prefix wrong: %s", filepath.Base(path))
	}
	if !strings.HasPrefix(big, out.Content[:spillPreview]) {
		t.Error("preview is not a prefix of full content")
	}
}

// TestSpillPreviewUTF8Safe verifies the successful spill preview does not split UTF-8 runes.
func TestSpillPreviewUTF8Safe(t *testing.T) {
	info := ToolInfo{Name: "t", MaxResultChars: 10}
	r := ToolResult{Content: strings.Repeat("你", 1000)}
	out := maybeSpillResult(info, r)
	if !strings.Contains(out.Content, "spilled to") {
		t.Fatalf("expected spill hint, got %q", out.Content)
	}
	if !utf8.ValidString(out.Content) {
		t.Errorf("spill preview should remain valid UTF-8")
	}
}

// TestSpillSkipsError verifies: IsError results are not spilled (business errors go directly to the model).
func TestSpillSkipsError(t *testing.T) {
	info := ToolInfo{Name: "t", MaxResultChars: 10}
	r := ToolResult{Content: strings.Repeat("e", 1000), IsError: true}
	out := maybeSpillResult(info, r)
	if out.Content != r.Content {
		t.Error("IsError result was modified")
	}
}

// TestSanitizeToolName verifies: illegal characters are replaced with '-'.
func TestSanitizeToolName(t *testing.T) {
	cases := map[string]string{
		"read":      "read",
		"bash":      "bash",
		"a/b":       "a-b",
		"..":        "--",
		"":          "tool",
		"tool-name": "tool-name",
	}
	for in, want := range cases {
		if got := sanitizeToolName(in); got != want {
			t.Errorf("sanitizeToolName(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestSpillToDiskBasic: write content to a temp file and read it back.
func TestSpillToDiskBasic(t *testing.T) {
	path, err := spillToDisk("read", "line1\nline2\n")
	if err != nil {
		t.Fatalf("spillToDisk: %v", err)
	}
	if path == "" {
		t.Fatal("should return non-empty path")
	}
	defer os.Remove(path)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back spill: %v", err)
	}
	if string(data) != "line1\nline2\n" {
		t.Errorf("spill content = %q", string(data))
	}
	if !strings.Contains(path, "read") {
		t.Errorf("spill filename should contain tool name, got %q", path)
	}
}

// TestSpillFallbackRuneAware verifies: when spilling fails, the fallback truncation does not
// split a multi-byte UTF-8 rune.
func TestSpillFallbackRuneAware(t *testing.T) {
	t.Setenv("TMPDIR", "/nonexistent-tmpdir-for-creator-agent-test")
	info := ToolInfo{Name: "t", MaxResultChars: 5}
	// "你好世界" is 4 CJK runes; limit 5 means fallback should return all 4 runes cleanly.
	r := ToolResult{Content: "你好世界"}
	out := maybeSpillResult(info, r)
	if out.Content == r.Content {
		t.Fatal("expected fallback truncation")
	}
	if strings.Contains(out.Content, "\ufffd") {
		t.Errorf("fallback split a rune: %q", out.Content)
	}
	// Should contain the full 4 runes plus the failure notice.
	if !strings.HasPrefix(out.Content, "你好世界") {
		t.Errorf("fallback should preserve whole runes, got %q", out.Content)
	}
}

// TestSpillToDiskSanitizesName: tool name special characters are sanitized (prevents path injection).
func TestSpillToDiskSanitizesName(t *testing.T) {
	path, err := spillToDisk("../evil", "x")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(path)
	if strings.Contains(path, "..") {
		t.Errorf("tool name should be sanitized in filename, got %q", path)
	}
}
