package core

// EstimateChars sums the byte length of message content, reasoning, and tool-call
// inputs across all messages — a rough proxy for context size without a tokenizer.
//
// Shared by MicroCompact (structural rewrite trigger) and Summarization (model
// compaction trigger) so both layers use one consistent size metric.
func EstimateChars(msgs []Message) int {
	n := 0
	for _, m := range msgs {
		n += len(m.Content)
		n += len(m.Reasoning)
		for _, tc := range m.ToolCalls {
			n += len(tc.Input)
		}
	}
	return n
}

// EstimateTokens returns a language-aware token estimate.
//
// Previous implementation used a flat chars/3 ratio, which is reasonable for ASCII English but
// severely underestimates CJK text (where one character maps to ~1 token in BPE tokenizers like
// tiktoken/cl100k). This improved heuristic walks all message text rune-by-rune, tallying CJK
// runes (~1 token each) vs non-CJK runes (~4 chars/token), then combines them into one estimate:
//
//	tokens ≈ cjkRunes + nonCJKRunes / 4
//
// Tallying across all messages before dividing avoids the precision loss of per-field integer
// division. The estimate is intentionally conservative (slightly over-counts) so compaction
// triggers a bit earlier rather than too late. Only used for compaction-threshold decisions.
func EstimateTokens(msgs []Message) int {
	cjk := 0
	nonCJK := 0
	count := func(s string) {
		for _, r := range s {
			if isCJKRune(r) {
				cjk++
			} else {
				nonCJK++
			}
		}
	}
	for _, m := range msgs {
		count(m.Content)
		count(m.Reasoning)
		for _, tc := range m.ToolCalls {
			count(string(tc.Input))
		}
	}
	return cjk + nonCJK/4
}

// isCJKRune reports whether r is a CJK ideograph or full-width punctuation (high token density in
// BPE tokenizers: ~1 token per character vs ~4 chars/token for ASCII).
func isCJKRune(r rune) bool {
	switch {
	case r >= 0x4E00 && r <= 0x9FFF: // CJK Unified Ideographs
		return true
	case r >= 0x3400 && r <= 0x4DBF: // CJK Extension A
		return true
	case r >= 0x20000 && r <= 0x2A6DF: // CJK Extension B
		return true
	case r >= 0x2A700 && r <= 0x2B73F: // CJK Extension C
		return true
	case r >= 0x2B740 && r <= 0x2B81F: // CJK Extension D
		return true
	case r >= 0x2B820 && r <= 0x2CEAF: // CJK Extension E
		return true
	case r >= 0x2CEB0 && r <= 0x2EBEF: // CJK Extension F
		return true
	case r >= 0x30000 && r <= 0x3134F: // CJK Extension G
		return true
	case r >= 0x3040 && r <= 0x30FF: // Hiragana + Katakana (Japanese)
		return true
	case r >= 0xAC00 && r <= 0xD7AF: // Hangul Syllables (Korean)
		return true
	case r >= 0xF900 && r <= 0xFAFF: // CJK Compatibility Ideographs
		return true
	case r >= 0xFE30 && r <= 0xFE4F: // CJK Compatibility Forms
		return true
	case r >= 0xFF00 && r <= 0xFFEF: // Fullwidth Forms (fullwidth punctuation/letters)
		return true
	case r >= 0x3000 && r <= 0x303F: // CJK Symbols and Punctuation
		return true
	case r >= 0x2F00 && r <= 0x2FDF: // Kangxi Radicals
		return true
	case r >= 0x2E80 && r <= 0x2EFF: // CJK Radicals Supplement
		return true
	case r >= 0x31C0 && r <= 0x31EF: // CJK Strokes
		return true
	case r >= 0x3200 && r <= 0x32FF: // Enclosed CJK Letters and Months
		return true
	case r >= 0x3300 && r <= 0x33FF: // CJK Compatibility block
		return true
	}
	return false
}

// PartitionForCompact splits message history into (toCompress, recent) while preserving
// assistant(toolCalls) and tool result pairings.
//
// Most providers (OpenAI, Anthropic, etc.) require role=tool messages to immediately follow the
// assistant(toolCalls) that issued them, and every ToolCall.ID must have a matching result.
// Cutting at a fixed count may leave an assistant in the compressed segment while orphaning its
// tool results in recent, causing provider errors like "tool result without preceding tool_call".
//
// Rules:
//  1. Coarse cut at len(rest)-keepRecent;
//  2. If the first message of recent is a tool result, expand the boundary backward to include
//     the corresponding assistant(toolCalls) (the loop guarantees tool results follow their assistant,
//     so stepping back to a non-tool message fully captures the assistant and all its results);
//  3. keepRecent is a target, not a hard constraint: extra messages may be retained to preserve pairings.
//
// rest should already have the leading system message removed (system is not compressed).
// When len(rest) <= keepRecent, returns (nil, rest).
func PartitionForCompact(rest []Message, keepRecent int) (toCompress, recent []Message) {
	if keepRecent < 0 {
		keepRecent = 0
	}
	if len(rest) <= keepRecent {
		return nil, rest
	}
	cut := len(rest) - keepRecent
	// Step the boundary back while the first message of recent is a tool result, to keep it paired
	// with its assistant(toolCalls). The cut < len(rest) guard avoids indexing past the end when
	// keepRecent == 0 (cut == len(rest), recent is empty, so there is nothing to orphan).
	for cut > 0 && cut < len(rest) && rest[cut].Role == RoleTool {
		cut--
	}
	return rest[:cut], rest[cut:]
}
