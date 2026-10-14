//go:build !windows && !linux

package tunnels

import "os/exec"

func hideWindow(cmd *exec.Cmd)                     {}
func attachLifetime(cmd *exec.Cmd) (func(), error) { return func() {}, nil }
