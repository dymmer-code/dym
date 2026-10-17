//go:build !windows

package daemon

import (
	"os/exec"
	"syscall"
)

// ownProcessGroup makes the command lead a group of its own, so that ending it ends what it started too.
func ownProcessGroup(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true} }

func killGroup(pid int) { _ = syscall.Kill(-pid, syscall.SIGKILL) }
