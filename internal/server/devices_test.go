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

func TestDeviceChangesRequireAtomicAudit(t *testing.T) {
	c := newClient(t)
	c.setup()
	if _, e := c.db.Exec("INSERT INTO devices(id,identity,ip,first_seen,name) VALUES(1,'test','192.168.1.1',1,'Old')"); e != nil {
		t.Fatal(e)
	}
	config := map[string]any{"enabled": false, "subnet": "192.168.1.0/24", "interval": 900}
	device := map[string]any{"name": "New", "icon": "server", "known": true}
	c.call("POST", "/api/devices/config", config, 200)
	c.call("PUT", "/api/devices/1", device, 200)
	var count int
	if e := c.db.QueryRow("SELECT COUNT(*) FROM audit WHERE action IN ('discovery_config','device_updated') AND user_id=1").Scan(&count); e != nil || count != 2 {
		t.Fatal(count, e)
	}
	if _, e := c.db.Exec("CREATE TRIGGER fail_device_audit BEFORE INSERT ON audit BEGIN SELECT RAISE(ABORT,'audit unavailable'); END"); e != nil {
		t.Fatal(e)
	}
	config["interval"] = 1200
	device["name"] = "Lost"
	device["known"] = false
	c.call("POST", "/api/devices/config", config, 500)
	c.call("PUT", "/api/devices/1", device, 500)
	c.call("PUT", "/api/devices/999", device, 404)
	var interval int
	if e := c.db.QueryRow("SELECT interval FROM discovery_config WHERE id=1").Scan(&interval); e != nil || interval != 900 {
		t.Fatal(interval, e)
	}
	var name string
	var known bool
	if e := c.db.QueryRow("SELECT name,known FROM devices WHERE id=1").Scan(&name, &known); e != nil || name != "New" || !known {
		t.Fatal(name, known, e)
	}
}
