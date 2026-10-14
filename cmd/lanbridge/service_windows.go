package main

import (
	"context"
	"time"

	"golang.org/x/sys/windows/svc"
)

const serviceName = "LANBridgeGo"

func startApplication() error {
	service, err := svc.IsWindowsService()
	if err != nil {
		return err
	}
	if !service {
		return run(context.Background(), func() {})
	}
	return svc.Run(serviceName, &windowsService{run: run})
}

type windowsService struct {
	run func(context.Context, func()) error
}

func (s *windowsService) Execute(_ []string, requests <-chan svc.ChangeRequest, changes chan<- svc.Status) (bool, uint32) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ready := make(chan struct{}, 1)
	done := make(chan error, 1)
	status := svc.Status{State: svc.StartPending, WaitHint: 30000}
	changes <- status
	go func() {
		done <- s.run(ctx, func() {
			select {
			case ready <- struct{}{}:
			default:
			}
		})
	}()
	tick := time.NewTicker(5 * time.Second)
	defer tick.Stop()
	var stopping <-chan time.Time
	var timer *time.Timer
	defer func() {
		if timer != nil {
			timer.Stop()
		}
	}()
	for {
		select {
		case <-ready:
			if status.State == svc.StartPending {
				status = svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}
				changes <- status
			}
		case err := <-done:
			if err != nil {
				return true, 1
			}
			return false, 0
		case <-stopping:
			return true, 2
		case <-tick.C:
			if status.State == svc.StartPending || status.State == svc.StopPending {
				status.CheckPoint++
				changes <- status
			}
		case request, ok := <-requests:
			if !ok {
				cancel()
				return true, 3
			}
			switch request.Cmd {
			case svc.Interrogate:
				changes <- status
			case svc.Stop, svc.Shutdown:
				if status.State != svc.StopPending {
					status = svc.Status{State: svc.StopPending, WaitHint: 30000}
					changes <- status
					cancel()
					timer = time.NewTimer(30 * time.Second)
					stopping = timer.C
				}
			}
		}
	}
}
