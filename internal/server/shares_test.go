package server

import (
	"archive/zip"
	"bytes"
	"io"
	"net/http"
	"net/http/cookiejar"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func shareFixture(t *testing.T) (*testClient, *testClient) {
	t.Helper()
	c := newClient(t)
	c.setup()
	if e := os.MkdirAll(filepath.Join(c.server.Files.Root.Name(), "folder"), 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(c.server.Files.Root.Name(), "folder", "hello.txt"), []byte("guest content"), 0600); e != nil {
		t.Fatal(e)
	}
	jar, _ := cookiejar.New(nil)
	h := *c.srv.Client()
	h.Jar = jar
	guest := &testClient{t: t, srv: c.srv, http: &h, db: c.db}
	guest.call("GET", "/api/status", nil, 200)
	return c, guest
}
func makeShare(c *testClient, name, password string, limit int) map[string]any {
	return c.call("POST", "/api/shares", map[string]any{"path": name, "ttl_seconds": 3600, "password": password, "max_downloads": limit}, 201)
}
func guestEndpoint(v map[string]any) string {
	return "/api/guest/" + strings.TrimPrefix(v["url"].(string), "/s/")
}
func fetchShare(t *testing.T, c *testClient, target string, status int) []byte {
	t.Helper()
	resp, e := c.http.Get(c.srv.URL + target)
	if e != nil {
		t.Fatal(e)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != status {
		t.Fatalf("guest download: %d want %d: %s", resp.StatusCode, status, b)
	}
	return b
}
func TestGuestPasswordQuotaAndReplay(t *testing.T) {
	c, g := shareFixture(t)
	v := makeShare(c, "folder/hello.txt", "guest-password", 1)
	ep := guestEndpoint(v)
	meta := g.call("GET", ep, nil, 200)
	if meta["name"] != "Защищённый объект" {
		t.Fatal(meta)
	}
	g.call("GET", "/api/files", nil, 401)
	g.call("POST", ep+"/ticket", map[string]any{"password": "wrong"}, 401)
	ticket := g.call("POST", ep+"/ticket", map[string]any{"password": "guest-password"}, 201)["url"].(string)
	req, _ := http.NewRequest("HEAD", g.srv.URL+ticket, nil)
	resp, e := g.http.Do(req)
	if e != nil {
		t.Fatal(e)
	}
	resp.Body.Close()
	if resp.StatusCode != 405 {
		t.Fatal(resp.StatusCode)
	}
	b := fetchShare(t, g, ticket+"?path=other&inline=1", 200)
	if string(b) != "guest content" {
		t.Fatal(string(b))
	}
	fetchShare(t, g, ticket, 404)
	g.call("GET", ep, nil, 404)
	var stored string
	if e = c.db.QueryRow("SELECT token_hash FROM shares WHERE id=?", v["id"]).Scan(&stored); e != nil {
		t.Fatal(e)
	}
	if stored == strings.TrimPrefix(v["url"].(string), "/s/") {
		t.Fatal("raw token stored")
	}
}
func TestGuestFolderRevocationExpiryAndACL(t *testing.T) {
	c, g := shareFixture(t)
	v := makeShare(c, "folder", "", 0)
	ep := guestEndpoint(v)
	ticket := g.call("POST", ep+"/ticket", map[string]any{}, 201)["url"].(string)
	b := fetchShare(t, g, ticket, 200)
	z, e := zip.NewReader(bytes.NewReader(b), int64(len(b)))
	if e != nil || len(z.File) != 2 {
		t.Fatal(e)
	}
	ticket = g.call("POST", ep+"/ticket", map[string]any{}, 201)["url"].(string)
	c.call("DELETE", "/api/shares/"+v["id"].(string), map[string]any{}, 200)
	fetchShare(t, g, ticket, 404)
	v = makeShare(c, "folder", "", 0)
	ep = guestEndpoint(v)
	c.db.Exec("UPDATE shares SET expires=? WHERE id=?", time.Now().Unix()-1, v["id"])
	g.call("GET", ep, nil, 404)
	v = makeShare(c, "folder", "", 0)
	ep = guestEndpoint(v)
	ticket = g.call("POST", ep+"/ticket", map[string]any{}, 201)["url"].(string)
	// Existing links follow the creator's current role and descendant ACL.
	c.db.Exec("INSERT INTO file_acl(path,access) VALUES('folder/hello.txt','admin')")
	c.db.Exec("UPDATE users SET role='user'")
	fetchShare(t, g, ticket, 404)
	c.call("POST", "/api/shares", map[string]any{"path": "folder", "ttl_seconds": 3600}, 403)
}
func TestGuestAtomicQuotaAndPasswordThrottle(t *testing.T) {
	c, g := shareFixture(t)
	v := makeShare(c, "folder/hello.txt", "", 1)
	ep := guestEndpoint(v)
	a := g.call("POST", ep+"/ticket", map[string]any{}, 201)["url"].(string)
	b := g.call("POST", ep+"/ticket", map[string]any{}, 201)["url"].(string)
	var wg sync.WaitGroup
	codes := make(chan int, 2)
	for _, target := range []string{a, b} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp, e := g.http.Get(g.srv.URL + target)
			if e != nil {
				codes <- 0
				return
			}
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			codes <- resp.StatusCode
		}()
	}
	wg.Wait()
	close(codes)
	success := 0
	for code := range codes {
		if code == 200 {
			success++
		} else if code != 404 {
			t.Fatal(code)
		}
	}
	if success != 1 {
		t.Fatal(success)
	}
	v = makeShare(c, "folder/hello.txt", "guest-password", 0)
	ep = guestEndpoint(v)
	for i := 0; i < 10; i++ {
		g.call("POST", ep+"/ticket", map[string]any{"password": "wrong"}, 401)
	}
	g.call("POST", ep+"/ticket", map[string]any{"password": "guest-password"}, 429)
}
func TestGuestValidationAndOwnership(t *testing.T) {
	c, g := shareFixture(t)
	for _, p := range []string{".", "../outside", ".lanbridge"} {
		c.call("POST", "/api/shares", map[string]any{"path": p, "ttl_seconds": 3600}, 403)
	}
	c.call("POST", "/api/shares", map[string]any{"path": "folder", "ttl_seconds": 0}, 400)
	c.call("POST", "/api/shares", map[string]any{"path": "folder", "ttl_seconds": 3600, "password": "short"}, 400)
	v := makeShare(c, "folder", "", 0)
	c.call("POST", "/api/users", map[string]any{"username": "another", "password": "another-password", "role": "user"}, 201)
	g.call("POST", "/api/login", credentials{"another", "another-password"}, 200)
	g.call("DELETE", "/api/shares/"+v["id"].(string), map[string]any{}, 404)
	c.db.Exec("INSERT INTO file_acl(path,access) VALUES('folder','read')")
	g.call("POST", "/api/shares", map[string]any{"path": "folder", "ttl_seconds": 3600}, 403)
}
