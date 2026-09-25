// Command creator-agent is the single-binary product: the agent runtime core
// plus its channels (docs/architecture.md). Default form is local and
// network-free; the network face only opens on demand.
//
// Subcommands:
//
//	(none)            full-screen TUI, in-process direct call (the default product form)
//	status            one-shot core status line
//	serve             open the gRPC network face (loopback + bearer token)
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/skys-mission/creator-agent/cmd/creator-agent/tui"
	"github.com/skys-mission/creator-agent/contract"
	"github.com/skys-mission/creator-agent/core"
	"github.com/skys-mission/creator-agent/kernel"
	"github.com/skys-mission/creator-agent/server/auth"
	grpcsvc "github.com/skys-mission/creator-agent/server/grpc"
)

const version = "0.0.1-dev"

// defaultListen pins the network face to loopback: gate 1 of the three-gate
// hardening (docs/architecture.md §8). Widen deliberately, never by default.
const defaultListen = "127.0.0.1:53711"

// tokenFileName is the bearer token file under the data dir (0600).
const tokenFileName = "rpc-token"

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "creator-agent:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	switch {
	case len(args) == 0 || strings.HasPrefix(args[0], "-"):
		return runTUI(args)
	case args[0] == "status":
		return runStatus()
	case args[0] == "serve":
		return runServe(args[1:])
	default:
		return fmt.Errorf("unknown command %q (status | serve)", args[0])
	}
}

// runTUI is the default product form: the minimal rebuild shell (input box + /model-new),
// in-process, no network. Optional flags come before any subcommand ("creator-agent -lang zh").
func runTUI(args []string) error {
	fs := flag.NewFlagSet("creator-agent", flag.ContinueOnError)
	lang := fs.String("lang", "", "UI language: en | zh (default: from locale)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("unknown command %q (status | serve)", fs.Arg(0))
	}
	l := *lang
	if l == "" {
		l = detectLang()
	}
	err := tui.Run(l)
	if errors.Is(err, tui.ErrTerminalUnavailable) {
		// No REPL fallback exists yet: say so plainly instead of failing cryptically.
		return fmt.Errorf("%w — the full-screen TUI needs a real interactive terminal (run it from a shell, not a pipe or redirect)", err)
	}
	return err
}

// detectLang picks the UI language from the locale: any zh* locale shows Chinese, everything else
// English (the i18n default).
func detectLang() string {
	for _, key := range []string{"LC_ALL", "LC_CTYPE", "LANG"} {
		loc := strings.ToLower(os.Getenv(key))
		if loc == "" || loc == "c" || loc == "posix" {
			continue
		}
		if strings.HasPrefix(loc, "zh") {
			return "zh"
		}
		return "en"
	}
	return "en"
}

// runStatus is the one-shot status line: one direct core method call, zero network.
func runStatus() error {
	c := core.New(version)
	st, err := c.Status(context.Background())
	if err != nil {
		return err
	}
	fmt.Printf("creator-agent %s (up %s) — in-process direct call, no network\n", st.Version, st.Uptime)
	return nil
}

// runServe opens the gRPC network face assembled from kernel components:
// "auth" issues the token before "grpc" starts serving (dependency order G2).
func runServe(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	listen := fs.String("listen", defaultListen, "gRPC listen address (loopback by default)")
	tokenFile := fs.String("token-file", "", "bearer token file (default <data dir>/"+tokenFileName+")")
	if err := fs.Parse(args); err != nil {
		return err
	}

	tf := *tokenFile
	if tf == "" {
		dir, err := contract.DataDir()
		if err != nil {
			return err
		}
		tf = filepath.Join(dir, tokenFileName)
	}
	store := auth.New(tf)

	app := kernel.New()
	if err := app.Add(kernel.Funcs{
		CompName: "auth",
		OnStart:  func(*kernel.Scope) error { return store.Ensure() },
	}); err != nil {
		return err
	}
	var srv *grpcsvc.Server
	if err := app.Add(kernel.Funcs{
		CompName: "grpc",
		CompDeps: []string{"auth"},
		OnStart: func(*kernel.Scope) error {
			s, err := grpcsvc.New(store, *listen)
			if err != nil {
				return err
			}
			srv = s
			fmt.Printf("gRPC listening on %s (bearer token in %s)\n", s.Addr(), tf)
			go func() {
				if err := s.Serve(); err != nil {
					slog.Error("grpc serve ended", "err", err)
				}
			}()
			return nil
		},
		OnStop: func(ctx context.Context) error {
			if srv == nil {
				return nil
			}
			return srv.Stop(ctx)
		},
	}); err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := app.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		return err
	}
	return nil
}
