package main

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/skys-mission/creator-agent/core"
)

// Color output (enabled on TTY, automatically disabled on pipes/redirects to avoid ANSI codes polluting pipe output).
var useColor bool

const (
	ansiReset = "\033[0m"
	cBold     = "\033[1m"
	cDim      = "\033[2m"
	cRed      = "\033[31m"
	cGreen    = "\033[32m"
	cYellow   = "\033[33m"
	cCyan     = "\033[36m"
)

// paint colors a string (returns unchanged when not on a TTY).
func paint(c, s string) string {
	if !useColor {
		return s
	}
	return c + s + ansiReset
}

// isTTY reports whether the file is a terminal (uses stat, no third-party dependency).
func isTTY(f *os.File) bool {
	info, err := f.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

// printEvents consumes the event stream and writes to stdout.
func printEvents(events <-chan core.Event) {
	printEventsTo(events, os.Stdout)
}

// printEventsTo consumes the event stream: streaming text + friendly (colored) tool display to out.
//
// Tool display format (one tool per line, avoids concurrent tool results squeezing onto one line):
//
//	⚙ read … ✓
//	⚙ bash … ✗ <error>
//
// Multiple concurrent tools each occupy their own line (Start prints newline+prefix, Result appends at the end of that line and prints newline),
// avoiding the readability issue of old ✓✗✗ piling up on one line.
func printEventsTo(events <-chan core.Event, out io.Writer) {
	firstText := true
	for ev := range events {
		switch e := ev.(type) {
		case core.TextEvent:
			if firstText {
				fmt.Fprintln(out)
				firstText = false
			}
			fmt.Fprint(out, e.Delta)
		case core.ToolUseStartEvent:
			// Tool start: new line, print "⚙ name …", no trailing newline (waits for Result to append).
			fmt.Fprintf(out, "\n  %s %s %s", paint(cCyan, "⚙"), paint(cCyan, e.Name), paint(cDim, "…"))
		case core.ToolResultEvent:
			// Tool result: appends after Start (same line), prints status + error detail, then newline.
			switch {
			case e.Err != nil:
				fmt.Fprintln(out, " "+paint(cRed, "✗ "+truncateErr(e.Err.Error())))
			case e.Result.IsError:
				msg := e.Result.Content
				if msg == "" {
					msg = "(no detail)"
				}
				fmt.Fprintln(out, " "+paint(cRed, "✗ "+truncateErr(msg)))
			default:
				fmt.Fprintln(out, " "+paint(cGreen, "✓"))
			}
		case core.FinishEvent:
			if e.Reason != "" && e.Reason != "stop" {
				fmt.Fprintf(out, "\n%s\n", paint(cYellow, "[finish: "+string(e.Reason)+"]"))
			}
		case core.ErrorEvent:
			// System error: classification hint + detail, goes to out (testable)
			fmt.Fprintf(out, "\n%s\n", paint(cRed, core.UserHint(e.Err)+truncateErr(e.Err.Error())))
		}
	}
}

// truncateErr truncates long error messages (>500 chars + …) to avoid flooding.
func truncateErr(s string) string {
	const max = 500
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}

// die prints the error to stderr and exits with code 1 (runs all registered cleanups first to prevent resource leaks).
func die(err error) {
	exitErr(err, 1)
}

// firstRunGuide generates the first-run guidance text: tells the user where the config file is, which line to change, and how to verify.
// Design principle ("treat users as beginners"): old text only said "edit and fill in api_key", without giving the specific line number or example,
// forcing the user to guess the format. Here we give the exact "open file → change line N → verify" steps.
func firstRunGuide(configPath string) string {
	var sb strings.Builder
	sb.WriteString(paint(cBold, "First run: config template generated\n"))
	sb.WriteString("\n")
	sb.WriteString(paint(cDim, "Config file: ") + configPath + "\n")
	sb.WriteString("\n")
	sb.WriteString("Next steps (three to get running):\n")
	sb.WriteString("  1. Open the file above in an editor\n")
	sb.WriteString("  2. Replace sk-xxx on this line with your real API key:\n")
	sb.WriteString(paint(cCyan, "       api_key = \"sk-xxx\"") + paint(cDim, "  ← replace with your key") + "\n")
	sb.WriteString("  3. Re-run this program\n")
	sb.WriteString("\n")
	sb.WriteString(paint(cDim, "Or skip the file and use an environment variable:\n"))
	sb.WriteString(paint(cCyan, "  export OPENAI_API_KEY=sk-yourkey\n"))
	sb.WriteString("\n")
	sb.WriteString(paint(cDim, "Defaults to DeepSeek endpoint. To switch to OpenAI / Tongyi / Zhipu / Kimi, etc., change default and the corresponding profile's base_url/model.\n"))
	return sb.String()
}
