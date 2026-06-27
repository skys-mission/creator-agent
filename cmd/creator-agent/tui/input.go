package tui

import (
	"github.com/clipperhouse/uax29/v2/graphemes"
)

// inputBuffer holds the input as a rune slice and a rune-offset cursor.
type inputBuffer struct {
	runes []rune // raw text, may contain '\n' inserted via Alt+Enter
	cur   int    // cursor rune offset into runes (0..len(runes))
	width int    // render width (for cursor line/col computation)
}

// Value returns the input text as a string.
func (b *inputBuffer) Value() string { return string(b.runes) }

// SetValue replaces the text and moves the cursor to the end.
func (b *inputBuffer) SetValue(s string) {
	b.runes = []rune(s)
	b.cur = len(b.runes)
}

// SetWidth sets the render width used for cursor (line, col) computation.
func (b *inputBuffer) SetWidth(w int) {
	if w < 4 {
		w = 4
	}
	b.width = w
}

// InsertString inserts s at the cursor.
func (b *inputBuffer) InsertString(s string) {
	if s == "" {
		return
	}
	ins := []rune(s)
	tail := append([]rune(nil), b.runes[b.cur:]...)
	b.runes = append(b.runes[:b.cur], ins...)
	b.runes = append(b.runes, tail...)
	b.cur += len(ins)
}

// InsertRune inserts a single rune at the cursor.
func (b *inputBuffer) InsertRune(r rune) {
	tail := append([]rune(nil), b.runes[b.cur:]...)
	b.runes = append(b.runes[:b.cur], r)
	b.runes = append(b.runes, tail...)
	b.cur++
}

// Backspace deletes the rune before the cursor.
func (b *inputBuffer) Backspace() {
	if b.cur == 0 {
		return
	}
	b.runes = append(b.runes[:b.cur-1], b.runes[b.cur:]...)
	b.cur--
}

// Delete deletes the rune at the cursor.
func (b *inputBuffer) Delete() {
	if b.cur >= len(b.runes) {
		return
	}
	b.runes = append(b.runes[:b.cur], b.runes[b.cur+1:]...)
}

// CursorLeft / CursorRight move the cursor by one rune.
func (b *inputBuffer) CursorLeft() {
	if b.cur > 0 {
		b.cur--
	}
}

func (b *inputBuffer) CursorRight() {
	if b.cur < len(b.runes) {
		b.cur++
	}
}

// CursorStart / CursorEnd move to the beginning / end of the buffer.
func (b *inputBuffer) CursorStart() { b.cur = 0 }
func (b *inputBuffer) CursorEnd()   { b.cur = len(b.runes) }

// WordLeft deletes back to the previous whitespace boundary (Ctrl+W).
func (b *inputBuffer) WordLeft() {
	i := b.cur
	for i > 0 && isSpace(b.runes[i-1]) {
		i--
	}
	for i > 0 && !isSpace(b.runes[i-1]) {
		i--
	}
	b.runes = append(b.runes[:i], b.runes[b.cur:]...)
	b.cur = i
}

func isSpace(r rune) bool { return r == ' ' || r == '\t' || r == '\n' }

// cursorPos returns the (visualLine, visualCol) of the cursor for rendering. Both are in DISPLAY
// width units (so a CJK rune counts as 2), matching how drawTextRaw advances cells. Wrapping is
// recomputed the same way (a wide grapheme cluster that does not fit the remaining row width wraps
// to the next line), so the cursor lands exactly where the cluster will be drawn. Iterates grapheme
// clusters (not runes) so ZWJ emoji sequences are measured as a single unit with width 2.
func (b *inputBuffer) cursorPos() (line, col int) {
	if b.width <= 0 {
		b.width = 80
	}
	// Find the start of the last line (after the last '\n' before cursor).
	lastLineStart := 0
	visLine := 0
	for i := 0; i < b.cur; i++ {
		if b.runes[i] == '\n' {
			visLine++
			lastLineStart = i + 1
		}
	}
	lastLine := string(b.runes[lastLineStart:b.cur])
	visCol := 0
	iter := graphemes.FromString(lastLine)
	for iter.Next() {
		cluster := iter.Value()
		cw := strW(cluster)
		if cw <= 0 {
			continue
		}
		if visCol+cw > b.width {
			visLine++
			visCol = 0
		}
		visCol += cw
	}
	return visLine, visCol
}

func runeCount(s string) int { return len([]rune(s)) }

func runeLen(s string) (int, int) {
	n := runeCount(s)
	return n, n
}

// runesDisplayWidth returns the sum of display widths of the given runes (uses strW on the
// converted string, which iterates grapheme clusters so ZWJ emoji sequences are measured as a
// single unit with width 2).
func runesDisplayWidth(rs []rune) int {
	return strW(string(rs))
}

// wrappedLineCount returns the number of visual lines a logical line occupies when wrapped at
// width, accounting for wide grapheme clusters that don't fit the remaining width (they wrap whole).
// Iterates grapheme clusters (not runes) so ZWJ emoji sequences are measured as a single unit.
func wrappedLineCount(line string, width int) int {
	if width <= 0 {
		return 1
	}
	if line == "" {
		return 1
	}
	visLine := 1
	visCol := 0
	iter := graphemes.FromString(line)
	for iter.Next() {
		cluster := iter.Value()
		cw := strW(cluster)
		if cw <= 0 {
			continue
		}
		if visCol+cw > width {
			visLine++
			visCol = 0
		}
		visCol += cw
	}
	return visLine
}
