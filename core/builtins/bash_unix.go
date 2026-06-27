//go:build unix

package builtins

import (
	"fmt"
	"os/exec"
	"syscall"
)

// setSysProcAttr makes the command (and all its children) the leader of a new process group.
//
// Background: exec.CommandContext on ctx cancellation only SIGKILLs the direct child (bash itself).
// But child processes spawned by bash (e.g. `npm run dev &`, `sleep 1000 &`) escape and become
// orphan processes, continuing to hold ports/resources. With Setpgid=true, the command and its
// children form a single group (PGID=cmd.Pid), so killProcessGroup can kill the entire tree
// with kill -PGID.
func setSysProcAttr(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
}

// killProcessGroup sends SIGKILL to the command's process group (kills the entire tree).
//
// The process group PGID equals cmd.Process.Pid (because Setpgid=true, the leader PID is the PGID).
// A negative PID sends the signal to the whole group. ESRCH (no such process) is ignored.
func killProcessGroup(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return fmt.Errorf("process not started")
	}
	// -pid = process group. Ignore ESRCH (process already exited).
	if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != nil && err != syscall.ESRCH {
		return err
	}
	return nil
}
