package main

import (
	"context"
	"encoding/json"
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

// runHeadless executes a single prompt and prints the event stream, then exits with a finish-reason
// derived exit code (see headlessExitCode). jsonMode switches the output to a single structured JSON
// object for scripting instead of the human-friendly stream.
func runHeadless(ctx context.Context, ag core.Agent, prompt string, jsonMode bool) {
	code := runHeadlessIO(ctx, ag, prompt, os.Stdout, jsonMode)
	if code != 0 {
		exitWith(code)
	}
}

func runHeadlessIO(ctx context.Context, ag core.Agent, prompt string, out io.Writer, jsonMode bool) int {
	// Inject headless constraint so the model knows write operations are unavailable from the start, avoiding wasted turns.
	events, err := ag.Stream(ctx, core.StreamInput{Messages: []core.Message{core.UserMessage(headlessModeNote + prompt)}})
	if err != nil {
		if jsonMode {
			return emitHeadlessJSONError(out, err)
		}
		die(err)
	}
	if jsonMode {
		return emitHeadlessJSON(events, out)
	}
	res := printEventsTo(events, out)
	fmt.Fprintln(out)
	return headlessExitCode(res)
}

// headlessJSON is the machine-readable result of a headless run (printed once at the end with
// -json). It is intentionally flat and stable so scripts can depend on it.
type headlessJSON struct {
	Text         string             `json:"text"`
	FinishReason string             `json:"finish_reason"`
	Error        string             `json:"error,omitempty"`
	Tools        []headlessJSONTool `json:"tools,omitempty"`
	Usage        *core.Usage        `json:"usage,omitempty"`
}

type headlessJSONTool struct {
	Name  string `json:"name"`
	Error string `json:"error,omitempty"`
}

// emitHeadlessJSON consumes the stream, aggregates it into a headlessJSON, prints it, and returns
// the finish-reason exit code. Text and thinking deltas are concatenated; tool outcomes and usage
// are recorded so a caller gets one self-contained object.
func emitHeadlessJSON(events <-chan core.Event, out io.Writer) int {
	var doc headlessJSON
	var res streamResult
	var text []byte
	for ev := range events {
		switch e := ev.(type) {
		case core.TextEvent:
			text = append(text, e.Delta...)
		case core.ToolUseStartEvent:
			doc.Tools = append(doc.Tools, headlessJSONTool{Name: e.Name})
		case core.ToolResultEvent:
			if n := len(doc.Tools); n > 0 {
				switch {
				case e.Err != nil:
					doc.Tools[n-1].Error = e.Err.Error()
				case e.Result.IsError:
					doc.Tools[n-1].Error = e.Result.Content
				}
			}
		case core.UsageEvent:
			u := e.Usage
			doc.Usage = &u
		case core.FinishEvent:
			res.Reason = e.Reason
		case core.ErrorEvent:
			res.Err = e.Err
		}
	}
	doc.Text = string(text)
	doc.FinishReason = string(res.Reason)
	if res.Err != nil {
		doc.Error = res.Err.Error()
	}
	writeJSONLine(out, doc)
	return headlessExitCode(res)
}

// emitHeadlessJSONError prints a JSON object for the pre-stream failure path (Stream returned an
// error before producing any event) and returns exit code 1.
func emitHeadlessJSONError(out io.Writer, err error) int {
	writeJSONLine(out, headlessJSON{Error: err.Error()})
	return 1
}

func writeJSONLine(out io.Writer, v any) {
	enc := json.NewEncoder(out)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v) // Encode appends a newline; errors here are unactionable (stdout closed).
}
