package server

import "testing"

func TestDeviceSummary(t *testing.T) {
	c := newClient(t)
	c.call("GET", "/api/devices/summary", nil, 401)
	c.setup()
	empty := c.call("GET", "/api/devices/summary", nil, 200)
	if empty["total"] != float64(0) || empty["online"] != float64(0) || empty["enabled"] != false {
		t.Fatal(empty)
	}
	for _, query := range []string{
		`INSERT INTO devices(identity,ip,status,known,first_seen) VALUES('a','192.168.1.1','online',1,1),('b','192.168.1.2','offline',0,1),('c','192.168.1.3','unknown',0,1)`,
		`UPDATE discovery_config SET last_scan=123,last_error='fixture' WHERE id=1`,
	} {
		if _, err := c.db.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	check := func() {
		got := c.call("GET", "/api/devices/summary", nil, 200)
		for key, want := range map[string]float64{"total": 3, "online": 1, "offline": 1, "unknown": 1, "unrecognized": 2, "last_scan": 123} {
			if got[key] != want {
				t.Fatalf("%s: %v", key, got)
			}
		}
		if got["error"] != "fixture" {
			t.Fatal(got)
		}
	}
	check()
	c.call("POST", "/api/users", map[string]any{"username": "reader", "password": "another-long-password", "role": "user"}, 201)
	c.call("POST", "/api/logout", map[string]any{}, 200)
	c.call("GET", "/api/status", nil, 200)
	c.call("POST", "/api/login", credentials{"reader", "another-long-password"}, 200)
	check()
}
