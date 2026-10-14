//go:build !windows

package main

import "context"

func startApplication() error { return run(context.Background(), func() {}) }
