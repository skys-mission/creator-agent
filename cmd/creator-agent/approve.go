package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/skys-mission/creator-agent/core/middlewares"
)

// replApprover is the REPL mode write-operation approver: interactive y/a/n prompt, session-level remember.
//
// Behavior (middlewares.AskResolver signature):
//   - Tool already in session allow-set (user previously chose "a") -> direct allow
//   - Otherwise prompt for input:
//   - "y" / "yes" -> allow this time
//   - "a" / "always" -> allow + add to session allow-set (subsequent same-name tools skip prompt)
//   - "n" / other / EOF -> deny
type replApprover struct {
	in  *bufio.Reader // approval response input (not readline, simple y/a/n)
	out io.Writer

	allowSet *middlewares.AllowSet // session-level approved invocations (shared type with the TUI approver)
}

// newReplApprover constructs. in/out are injectable (for testing).
func newReplApprover(in io.Reader, out io.Writer) *replApprover {
	return &replApprover{
		in:       bufio.NewReader(in),
		out:      out,
		allowSet: middlewares.NewAllowSet(),
	}
}

// approve implements middlewares.AskResolver.
// Note: dumb REPL uses bufio blocking read, does not respond to ctx (non-TTY test path);
// when ctx is canceled the process exits as a whole.
func (a *replApprover) approve(_ context.Context, toolName, input string) bool {
	if a.allowSet.Allowed(toolName, input) {
		return true
	}

	// prompt for approval
	fmt.Fprintf(a.out, "\n%s tool %q wants to execute: %s\n",
		paint(cYellow, "⚡ Approval"), toolName, truncateForPrompt(input))
	fmt.Fprintf(a.out, "%s [y]allow once  %s [a]allow this session  %s [n]deny (enter=n): ",
		paint(cGreen, "y"), paint(cCyan, "a"), paint(cRed, "n"))

	line, err := a.in.ReadString('\n')
	if err != nil && line == "" {
		// EOF or read error -> safe-side deny
		fmt.Fprintln(a.out, paint(cRed, "(denied)"))
		return false
	}
	resp := strings.ToLower(strings.TrimSpace(line))

	switch resp {
	case "y", "yes":
		fmt.Fprintln(a.out, paint(cGreen, "(allowed once)"))
		return true
	case "a", "always":
		if toolName == "write" || toolName == "edit" {
			a.allowSet.RememberWriteEdit()
		} else {
			a.allowSet.RememberExact(toolName, input)
		}
		fmt.Fprintf(a.out, "%s\n", paint(cGreen, "(allowed for this session)"))
		return true
	default:
		fmt.Fprintln(a.out, paint(cRed, "(denied)"))
		return false
	}
}

// truncateForPrompt summarizes input into a single line for the approval prompt (avoids flooding).
func truncateForPrompt(input string) string {
	s := strings.TrimSpace(input)
	// collapse to single line
	s = strings.ReplaceAll(s, "\n", " ⏎ ")
	if len(s) > 100 {
		s = s[:100] + "…"
	}
	if s == "" {
		s = "(no args)"
	}
	return s
}

// headlessApprover is the headless mode approver: all asks are denied (non-interactive, safety-first).
// Users whitelist specific write operations via config allow rules.
type headlessApprover struct{}

// approve implements middlewares.AskResolver: fixed deny.
func (headlessApprover) approve(context.Context, string, string) bool { return false }

// compile-time check that both implement AskResolver.
var _ middlewares.AskResolver = (*replApprover)(nil).approve
var _ middlewares.AskResolver = headlessApprover{}.approve
