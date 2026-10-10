//go:build !windows

package network

import "os/exec"

func hideWindow(cmd *exec.Cmd) {}
