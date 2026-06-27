package tui

// bench_test.go holds stable, pure-function hot-path benchmarks for the TUI render pipeline.
// These run with `make bench` and cover the per-frame work that dominates rendering cost:
// styledLine wrapping (called for every finalized message block each frame) and width-aware
// truncation. They have no Screen / App / TTY dependency so they stay deterministic.

import (
	"strings"
	"testing"
)

// asciiLine and cjkLine are representative single-run styledLines used across the wrap benches.
func asciiLine(n int) styledLine {
	var sl styledLine
	sl.appendRun(strings.Repeat("The quick brown fox jumps over the lazy dog. ", n/45+1), styleText())
	return sl
}

func cjkLine(n int) styledLine {
	var sl styledLine
	sl.appendRun(strings.Repeat("中文字符渲染宽度为两列，换行需按显示宽度断行。", n/20+1), styleText())
	return sl

}

// BenchmarkWrapStyledLineASCII measures wrapping a long ASCII styledLine to a typical terminal
// width (80 cols). This runs on every finalized message block during render.
func BenchmarkWrapStyledLineASCII(b *testing.B) {
	ln := asciiLine(500)
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = wrapStyledLine(ln, 80)
	}
}

// BenchmarkWrapStyledLineCJK measures the CJK/wide-glyph path (the costlier branch because each
// grapheme cluster is width-measured). Long CJK paragraphs are the worst case for wrap cost.
func BenchmarkWrapStyledLineCJK(b *testing.B) {
	ln := cjkLine(500)
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = wrapStyledLine(ln, 80)
	}
}

// BenchmarkTruncateStrW measures width-aware truncation, used for previews (error bars, tool
// output previews). Iterates grapheme clusters so it reflects the wide-glyph cost.
func BenchmarkTruncateStrW(b *testing.B) {
	s := strings.Repeat("中文测试文本abc", 50)
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = truncateStrW(s, 60)
	}
}
