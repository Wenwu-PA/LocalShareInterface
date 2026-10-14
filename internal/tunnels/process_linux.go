//go:build linux

package tunnels

import (
	"os/exec"
	"syscall"
)

func hideWindow(cmd *exec.Cmd)                     { cmd.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGKILL} }
func attachLifetime(cmd *exec.Cmd) (func(), error) { return func() {}, nil }
