//go:build linux

package terminal

import "golang.org/x/sys/unix"

func dupStderr(fd int) error {
	return unix.Dup3(fd, unix.Stderr, 0)
}
