//go:build darwin || linux

package terminal

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/skys-mission/creator-agent/paths"
)

const stderrLogName = "tui-stderr.log"

func RedirectStderrToFile() string {
	dir, err := paths.LogDir()
	if err != nil {
		return ""
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return ""
	}
	path := filepath.Join(dir, stderrLogName)
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return ""
	}
	if err := dupStderr(int(f.Fd())); err != nil {
		f.Close()
		return ""
	}
	f.Close()
	fmt.Fprintf(os.Stderr, "\n==== creator-agent TUI session start %s ====\n", time.Now().Format(time.RFC3339))
	return path
}
