//go:build linux

package terminal

import "golang.org/x/sys/unix"

const (
	ioctlGet = unix.TCGETS
	ioctlSet = unix.TCSETS

	iflagICRNL  = unix.ICRNL
	iflagIXON   = unix.IXON
	iflagISTRIP = unix.ISTRIP
	lflagICANON = unix.ICANON
	lflagECHO   = unix.ECHO
	lflagIEXTEN = unix.IEXTEN
	lflagISIG   = unix.ISIG
	oflagOPOST  = unix.OPOST
	cflagCSIZE  = unix.CSIZE
	cflagPARENB = unix.PARENB
	cflagCS8    = unix.CS8
	vminIdx     = unix.VMIN
	vtimeIdx    = unix.VTIME
)

type termios = unix.Termios
