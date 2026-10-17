package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"lanbridge/internal/tunnels"
)

type externalTestProvider struct{ origin chan string }

func (p externalTestProvider) Name() string     { return "cloudflare" }
func (p externalTestProvider) Available() error { return nil }
func (p externalTestProvider) Command(ctx context.Context, origin, config string) *exec.Cmd {
	p.origin <- origin
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestExternalProcessHelper$")
	cmd.Env = append(os.Environ(), "LANBRIDGE_TEST_EXTERNAL=1")
	cmd.WaitDelay = time.Second
	return cmd
}
func TestExternalProcessHelper(t *testing.T) {
	if os.Getenv("LANBRIDGE_TEST_EXTERNAL") == "" {
		return
	}
	fmt.Println("https://test-guest.trycloudflare.com")
	time.Sleep(time.Minute)
	os.Exit(0)
}
func TestExternalGatewayIsolation(t *testing.T) {
	c, g := shareFixture(t)
	origins := make(chan string, 1)
	c.server.External = tunnels.New(externalTestProvider{origins})
	t.Cleanup(func() { c.server.External.Stop() })
	g.call("GET", "/api/external", nil, 401)
	c.call("POST", "/api/external/start", map[string]any{"provider": "cloudflare"}, 400)
	c.call("POST", "/api/external/start", map[string]any{"provider": "cloudflare", "confirm_public_guest_access": true}, 202)
	origin := <-origins
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && c.server.External.Status().State != "running" {
		time.Sleep(10 * time.Millisecond)
	}
	call := func(method, target, body, csrf string, want int) []byte {
		t.Helper()
		r, _ := http.NewRequest(method, origin+target, strings.NewReader(body))
		r.Host = "test-guest.trycloudflare.com"
		r.Header.Set("X-Forwarded-Proto", "https")
		r.Header.Set("Cookie", sessionCookie+"=copied-session; "+csrfCookie+"="+csrf)
		if method == "POST" {
			r.Header.Set("Content-Type", "application/json")
			r.Header.Set("X-CSRF-Token", csrf)
			r.Header.Set("Origin", "https://test-guest.trycloudflare.com")
		}
		resp, e := http.DefaultClient.Do(r)
		if e != nil {
			t.Fatal(e)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != want {
			t.Fatalf("%s %s: %d want %d: %s", method, target, resp.StatusCode, want, b)
		}
		return b
	}
	for _, p := range []string{"/", "/api/me", "/api/files", "/api/shares", "/api/external", "/api/events", "/healthz", "/api/settings", "/api/branding", "/assets/../api/me", "/api/%73tatus", "//api/status"} {
		call("GET", p, "", "", 404)
	}
	call("POST", "/api/login", "{}", "", 404)
	call("PUT", "/api/branding", "image", "", 404)
	call("DELETE", "/api/branding", "", "", 404)
	var status map[string]any
	json.Unmarshal(call("GET", "/api/status", "", "", 200), &status)
	csrf := status["csrf"].(string)
	if status["authenticated"] != false || status["user"] != nil {
		t.Fatal(status)
	}
	share := makeShare(c, "folder/hello.txt", "", 1)
	endpoint := guestEndpoint(share)
	call("GET", endpoint, "", "", 200)
	call("POST", endpoint+"/ticket", "{}", "", 403)
	var ticket map[string]string
	json.Unmarshal(call("POST", endpoint+"/ticket", "{}", csrf, 201), &ticket)
	if b := call("GET", ticket["url"], "", "", 200); !bytes.Equal(b, []byte("guest content")) {
		t.Fatal(string(b))
	}
	c.call("POST", "/api/external/stop", map[string]any{}, 200)
	if resp, e := http.Get(origin + endpoint); e == nil {
		resp.Body.Close()
		t.Fatal("public gateway still open")
	}
	// Untrusted LAN requests cannot use the public gateway's forwarded identity.
	g.call("GET", "/api/me", nil, 401)
}
func TestExternalRoutesAndWAN(t *testing.T) {
	token := strings.Repeat("A", 43)
	for _, p := range []string{"/s/" + token, "/api/guest/" + token, "/api/guest-download/" + token, "/assets/app.js"} {
		r, _ := http.NewRequest("GET", "https://host"+p, nil)
		if externalRoute(r) == "blocked" {
			t.Fatal(p)
		}
	}
	cases := map[string]string{"100.64.0.1": "shared", "100.127.255.254": "shared", "192.168.1.1": "private", "10.0.0.2": "private", "127.0.0.1": "invalid", "8.8.8.8": "match", "1.1.1.1": "different", "": "unknown"}
	for ip, want := range cases {
		if got := classifyWAN(ip, "8.8.8.8"); got != want {
			t.Fatal(ip, got, want)
		}
	}
}
