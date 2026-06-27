package main

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/skys-mission/creator-agent/core"
)

// headlessModeNote tells the model the constraints of non-interactive headless mode.
// Injected before the user prompt to avoid the model repeatedly attempting write operations that will be denied (wasting turns + a bunch of ✗).
const headlessModeNote = "[context: running in non-interactive headless mode. " +
	"Write operations (write/edit/bash) are blocked here — they cannot be approved. " +
	"Answer directly, or use only read-only tools (read/grep/glob). " +
	"If a write is truly required, tell the user to run in interactive mode.]\n\n"

// runHeadless executes a single prompt and prints the event stream, then exits (no multi-turn memory).
func runHeadless(ctx context.Context, ag core.Agent, prompt string) {
	runHeadlessIO(ctx, ag, prompt, os.Stdout)
}

func runHeadlessIO(ctx context.Context, ag core.Agent, prompt string, out io.Writer) {
	// Inject headless constraint so the model knows write operations are unavailable from the start, avoiding wasted turns.
	events, err := ag.Stream(ctx, core.StreamInput{Messages: []core.Message{core.UserMessage(headlessModeNote + prompt)}})
	if err != nil {
		die(err)
	}
	printEventsTo(events, out)
	fmt.Fprintln(out)
}
