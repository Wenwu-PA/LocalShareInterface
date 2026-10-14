package tunnels

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

type helperProvider struct {
	mode   string
	origin chan string
}

func (p helperProvider) Name() string     { return "test" }
func (p helperProvider) Available() error { return nil }
func (p helperProvider) Command(ctx context.Context, origin, config string) *exec.Cmd {
	if p.origin != nil {
		p.origin <- origin
	}
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestConnectorHelper$")
	cmd.Env = append(os.Environ(), "LANBRIDGE_TEST_CONNECTOR="+p.mode)
	cmd.WaitDelay = time.Second
	hideWindow(cmd)
	return cmd
}
func TestConnectorHelper(t *testing.T) {
	mode := os.Getenv("LANBRIDGE_TEST_CONNECTOR")
	if mode == "" {
		return
	}
	if mode == "exit" {
		os.Exit(1)
	}
	if mode == "ready" {
		fmt.Fprintln(os.Stderr, "| https://test-lanbridge.trycloudflare.com |")
	}
	time.Sleep(time.Minute)
	os.Exit(0)
}
func waitState(t *testing.T, m *Manager, want string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if m.Status().State == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal(m.Status())
}
func TestLifecycleAndGatewayClose(t *testing.T) {
	origins := make(chan string, 2)
	m := New(helperProvider{"ready", origins})
	t.Cleanup(func() { m.Stop() })
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) })
	for i := 0; i < 2; i++ {
		if e := m.Start(h); e != nil {
			t.Fatal(e)
		}
		origin := <-origins
		waitState(t, m, "running")
		if e := m.Start(h); e == nil {
			t.Fatal("duplicate start")
		}
		resp, e := http.Get(origin)
		if e != nil {
			t.Fatal(e)
		}
		resp.Body.Close()
		if e = m.Stop(); e != nil {
			t.Fatal(e)
		}
		if m.Status().State != "off" || m.Status().URL != "" {
			t.Fatal(m.Status())
		}
		if resp, e = http.Get(origin); e == nil {
			resp.Body.Close()
			t.Fatal("gateway still open")
		}
	}
}
func TestStartupTimeoutAndProcessExit(t *testing.T) {
	for _, mode := range []string{"silent", "exit"} {
		t.Run(mode, func(t *testing.T) {
			m := New(helperProvider{mode, nil})
			m.timeout = 50 * time.Millisecond
			t.Cleanup(func() { m.Stop() })
			if e := m.Start(http.NotFoundHandler()); e != nil {
				t.Fatal(e)
			}
			waitState(t, m, "error")
			if m.Status().URL != "" {
				t.Fatal(m.Status())
			}
		})
	}
}
func TestURLLogBounded(t *testing.T) {
	var got []string
	w := &urlWriter{found: func(s string) { got = append(got, s) }}
	w.Write([]byte("prefix https://test-"))
	w.Write([]byte("site.trycloudflare.com |\n"))
	w.Write([]byte(strings.Repeat("x", 9000) + " https://bad.trycloudflare.com\n"))
	w.Write([]byte("https://lookalike.trycloudflare.com.evil.example\nhttps://-bad.trycloudflare.com\n"))
	if len(got) != 1 || got[0] != "https://test-site.trycloudflare.com" || len(w.line) > 8192 {
		t.Fatal(got)
	}
}

func TestClosedManagerCannotRestart(t *testing.T) {
	m := New(helperProvider{"ready", nil})
	if e := m.Close(); e != nil {
		t.Fatal(e)
	}
	if e := m.Start(http.NotFoundHandler()); e == nil {
		m.Stop()
		t.Fatal("restarted during shutdown")
	}
}
