// Package diag provides always-on, low-overhead crash diagnostics for the production (non-race)
// build. It exists because the hardest crashes — memory-corruption fatal errors ("found bad pointer
// in Go heap", "unexpected signal during runtime execution") — are raised by the Go runtime via
// runtime.throw, which bypasses every deferred cleanup and recover. Those crashes cannot be caught
// in-process; the only way to diagnose them after the fact is to have already written, incrementally
// to disk, (a) the full crash report and (b) a breadcrumb trail of what the agent was doing.
//
// Two outputs, both under the data dir (~/.creator):
//   - fatal.log : the runtime's crash report (all goroutine stacks), via debug.SetCrashOutput.
//   - trace.log : timestamped breadcrumbs of coarse lifecycle events, written unbuffered so the
//     tail always reflects the instant of death (a process crash cannot lose an already-written
//     line; only power loss could, and we deliberately skip fsync to keep the hot path cheap).
//
// Both files are size-rotated once on Setup to bound disk usage without losing the most recent run.
package diag

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"sync"
	"time"

	"github.com/skys-mission/creator-agent/paths"
)

const (
	traceFileName = "trace.log"
	fatalFileName = "fatal.log"
	// maxLogBytes caps each log; on Setup a file over this size is rotated to "<name>.1".
	maxLogBytes = 2 << 20 // 2 MiB
)

var (
	mu        sync.Mutex
	traceFile *os.File // nil when diagnostics are disabled (no home dir / open failure)
	// fatalFile is kept referenced for the process lifetime so the fd handed to the runtime via
	// debug.SetCrashOutput stays valid; it is never written to by us.
	fatalFile *os.File
	pid       = os.Getpid()
)

// Setup initializes crash diagnostics. Safe to call once near program start; subsequent calls are
// no-ops. It never fails hard: if the log directory is unavailable, diagnostics degrade to off and
// the program continues normally. version identifies the build (e.g. a git commit) for correlating
// a crash with source.
func Setup() {
	mu.Lock()
	defer mu.Unlock()
	if traceFile != nil {
		return // already set up
	}
	dir, err := paths.LogDir()
	if err != nil {
		return
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}

	// Route the runtime's fatal crash report (all goroutines) to a dedicated, rotated file. This is
	// the canonical capture path for runtime.throw crashes that skip every defer.
	debug.SetTraceback("all")
	rotateIfLarge(filepath.Join(dir, fatalFileName))
	if f, ferr := os.OpenFile(filepath.Join(dir, fatalFileName), os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600); ferr == nil {
		if serr := debug.SetCrashOutput(f, debug.CrashOptions{}); serr == nil {
			fatalFile = f // keep open: the runtime holds this fd until exit
		} else {
			f.Close()
		}
	}

	rotateIfLarge(filepath.Join(dir, traceFileName))
	if f, ferr := os.OpenFile(filepath.Join(dir, traceFileName), os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600); ferr == nil {
		traceFile = f
	}

	writeLine(fmt.Sprintf("==== session start pid=%d %s/%s go=%s build=%s ====",
		pid, runtime.GOOS, runtime.GOARCH, runtime.Version(), buildRevision()))
}

// Trace appends one timestamped breadcrumb line. Cheap (a single unbuffered write) and concurrency
// safe. No-op when diagnostics are disabled. Keep messages short and coarse-grained (per turn / tool
// / stream), not per-token, to stay off the hot path.
func Trace(format string, args ...any) {
	if format == "" {
		return
	}
	msg := format
	if len(args) > 0 {
		msg = fmt.Sprintf(format, args...)
	}
	mu.Lock()
	writeLine(msg)
	mu.Unlock()
}

// writeLine writes one line to the trace file. Caller holds mu (except the Setup header, which runs
// under the same lock). Failures are swallowed: diagnostics must never disrupt the program.
func writeLine(msg string) {
	if traceFile == nil {
		return
	}
	ts := time.Now().Format("2006-01-02T15:04:05.000")
	_, _ = fmt.Fprintf(traceFile, "%s pid=%d %s\n", ts, pid, msg)
}

// rotateIfLarge renames path to path+".1" when it exceeds maxLogBytes, keeping exactly one previous
// generation. Best-effort: any error leaves the original in place.
func rotateIfLarge(path string) {
	info, err := os.Stat(path)
	if err != nil || info.Size() <= maxLogBytes {
		return
	}
	_ = os.Rename(path, path+".1")
}

// buildRevision returns the VCS revision embedded by the Go toolchain at build time (with a "-dirty"
// suffix when the working tree had uncommitted changes), or "unknown" when unavailable. This pins a
// crash report to exact source without a hand-maintained version string.
func buildRevision() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "unknown"
	}
	var rev, modified string
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			rev = s.Value
		case "vcs.modified":
			if s.Value == "true" {
				modified = "-dirty"
			}
		}
	}
	if rev == "" {
		return "unknown"
	}
	if len(rev) > 12 {
		rev = rev[:12]
	}
	return rev + modified
}
