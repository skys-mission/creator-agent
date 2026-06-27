package core

import "testing"

// noOrphanTool checks that every role=tool message's ToolCallID matches a ToolCall.ID earlier in the sequence.
func noOrphanTool(msgs []Message) bool {
	seen := map[string]bool{}
	for _, m := range msgs {
		for _, tc := range m.ToolCalls {
			seen[tc.ID] = true
		}
		if m.Role == RoleTool && !seen[m.ToolCallID] {
			return false
		}
	}
	return true
}

func TestPartitionForCompactShortHistory(t *testing.T) {
	msgs := []Message{UserMessage("a"), UserMessage("b")}
	tc, rec := PartitionForCompact(msgs, 6)
	if tc != nil {
		t.Errorf("short history: toCompress should be nil, got %d msgs", len(tc))
	}
	if len(rec) != 2 {
		t.Errorf("recent should be all msgs, got %d", len(rec))
	}
}

func TestPartitionForCompactKeepRecentZero(t *testing.T) {
	// keepRecent == 0 must compress everything and keep nothing recent, without panicking
	// (cut == len(rest); the boundary-walk must not index past the end).
	msgs := []Message{
		UserMessage("q1"),
		AssistantMessage("", ToolCall{ID: "a", Name: "read"}),
		ToolMessage("res", "a", "read"),
	}
	toCompress, recent := PartitionForCompact(msgs, 0)
	if len(toCompress) != len(msgs) {
		t.Errorf("keepRecent=0: toCompress should be all %d msgs, got %d", len(msgs), len(toCompress))
	}
	if len(recent) != 0 {
		t.Errorf("keepRecent=0: recent should be empty, got %d", len(recent))
	}
}

func TestPartitionForCompactExtendsForOrphanedTool(t *testing.T) {
	// Sequence: u1, asst(toolA), tool(resA), u2, asst(toolB), tool(resB), asst(final)
	msgs := []Message{
		UserMessage("q1"),
		AssistantMessage("", ToolCall{ID: "c1", Name: "read"}),
		ToolMessage("resA", "c1", "read"),
		UserMessage("q2"),
		AssistantMessage("", ToolCall{ID: "c2", Name: "grep"}),
		ToolMessage("resB", "c2", "grep"),
		AssistantMessage("final answer"),
	}
	// keepRecent=2 -> coarse cut at 7-2=5, recent=msgs[5:]=[tool(resB), asst(final)],
	// msgs[5]=tool -> expand cut=4, msgs[4]=asst(toolCalls) stops -> recent=msgs[4:]
	toCompress, recent := PartitionForCompact(msgs, 2)

	if len(recent) == 0 || recent[0].Role == RoleTool {
		t.Fatalf("recent must not start with orphan tool: %+v", recent)
	}
	if len(recent[0].ToolCalls) == 0 {
		t.Fatalf("recent[0] should be the assistant owning the rescued tool result: %+v", recent[0])
	}
	if !noOrphanTool(recent) {
		t.Errorf("recent has orphan tool result: %+v", recent)
	}
	// toCompress tail must not be an assistant with toolCalls whose results landed in recent.
	if len(toCompress) == 0 {
		t.Fatal("expected something to compress")
	}
	last := toCompress[len(toCompress)-1]
	if len(last.ToolCalls) > 0 {
		t.Errorf("toCompress tail is assistant with pending toolCalls (result cut away): %+v", last)
	}
	if !noOrphanTool(toCompress) {
		t.Errorf("toCompress has orphan tool result: %+v", toCompress)
	}
}

func TestPartitionForCompactNoExtensionWhenBoundaryClean(t *testing.T) {
	// Cut point lands exactly on an assistant(text) boundary; no expansion needed.
	msgs := []Message{
		UserMessage("q1"),
		AssistantMessage("", ToolCall{ID: "c1", Name: "read"}),
		ToolMessage("resA", "c1", "read"),
		UserMessage("q2"),
		AssistantMessage("answer"),
	}
	// keepRecent=2 -> cut=5-2=3, recent=msgs[3:]=[u2, asst(answer)], msgs[3]=user not tool, no expansion.
	toCompress, recent := PartitionForCompact(msgs, 2)
	if len(recent) != 2 {
		t.Errorf("clean boundary: recent should be 2, got %d", len(recent))
	}
	if len(toCompress) != 3 {
		t.Errorf("clean boundary: toCompress should be 3, got %d", len(toCompress))
	}
	if !noOrphanTool(recent) || !noOrphanTool(toCompress) {
		t.Error("orphan tool in clean-boundary case")
	}
}

// ===== EstimateTokens (CJK-aware heuristic) =====

func TestEstimateTokensASCII(t *testing.T) {
	// 40 ASCII chars -> 40/4 = 10 tokens.
	msgs := []Message{UserMessage("abcdefghijklmnopqrstuvwxyzabcdefghijklmn")}
	got := EstimateTokens(msgs)
	if got != 10 {
		t.Errorf("EstimateTokens(ASCII 40 chars) = %d, want 10", got)
	}
}

func TestEstimateTokensCJK(t *testing.T) {
	// 12 CJK characters (incl. fullwidth punctuation) -> 12 tokens (1 token/char).
	msgs := []Message{UserMessage("你好世界，这是一个测试。")}
	got := EstimateTokens(msgs)
	if got != 12 {
		t.Errorf("EstimateTokens(CJK 12 chars) = %d, want 12", got)
	}
}

func TestEstimateTokensMixed(t *testing.T) {
	// "hello 你好" = 6 non-CJK (h,e,l,l,o,space) + 2 CJK.
	// Expected: 2 (CJK) + 6/4 = 2 + 1 = 3 tokens.
	msgs := []Message{UserMessage("hello 你好")}
	got := EstimateTokens(msgs)
	if got != 3 {
		t.Errorf("EstimateTokens(mixed) = %d, want 3", got)
	}
}

func TestEstimateTokensEmpty(t *testing.T) {
	if got := EstimateTokens(nil); got != 0 {
		t.Errorf("EstimateTokens(nil) = %d, want 0", got)
	}
}

func TestEstimateTokensMultipleFields(t *testing.T) {
	msgs := []Message{
		{
			Role:      RoleAssistant,
			Content:   "aaaaaaaa",                          // 8 non-CJK -> 2 tokens
			Reasoning: "bbbb",                              // 4 non-CJK -> 1 token
			ToolCalls: []ToolCall{{Input: []byte("cccc")}}, // 4 non-CJK -> 1 token
		},
	}
	if got := EstimateTokens(msgs); got != 4 {
		t.Errorf("EstimateTokens(multi-field) = %d, want 4", got)
	}
}

func TestIsCJKRune(t *testing.T) {
	cases := []struct {
		r    rune
		want bool
	}{
		{'你', true},  // CJK ideograph
		{'あ', true},  // Hiragana
		{'カ', true},  // Katakana
		{'한', true},  // Hangul
		{'，', true},  // fullwidth comma (U+FF0C)
		{'。', true},  // CJK full stop (U+3002)
		{'a', false}, // ASCII
		{'Z', false}, // ASCII
		{' ', false}, // space
		{'é', false}, // Latin extended
	}
	for _, c := range cases {
		if got := isCJKRune(c.r); got != c.want {
			t.Errorf("isCJKRune(%q) = %v, want %v", c.r, got, c.want)
		}
	}
}
