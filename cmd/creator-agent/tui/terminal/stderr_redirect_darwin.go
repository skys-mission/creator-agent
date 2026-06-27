//go:build darwin

package terminal

import "golang.org/x/sys/unix"

func dupStderr(fd int) error {
	return unix.Dup2(fd, unix.Stderr)
}
