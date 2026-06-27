package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"

	"github.com/ergochat/readline"

	"github.com/skys-mission/creator-agent/config"
	"github.com/skys-mission/creator-agent/core"
	"github.com/skys-mission/creator-agent/paths"
)

// lineReader abstracts line reading (TTY uses readline with history/editing; non-TTY uses bufio for easy testing).
type lineReader interface {
	ReadLine(prompt string) (string, error)
	Close() error
}

// plainReader is the non-TTY path (testing/pipes): bufio line read. prompt is printed verbatim to out.
type plainReader struct {
	r   *bufio.Reader
	out io.Writer
}

func (p *plainReader) ReadLine(prompt string) (string, error) {
	fmt.Fprint(p.out, prompt)
	line, err := p.r.ReadString('\n')
	if err != nil {
		return line, err
	}
	return strings.TrimSpace(line), nil
}

func (p *plainReader) Close() error { return nil }

// readlineReader is the TTY path: ergochat/readline with up/down history + line editing + Ctrl-R search.
type readlineReader struct {
	inst *readline.Instance
}

// newReadlineReader constructs a readline instance, history stored under the data dir (~/.creator).
func newReadlineReader() (*readlineReader, error) {
	histDir, err := paths.DataDir()
	if err != nil {
		return nil, err
	}
	historyFile := ""
	if err := os.MkdirAll(histDir, 0o755); err != nil {
		fmt.Fprintln(os.Stderr, "warn: readline history disabled:", err)
	} else {
		historyFile = filepath.Join(histDir, "history")
	}
	inst, err := readline.NewFromConfig(&readline.Config{
		Prompt:            paint(cCyan, "\n▶ "),
		HistoryFile:       historyFile,
		HistoryLimit:      500,
		HistorySearchFold: true,
	})
	if err != nil {
		return nil, err
	}
	return &readlineReader{inst: inst}, nil
}

func (r *readlineReader) ReadLine(prompt string) (string, error) {
	if prompt != "" {
		r.inst.SetPrompt(prompt)
	}
	line, err := r.inst.Readline()
	return strings.TrimSpace(line), err
}

func (r *readlineReader) Close() error {
	if r.inst != nil {
		return r.inst.Close()
	}
	return nil
}

// withSessionStore returns a JSONFileStore Option (disk persistence for multi-turn memory);
// falls back to MemoryStore if the directory is unavailable, without blocking startup.
func withSessionStore() core.Option {
	store, err := core.NewJSONFileStore("")
	if err != nil {
		fmt.Fprintln(os.Stderr, "warn: session persistence disabled:", err)
		return core.WithSessionStore(core.NewMemoryStore())
	}
	return core.WithSessionStore(store)
}

// runREPL starts interactive mode: multi-turn conversation, remembers context, supports /clear /exit /help.
func runREPL(ctx context.Context, ag core.Agent, prof config.Profile) {
	runREPLIO(ctx, ag, prof, os.Stdin, os.Stdout)
}

// runREPLIO is the REPL core. in/out are injectable (for testing).
//
// Input path: if in is a TTY (*os.File and isTTY) -> use readline (history/editing/Ctrl-R);
// otherwise -> bufio (testing/pipes, zero dependencies). This keeps all 8 runREPLIO tests green.
func runREPLIO(ctx context.Context, ag core.Agent, prof config.Profile, in io.Reader, out io.Writer) {
	// Each REPL launch is a fresh session (a generated "ses_..." id), mirroring the TUI's default so
	// behavior is consistent across modes. History is reused only within a single launch (multi-turn).
	sessionID := core.GenerateSessionID()

	var reader lineReader
	if f, ok := in.(*os.File); ok && isTTY(f) {
		if rl, err := newReadlineReader(); err == nil {
			reader = rl
			defer rl.Close()
		}
	}
	if reader == nil {
		reader = &plainReader{r: bufio.NewReader(in), out: out}
	}

	fmt.Fprintf(out, "%s · %s @ %s\n", paint(cBold, "creator-agent"), paint(cCyan, prof.Model), paint(cDim, config.HostOf(prof.BaseURL)))
	fmt.Fprintln(out, "Interactive mode: type a question to start (multi-turn, remembers context). /help for commands, /exit to quit, Ctrl+C to interrupt current.")

	prompt := paint(cCyan, "\n▶ ")
	for {
		line, err := reader.ReadLine(prompt)
		if err == io.EOF {
			fmt.Fprintln(out)
			return
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			return
		}
		if line == "" {
			continue
		}

		switch line {
		case "/exit", "/quit":
			return
		case "/clear":
			ag.ClearSession(sessionID)
			fmt.Fprintln(out, "(conversation cleared)")
			continue
		case "/help":
			fmt.Fprintln(out, "  /clear  clear conversation history")
			fmt.Fprintln(out, "  /exit   exit")
			fmt.Fprintln(out, "  other input is conversation (agent remembers context within this turn)")
			continue
		}

		// Each turn uses an independent interrupt-aware context: Ctrl+C cancels only the
		// current Stream, not the next turn. We must NOT use signal.NotifyContext here,
		// because its stop() function cancels the context as a side effect (so calling it
		// immediately after Stream() returns would cancel the running agent loop before any
		// event reaches the consumer).
		//
		// Instead, derive a cancelable child of the global ctx and forward os.Interrupt to
		// it via a dedicated signal channel. The listener goroutine exits when the turn ends
		// (turnDone closed) regardless of whether an interrupt arrived, so signal.Reset
		// restores default behavior between turns.
		turnCtx, turnCancel := context.WithCancel(ctx)
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, os.Interrupt)
		turnDone := make(chan struct{})
		go func() {
			select {
			case <-sigCh:
				turnCancel()
			case <-turnDone:
			}
		}()
		events, err := ag.Stream(turnCtx, core.StreamInput{
			SessionID: sessionID,
			Messages:  []core.Message{core.UserMessage(line)},
		})
		if err != nil {
			// Use the same classification hint as printEventsTo (core.UserHint) for consistent error display style
			fmt.Fprintln(os.Stderr, core.UserHint(err)+err.Error())
		} else {
			printEventsTo(events, out)
		}
		// Stop this turn's interrupt listener and release resources. signal.Stop only
		// unregisters (it does not cancel ctx), so the agent loop is not affected; we then
		// close turnDone to unblock the listener goroutine and cancel to release the ctx.
		signal.Stop(sigCh)
		close(turnDone)
		turnCancel()
	}
}
