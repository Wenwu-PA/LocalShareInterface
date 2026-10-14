package main

import (
	"context"
	"errors"
	"golang.org/x/sys/windows/svc"
	"testing"
	"time"
)

func TestServiceReadyAndGracefulStop(t *testing.T) {
	requests := make(chan svc.ChangeRequest, 4)
	changes := make(chan svc.Status, 8)
	done := make(chan uint32, 1)
	s := windowsService{run: func(ctx context.Context, ready func()) error { ready(); <-ctx.Done(); return nil }}
	go func() { _, code := s.Execute(nil, requests, changes); done <- code }()
	next := func(want svc.State) {
		t.Helper()
		select {
		case status := <-changes:
			if status.State != want {
				t.Fatal(status, want)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("service state timeout")
		}
	}
	next(svc.StartPending)
	next(svc.Running)
	requests <- svc.ChangeRequest{Cmd: svc.Interrogate}
	next(svc.Running)
	requests <- svc.ChangeRequest{Cmd: svc.Stop}
	next(svc.StopPending)
	select {
	case code := <-done:
		if code != 0 {
			t.Fatal(code)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("stop timeout")
	}
}

func TestServiceStartupFailure(t *testing.T) {
	s := windowsService{run: func(context.Context, func()) error { return errors.New("startup failed") }}
	changes := make(chan svc.Status, 4)
	specific, code := s.Execute(nil, make(chan svc.ChangeRequest), changes)
	if !specific || code != 1 {
		t.Fatal(specific, code)
	}
	if len(changes) != 1 {
		t.Fatal("service advertised Running before ready")
	}
}
