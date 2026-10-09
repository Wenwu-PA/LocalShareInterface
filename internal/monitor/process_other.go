//go:build !windows

package monitor

import "os/exec"

func hideWindow(cmd *exec.Cmd) {}
