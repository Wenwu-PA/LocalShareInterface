package server

import (
	"bytes"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
)

func TestDeviceConfigurationAndDiagnostics(t *testing.T) {
	c := newClient(t)
	c.call("GET", "/api/devices", nil, 401)
	c.setup()
	result := c.call("GET", "/api/devices", nil, 200)
	if result["config"].(map[string]any)["enabled"].(bool) {
		t.Fatal("discovery enabled by default")
	}
	c.call("POST", "/api/devices/scan", map[string]any{"subnet": "8.8.8.0/24"}, 409)
	c.call("POST", "/api/devices/config", map[string]any{"enabled": true, "subnet": "192.168.0.0/16", "interval": 300}, 400)
	c.call("POST", "/api/devices/config", map[string]any{"enabled": false, "subnet": "", "interval": 300}, 200)
	if _, e := c.db.Exec("INSERT INTO devices(identity,ip,first_seen) VALUES('ip:192.168.1.1','192.168.1.1',1)"); e != nil {
		t.Fatal(e)
	}
	c.call("PUT", "/api/devices/1", map[string]any{"name": "Router", "icon": "router", "known": true}, 200)
	c.call("PUT", "/api/devices/1", map[string]any{"name": "Router", "icon": "", "known": true}, 400)
	c.call("PUT", "/api/devices/999", map[string]any{"name": "Router", "icon": "router", "known": true}, 404)
	listener, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	defer listener.Close()
	req, _ := http.NewRequest("POST", c.srv.URL+"/api/diagnostics", bytes.NewBufferString(`{"kind":"port","target":"`+listener.Addr().String()+`"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF-Token", c.csrf)
	response, e := c.http.Do(req)
	if e != nil {
		t.Fatal(e)
	}
	raw, e := io.ReadAll(response.Body)
	response.Body.Close()
	if e != nil || response.StatusCode != 200 || !strings.Contains(string(raw), "OK") || !strings.Contains(string(raw), `"kind":"done"`) {
		t.Fatal(e, response.StatusCode, string(raw))
	}
}
