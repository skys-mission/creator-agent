package tui

// highlight_test.go verifies the heuristic code tokenizer classifies common tokens correctly.

import (
	"strings"
	"testing"
)

func TestHighlightCodeLine(t *testing.T) {
	cases := []struct {
		name  string
		line  string
		check func(runs []styledRun) bool
	}{
		{"slash-comment", "// hello", func(r []styledRun) bool {
			return len(r) == 1 && r[0].style == styleSyntaxComment() && r[0].text == "// hello"
		}},
		{"hash-comment", "  # a comment", func(r []styledRun) bool {
			return len(r) >= 1 && r[0].style == styleSyntaxComment()
		}},
		{"string", `x = "hi"`, func(r []styledRun) bool {
			for _, run := range r {
				if run.text == `"hi"` && run.style == styleSyntaxString() {
					return true
				}
			}
			return false
		}},
		{"keyword", "if x {", func(r []styledRun) bool {
			for _, run := range r {
				if run.text == "if" && run.style == styleSyntaxKeyword() {
					return true
				}
			}
			return false
		}},
		{"number", "x = 42", func(r []styledRun) bool {
			for _, run := range r {
				if run.text == "42" && run.style == styleSyntaxNumber() {
					return true
				}
			}
			return false
		}},
		{"hex-number", "v := 0xff", func(r []styledRun) bool {
			for _, run := range r {
				if run.text == "0xff" && run.style == styleSyntaxNumber() {
					return true
				}
			}
			return false
		}},
		{"func-call", "fmt.Println(x)", func(r []styledRun) bool {
			for _, run := range r {
				if run.text == "Println" && run.style == styleSyntaxFunction() {
					return true
				}
			}
			return false
		}},
		{"plain-identifier", "foo := 1", func(r []styledRun) bool {
			// "foo" is a plain identifier: not classified as a keyword/string/number/function.
			// (It may merge with adjacent punctuation because both share the default text color.)
			for _, run := range r {
				if strings.Contains(run.text, "foo") {
					return run.style != styleSyntaxKeyword() && run.style != styleSyntaxString() &&
						run.style != styleSyntaxNumber() && run.style != styleSyntaxFunction()
				}
			}
			return false
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			runs := highlightCodeLine(c.line)
			if !c.check(runs) {
				t.Errorf("highlightCodeLine(%q) = %+v; check failed", c.line, runs)
			}
		})
	}
}

// TestHighlightEmptyLine ensures a blank code line yields a single default run (never empty, so the
// caller does not need a special case).
func TestHighlightEmptyLine(t *testing.T) {
	runs := highlightCodeLine("")
	if len(runs) != 1 {
		t.Fatalf("empty line should yield 1 run, got %d: %+v", len(runs), runs)
	}
}
