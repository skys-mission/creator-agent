//go:build unix

package core

import (
	"fmt"
	"os"
	"syscall"
)

// flockFile acquires an exclusive advisory lock on path (blocking until acquired).
// Prevents other creator-agent processes on the same host from concurrently writing the same session file,
// avoiding silent data loss from cross-process overwrites. Returns an unlock function (closing the fd releases the lock).
func flockFile(path string) (func(), error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open lock file: %w", err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		f.Close()
		return nil, fmt.Errorf("flock: %w", err)
	}
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
	}, nil
}
