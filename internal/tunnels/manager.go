// Package tunnels manages an explicitly started, temporary public connector.
package tunnels

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Provider is the extension point for additional connectors. No shell is used.
type Provider interface {
	Name() string
	Available() error
	Command(context.Context, string, string) *exec.Cmd
}
type Cloudflare struct{ Executable string }

func (p Cloudflare) Name() string     { return "cloudflare" }
func (p Cloudflare) Available() error { _, e := exec.LookPath(p.Executable); return e }
func (p Cloudflare) Command(ctx context.Context, origin, config string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, p.Executable, "tunnel", "--config", config, "--no-autoupdate", "--url", origin)
	hideWindow(cmd)
	// Isolate the quick connector from unrelated configured credentials/tunnels.
	for _, v := range os.Environ() {
		key, _, _ := strings.Cut(v, "=")
		key = strings.ToUpper(key)
		if !strings.HasPrefix(key, "LANBRIDGE_") && !strings.HasPrefix(key, "TUNNEL_") && !strings.HasPrefix(key, "CLOUDFLARE_") {
			cmd.Env = append(cmd.Env, v)
		}
	}
	cmd.WaitDelay = 3 * time.Second
	return cmd
}

type State struct {
	Provider string `json:"provider"`
	State    string `json:"state"`
	URL      string `json:"url"`
	Started  int64  `json:"started"`
	Error    string `json:"error"`
}
type Manager struct {
	mu       sync.Mutex
	provider Provider
	status   State
	cancel   context.CancelFunc
	gateway  *http.Server
	done     chan struct{}
	timeout  time.Duration
	closed   bool
}

func New(p Provider) *Manager {
	return &Manager{provider: p, status: State{Provider: p.Name(), State: "off"}, timeout: 45 * time.Second}
}
func (m *Manager) Status() State   { m.mu.Lock(); defer m.mu.Unlock(); return m.status }
func (m *Manager) Available() bool { return m.provider.Available() == nil }
func (m *Manager) Start(handler http.Handler) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return errors.New("сервер завершает работу")
	}
	if m.done != nil {
		return errors.New("туннель уже запущен или останавливается")
	}
	if e := m.provider.Available(); e != nil {
		return errors.New("cloudflared не найден; установите его и укажите external.cloudflared")
	}
	listener, e := net.Listen("tcp4", "127.0.0.1:0")
	if e != nil {
		return errors.New("не удалось открыть локальный порт шлюза")
	}
	dir, e := os.MkdirTemp("", "lanbridge-tunnel-")
	if e != nil {
		listener.Close()
		return e
	}
	config := filepath.Join(dir, "config.yaml")
	if e = os.WriteFile(config, []byte("{}\n"), 0600); e != nil {
		listener.Close()
		os.RemoveAll(dir)
		return e
	}
	ctx, cancel := context.WithCancel(context.Background())
	cmd := m.provider.Command(ctx, "http://"+listener.Addr().String(), config)
	urls := make(chan string, 1)
	writer := &urlWriter{found: func(public string) {
		select {
		case urls <- public:
		default:
		}
	}}
	cmd.Stdout = writer
	cmd.Stderr = writer
	if e = cmd.Start(); e != nil {
		cancel()
		listener.Close()
		os.RemoveAll(dir)
		return errors.New("не удалось запустить cloudflared")
	}
	release, e := attachLifetime(cmd)
	if e != nil {
		cancel()
		cmd.Wait()
		listener.Close()
		os.RemoveAll(dir)
		return errors.New("не удалось привязать туннель к процессу сервера")
	}
	gateway := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 16 * 1024}
	m.gateway = gateway
	m.cancel = cancel
	m.done = make(chan struct{})
	done := m.done
	m.status = State{Provider: m.provider.Name(), State: "starting", Started: time.Now().Unix()}
	go func() {
		if e := gateway.Serve(listener); e != nil && e != http.ErrServerClosed {
			cancel()
		}
	}()
	go func() {
		timer := time.NewTimer(m.timeout)
		defer timer.Stop()
		select {
		case public := <-urls:
			m.mu.Lock()
			if m.done == done && m.status.State == "starting" {
				m.status.State = "running"
				m.status.URL = public
			}
			m.mu.Unlock()
		case <-timer.C:
			m.mu.Lock()
			pending := m.done == done && m.status.State == "starting"
			if pending {
				m.status.State = "error"
				m.status.Error = "За 45 секунд не получен публичный URL"
			}
			m.mu.Unlock()
			if pending {
				cancel()
			}
		case <-done:
		}
	}()
	go func() {
		err := cmd.Wait()
		release()
		cancel()
		gateway.Close()
		os.RemoveAll(dir)
		m.mu.Lock()
		defer m.mu.Unlock()
		if m.status.State == "stopping" {
			m.status = State{Provider: m.provider.Name(), State: "off"}
		} else if m.status.State != "error" {
			m.status.State = "error"
			m.status.Error = "cloudflared завершился; включите туннель заново"
			if err == nil {
				m.status.Error = "cloudflared остановлен"
			}
		}
		m.status.URL = ""
		m.gateway = nil
		m.cancel = nil
		m.done = nil
		close(done)
	}()
	return nil
}
func (m *Manager) Stop() error {
	m.mu.Lock()
	if m.done == nil {
		m.status = State{Provider: m.provider.Name(), State: "off"}
		m.mu.Unlock()
		return nil
	}
	m.status.State = "stopping"
	m.status.URL = ""
	gateway, cancel, done := m.gateway, m.cancel, m.done
	m.mu.Unlock()
	// Close the gateway first: even a delayed connector shutdown cannot serve files.
	gateway.Close()
	cancel()
	select {
	case <-done:
		return nil
	case <-time.After(5 * time.Second):
		return errors.New("шлюз закрыт; процесс туннеля ещё завершается")
	}
}

// Close permanently prevents concurrent API requests from restarting a connector
// while the application's other services are shutting down.
func (m *Manager) Close() error { m.mu.Lock(); m.closed = true; m.mu.Unlock(); return m.Stop() }

var quickURL = regexp.MustCompile(`(?:^|[\s|"'])(https://[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.trycloudflare\.com)(?:[\s|"']|$)`)

type urlWriter struct {
	mu       sync.Mutex
	line     []byte
	overflow bool
	found    func(string)
}

func (w *urlWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, b := range p {
		if b == '\n' {
			if !w.overflow {
				match := quickURL.FindStringSubmatch(string(w.line))
				if len(match) > 1 {
					if u, e := url.Parse(match[1]); e == nil && u.Host != "" {
						w.found(match[1])
					}
				}
			}
			w.line = w.line[:0]
			w.overflow = false
		} else if len(w.line) < 8192 {
			w.line = append(w.line, b)
		} else {
			w.overflow = true
		}
	}
	return len(p), nil
}

var _ io.Writer = (*urlWriter)(nil)
