//go:build windows

package daemon

import "os/exec"

func ownProcessGroup(*exec.Cmd) {}

func killGroup(int) {}
