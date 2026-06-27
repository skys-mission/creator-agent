package tui

// wrap_test.go verifies wrapStyledLine/wrapStyledLines: long lines wrap to the given display
// width with per-rune style preserved, wide runes never split, and wrap boundaries consume spaces.

import (
	"strings"
	"testing"
)

// flattenStyledLine returns the plain text of a styledLine (concatenation of run texts).
func flattenStyledLine(sl styledLine) string {
	var b strings.Builder
	for _, r := range sl.runs {
		b.WriteString(r.text)
	}
	return b.String()
}

// lineWidth returns the display width of a styledLine.
func lineWidth(sl styledLine) int {
	w := 0
	for _, r := range sl.runs {
		w += strW(r.text)
	}
	return w
}

func TestWrapStyledLineASCII(t *testing.T) {
	var sl styledLine
	sl.appendRun("abcdefghijklmn", styleAssistant())
	got := wrapStyledLine(sl, 5)
	// width 5: "abcde","fghij","klmn"
	if len(got) != 3 {
		t.Fatalf("got %d lines, want 3: %q", len(got), joinFlattened(got))
	}
	for i, ln := range got {
		if lw := lineWidth(ln); lw > 5 {
			t.Errorf("line %d width %d > 5: %q", i, lw, flattenStyledLine(ln))
		}
	}
	if joined := joinFlattened(got); joined != "abcdefghijklmn" {
		t.Errorf("content changed: %q", joined)
	}
}

func TestWrapStyledLineCJK(t *testing.T) {
	var sl styledLine
	// 4 CJK runes = 8 display cols. width 6: each rune is width-2, so line holds 3 runes (6 cols),
	// 4th rune wraps. Expected: ["中文字","测试"].
	sl.appendRun("中文字测试", styleAssistant())
	got := wrapStyledLine(sl, 6)
	if len(got) != 2 {
		t.Fatalf("got %d lines, want 2: %q", len(got), joinFlattened(got))
	}
	if w := lineWidth(got[0]); w != 6 {
		t.Errorf("line 0 width %d, want 6: %q", w, flattenStyledLine(got[0]))
	}
	if w := lineWidth(got[1]); w != 4 {
		t.Errorf("line 1 width %d, want 4: %q", w, flattenStyledLine(got[1]))
	}
}

func TestWrapStyledLineWideRuneNotSplit(t *testing.T) {
	var sl styledLine
	// width 3: a CJK rune (width 2) fits at cols 0-1; an ASCII at col 2; next CJK (needs 2) won't
	// fit -> wraps. "a你b好" => "a你b" (1+2+1=4? no). Let's use width 4: a=1,你=2,b=1 -> fills 4,
	// 好 (2) wraps.
	sl.appendRun("a你b好", styleAssistant())
	got := wrapStyledLine(sl, 4)
	// line 0: a你b = 4 cols; line 1: 好 = 2 cols.
	if len(got) != 2 {
		t.Fatalf("got %d lines, want 2: %q", len(got), joinFlattened(got))
	}
	if w := lineWidth(got[0]); w != 4 {
		t.Errorf("line 0 width %d, want 4: %q", w, flattenStyledLine(got[0]))
	}
}

func TestWrapStyledLinePreservesStyles(t *testing.T) {
	var sl styledLine
	sl.appendRun("ab", StyleDefault.Foreground(NewRGBColor(255, 0, 0)))
	sl.appendRun("cdef", StyleDefault.Foreground(NewRGBColor(0, 0, 255)))
	got := wrapStyledLine(sl, 3)
	// Collect (rune, fg) pairs and verify styles survive wrapping.
	type rc struct {
		r  rune
		fg Color
	}
	want := []rc{{'a', NewRGBColor(255, 0, 0)}, {'b', NewRGBColor(255, 0, 0)}, {'c', NewRGBColor(0, 0, 255)}, {'d', NewRGBColor(0, 0, 255)}, {'e', NewRGBColor(0, 0, 255)}, {'f', NewRGBColor(0, 0, 255)}}
	var got2 []rc
	for _, ln := range got {
		for _, run := range ln.runs {
			fg, _, _ := run.style.Decompose()
			for _, r := range run.text {
				got2 = append(got2, rc{r, fg})
			}
		}
	}
	if len(got2) != len(want) {
		t.Fatalf("rune count %d, want %d", len(got2), len(want))
	}
	for i := range want {
		if got2[i].r != want[i].r || got2[i].fg != want[i].fg {
			t.Errorf("rune %d: got %q/%v want %q/%v", i, got2[i].r, got2[i].fg, want[i].r, want[i].fg)
		}
	}
}

func TestWrapStyledLineSpaceConsumedAtWrap(t *testing.T) {
	var sl styledLine
	// "ab cdef" width 3: "ab " (3), then "cde","f". The space after ab is consumed at the wrap so
	// the next line starts with "cde" not " cde".
	sl.appendRun("ab cdef", styleAssistant())
	got := wrapStyledLine(sl, 3)
	// Flatten and check no line has a leading space (except possibly the first).
	for i, ln := range got {
		txt := flattenStyledLine(ln)
		if i > 0 && len(txt) > 0 && txt[0] == ' ' {
			t.Errorf("line %d has leading space after wrap: %q", i, txt)
		}
	}
}

func TestWrapStyledLineShortReturnsAsIs(t *testing.T) {
	var sl styledLine
	sl.appendRun("ab", styleAssistant())
	got := wrapStyledLine(sl, 100)
	if len(got) != 1 {
		t.Fatalf("got %d lines, want 1", len(got))
	}
}

func TestWrapStyledLineRuneWiderThanWidth(t *testing.T) {
	// A single CJK rune (width 2) with width 1: must still emit it (not infinite-loop) to avoid
	// dropping all CJK content in a 1-col surface.
	var sl styledLine
	sl.appendRun("你", styleAssistant())
	got := wrapStyledLine(sl, 1)
	if len(got) == 0 {
		t.Fatal("empty result for wide rune in narrow surface")
	}
	if flattenStyledLine(got[0]) != "你" {
		t.Errorf("expected 你 preserved, got %q", flattenStyledLine(got[0]))
	}
}

func joinFlattened(lines []styledLine) string {
	var b strings.Builder
	for _, ln := range lines {
		b.WriteString(flattenStyledLine(ln))
	}
	return b.String()
}
