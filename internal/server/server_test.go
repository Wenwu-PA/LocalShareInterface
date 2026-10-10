package server

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"lanbridge/internal/config"
	"lanbridge/internal/store"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"
)

type testClient struct {
	t    *testing.T
	srv  *httptest.Server
	http *http.Client
	csrf string
	db   *sql.DB
}

func newClient(t *testing.T) *testClient {
	t.Helper()
	db, e := store.Open(filepath.Join(t.TempDir(), "test.sqlite3"))
	if e != nil {
		t.Fatal(e)
	}
	cfg := config.Default()
	cfg.Storage.Root = t.TempDir()
	s := New(db, cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if e = s.InitFiles(); e != nil {
		t.Fatal(e)
	}
	srv := httptest.NewTLSServer(s.Handler(nil))
	jar, _ := cookiejar.New(nil)
	client := srv.Client()
	client.Jar = jar
	t.Cleanup(func() { s.CloseStreams(); srv.Close(); s.Files.Close(); db.Close() })
	c := &testClient{t: t, srv: srv, http: client, db: db}
	c.call("GET", "/api/status", nil, 200)
	return c
}
func (c *testClient) call(method, path string, body any, status int) map[string]any {
	c.t.Helper()
	var b bytes.Buffer
	if body != nil {
		_ = json.NewEncoder(&b).Encode(body)
	}
	r, _ := http.NewRequest(method, c.srv.URL+path, &b)
	if body != nil {
		r.Header.Set("Content-Type", "application/json")
	}
	if c.csrf != "" {
		r.Header.Set("X-CSRF-Token", c.csrf)
	}
	resp, e := c.http.Do(r)
	if e != nil {
		c.t.Fatal(e)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != status {
		c.t.Fatalf("%s %s: got %d want %d: %s", method, path, resp.StatusCode, status, raw)
	}
	var result map[string]any
	_ = json.Unmarshal(raw, &result)
	if token, ok := result["csrf"].(string); ok {
		c.csrf = token
	}
	return result
}
func (c *testClient) setup() {
	c.call("POST", "/api/setup", credentials{"testadmin", "a-long-test-password"}, 200)
}
func TestSetupLoginLogoutAndPersistence(t *testing.T) {
	c := newClient(t)
	c.setup()
	c.call("POST", "/api/setup", credentials{"otheradmin", "a-long-test-password"}, 409)
	u := c.call("GET", "/api/me", nil, 200)
	if u["username"] != "testadmin" {
		t.Fatal(u)
	}
	c.call("POST", "/api/logout", map[string]any{}, 200)
	c.call("GET", "/api/me", nil, 401)
	c.call("GET", "/api/status", nil, 200)
	c.call("POST", "/api/login", credentials{"testadmin", "a-long-test-password"}, 200)
	var hash string
	if e := c.db.QueryRow("SELECT password_hash FROM users").Scan(&hash); e != nil {
		t.Fatal(e)
	}
	if !checkPassword("a-long-test-password", hash) || checkPassword("wrong", hash) {
		t.Fatal("invalid password verification")
	}
}
func TestCSRFAndOrigin(t *testing.T) {
	c := newClient(t)
	token := c.csrf
	c.csrf = ""
	c.call("POST", "/api/setup", credentials{"testadmin", "a-long-test-password"}, 403)
	c.csrf = token
	c.setup()
	r, _ := http.NewRequest("GET", c.srv.URL+"/api/status", nil)
	r.Header.Set("Origin", "https://evil.example")
	resp, e := c.http.Do(r)
	if e != nil {
		t.Fatal(e)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 403 {
		t.Fatal(resp.StatusCode)
	}
}
func TestRateLimit(t *testing.T) {
	c := newClient(t)
	c.setup()
	c.call("POST", "/api/logout", map[string]any{}, 200)
	c.call("GET", "/api/status", nil, 200)
	for i := 0; i < 8; i++ {
		c.call("POST", "/api/login", credentials{"testadmin", "incorrect-password"}, 401)
	}
	c.call("POST", "/api/login", credentials{"testadmin", "a-long-test-password"}, 429)
}
func TestExpiredSession(t *testing.T) {
	c := newClient(t)
	c.setup()
	_, e := c.db.Exec("UPDATE sessions SET expires_at=?", time.Now().Add(-time.Hour).Unix())
	if e != nil {
		t.Fatal(e)
	}
	c.call("GET", "/api/me", nil, 401)
}
func TestSettingsValidationAndRoles(t *testing.T) {
	c := newClient(t)
	c.setup()
	p := defaultPreferences()
	p.Name = "Моя сеть"
	c.call("PUT", "/api/settings", p, 200)
	if c.call("GET", "/api/settings", nil, 200)["name"] != p.Name {
		t.Fatal("not saved")
	}
	p.Accent = "url(evil)"
	c.call("PUT", "/api/settings", p, 400)
	c.call("POST", "/api/users", map[string]any{"username": "regular", "password": "a-long-test-password", "role": "user"}, 201)
	c.call("POST", "/api/logout", map[string]any{}, 200)
	c.call("GET", "/api/status", nil, 200)
	c.call("POST", "/api/login", credentials{"regular", "a-long-test-password"}, 200)
	c.call("PUT", "/api/settings", defaultPreferences(), 403)
	c.call("GET", "/api/users", nil, 403)
}
func TestSessionStoresOnlyHashes(t *testing.T) {
	c := newClient(t)
	c.setup()
	var token, csrf string
	if e := c.db.QueryRow("SELECT token_hash,csrf_hash FROM sessions").Scan(&token, &csrf); e != nil {
		t.Fatal(e)
	}
	if len(token) != 64 || csrf == c.csrf || csrf != digest(c.csrf) {
		t.Fatal("session secrets stored unhashed")
	}
}
func TestHealthAndSecurityHeaders(t *testing.T) {
	c := newClient(t)
	c.call("GET", "/healthz", nil, 200)
	r, e := c.http.Get(c.srv.URL + "/api/status")
	if e != nil {
		t.Fatal(e)
	}
	defer r.Body.Close()
	if r.Header.Get("X-Frame-Options") != "DENY" || r.Header.Get("Content-Security-Policy") == "" {
		t.Fatal("missing security headers")
	}
}
func TestSSESnapshotAndShutdown(t *testing.T) {
	c := newClient(t)
	c.setup()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	r, _ := http.NewRequestWithContext(ctx, "GET", c.srv.URL+"/api/events", nil)
	resp, e := c.http.Do(r)
	if e != nil {
		t.Fatal(e)
	}
	defer resp.Body.Close()
	b := make([]byte, 2048)
	n, e := resp.Body.Read(b)
	if e != nil {
		t.Fatal(e)
	}
	if !bytes.Contains(b[:n], []byte("snapshot")) {
		t.Fatal(string(b[:n]))
	}
}
