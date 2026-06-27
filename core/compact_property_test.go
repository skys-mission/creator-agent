package core

// compact_property_test.go verifies PartitionForCompact invariants for tool_call pairings,
// and SessionStore behavior under large histories (hundreds of turns).
//
// Key invariant: the recent slice after compact must not start with RoleTool,
// otherwise the tool result is orphaned (no preceding assistant(toolCalls)), and the provider
// will report "tool result without preceding tool_call".

import (
	"encoding/json"
	"strings"
	"testing"
)

// makePairedHistory builds N turns of user -> assistant(toolCalls) -> tool result.
// Each turn: user + assistant(1 toolCall) + 1 tool result.
func makePairedHistory(n int) []Message {
	out := make([]Message, 0, n*3)
	for i := 0; i < n; i++ {
		out = append(out, UserMessage("turn"))
		out = append(out, AssistantMessage("ok", ToolCall{ID: "c" + itoaCompact(i), Name: "read", Input: json.RawMessage("{}")}))
		out = append(out, ToolMessage("result", "c"+itoaCompact(i), "read"))
	}
	return out
}

func itoaCompact(i int) string {
	// Simple itoa (avoid importing strconv just for this).
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}

// TestPartitionForCompactNeverOrphansToolResult checks that across various keepRecent values,
// the recent slice never starts with RoleTool (which would orphan a tool result).
func TestPartitionForCompactNeverOrphansToolResult(t *testing.T) {
	hist := makePairedHistory(20) // 60 messages (20 turns x 3)
	for keepRecent := 1; keepRecent <= 30; keepRecent++ {
		_, recent := PartitionForCompact(hist, keepRecent)
		if len(recent) == 0 {
			continue
		}
		if recent[0].Role == RoleTool {
			t.Errorf("keepRecent=%d: recent starts with orphaned tool result", keepRecent)
		}
	}
}

// TestPartitionForCompactKeepsReasonable verifies that pairing expansion does not explode the recent slice.
func TestPartitionForCompactKeepsReasonable(t *testing.T) {
	hist := makePairedHistory(10) // 30 messages
	for keepRecent := 3; keepRecent <= 15; keepRecent++ {
		_, recent := PartitionForCompact(hist, keepRecent)
		// Expansion may add 1-2 messages (stepping back to the assistant), but should not grow wildly.
		if len(recent) > keepRecent+3 {
			t.Errorf("keepRecent=%d: recent=%d, expected keepRecent~keepRecent+3", keepRecent, len(recent))
		}
	}
}

// TestPartitionForCompactHistShorterThanKeep verifies: when history is shorter than keepRecent, everything is kept.
func TestPartitionForCompactHistShorterThanKeep(t *testing.T) {
	hist := makePairedHistory(2) // 6 messages
	toCompress, recent := PartitionForCompact(hist, 10)
	if toCompress != nil {
		t.Errorf("short history should not compress, got %d to compress", len(toCompress))
	}
	if len(recent) != len(hist) {
		t.Errorf("short history: recent should be all, got %d want %d", len(recent), len(hist))
	}
}

// ===== SessionStore large-history stress tests =====

// TestJSONFileStoreLargeHistoryRoundTrip verifies 200-turn history (600 messages) survives a write-read round trip.
func TestJSONFileStoreLargeHistoryRoundTrip(t *testing.T) {
	dir := t.TempDir()
	s := &JSONFileStore{Dir: dir}
	hist := makePairedHistory(200) // 600 messages

	if err := s.Save("big", hist); err != nil {
		t.Fatalf("Save large history: %v", err)
	}
	got, err := s.Load("big")
	if err != nil {
		t.Fatalf("Load large history: %v", err)
	}
	if len(got) != len(hist) {
		t.Fatalf("round-trip length: got %d want %d", len(got), len(hist))
	}
	// Spot-check first/last and a middle tool result's ToolCallID.
	if got[0].Content != "turn" || got[len(got)-1].Role != RoleTool {
		t.Errorf("round-trip content/role mismatch")
	}
	midTool := got[2] // first turn's tool result
	if midTool.Role != RoleTool || midTool.ToolCallID != "c0" {
		t.Errorf("tool result ToolCallID lost: %+v", midTool)
	}
}

// BenchmarkJSONFileStoreSaveLarge measures save performance (serialization + atomic write) for large histories.
func BenchmarkJSONFileStoreSaveLarge(b *testing.B) {
	dir := b.TempDir()
	s := &JSONFileStore{Dir: dir}
	hist := makePairedHistory(200)
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if err := s.Save("bench", hist); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkJSONFileStoreLoadLarge measures load performance (deserialization) for large histories.
func BenchmarkJSONFileStoreLoadLarge(b *testing.B) {
	dir := b.TempDir()
	s := &JSONFileStore{Dir: dir}
	hist := makePairedHistory(200)
	if err := s.Save("bench", hist); err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := s.Load("bench"); err != nil {
			b.Fatal(err)
		}
	}
}

// Prevent unused import (strings is not directly used by makePairedHistory, but kept for future expansion).
var _ = strings.TrimSpace
