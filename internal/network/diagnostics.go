package network

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"lanbridge/internal/monitor"
	"net"
	"os/exec"
	"regexp"
	"runtime"
	"strings"
	"time"
)

var diagnosticTarget = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.:-]{0,252}$`)

func Diagnose(ctx context.Context, kind, target string, emit func(string) error) error {
	if !diagnosticTarget.MatchString(target) {
		return errors.New("некорректный адрес")
	}
	switch kind {
	case "ping", "port":
		if kind == "port" {
			if _, _, e := net.SplitHostPort(target); e != nil {
				return errors.New("порт: host:port")
			}
		}
		count := 4
		probe := "icmp"
		if kind == "port" {
			count = 1
			probe = "tcp"
		}
		for i := 0; i < count; i++ {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			r := monitor.Probe(ctx, 0, monitor.Definition{Kind: probe, Target: target, Timeout: 3}, 0)
			status := "OK"
			if !r.OK {
				status = "Ошибка"
			}
			if e := emit(fmt.Sprintf("%d. %s · %.1f мс · %s", i+1, status, r.Latency, r.Message)); e != nil {
				return e
			}
			if i+1 < count {
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(time.Second):
				}
			}
		}
		return nil
	case "dns":
		resolver := net.Resolver{PreferGo: true}
		addresses, e := resolver.LookupHost(ctx, target)
		if e != nil {
			return errors.New("DNS: имя не разрешено")
		}
		for _, address := range addresses {
			if e = emit(address); e != nil {
				return e
			}
		}
		return nil
	case "traceroute":
		name, args := "traceroute", []string{"-n", "-m", "12", "-w", "1", target}
		if runtime.GOOS == "windows" {
			name = "tracert"
			args = []string{"-d", "-h", "12", "-w", "1000", target}
		}
		cmd := exec.CommandContext(ctx, name, args...)
		hideWindow(cmd)
		pipe, e := cmd.StdoutPipe()
		if e != nil {
			return e
		}
		if e = cmd.Start(); e != nil {
			return errors.New("traceroute/tracert не найден или недоступен")
		}
		scanner := bufio.NewScanner(pipe)
		scanner.Buffer(make([]byte, 4096), 16384)
		lines := 0
		for scanner.Scan() {
			lines++
			if lines > 128 {
				cmd.Process.Kill()
				cmd.Wait()
				return errors.New("превышен объём вывода")
			}
			if e = emit(strings.ToValidUTF8(scanner.Text(), "?")); e != nil {
				cmd.Process.Kill()
				cmd.Wait()
				return e
			}
		}
		scanErr := scanner.Err()
		if scanErr != nil {
			cmd.Process.Kill()
		}
		e = cmd.Wait()
		if scanErr != nil {
			return scanErr
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if e != nil {
			return errors.New("трассировка завершилась с ошибкой или цель недоступна")
		}
		return nil
	default:
		return errors.New("неизвестная диагностика")
	}
}
