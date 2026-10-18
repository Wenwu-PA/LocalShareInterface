package server

import "testing"

func TestDefaultMonitorIntervalPreferences(t *testing.T) {
	c := newClient(t)
	c.setup()
	p := c.call("GET", "/api/settings", nil, 200)
	if p["monitor_interval"] != float64(20) {
		t.Fatal(p)
	}
	for _, interval := range []int{5, 120, 86400} {
		p["monitor_interval"] = interval
		c.call("PUT", "/api/settings", p, 200)
		if got := c.call("GET", "/api/settings", nil, 200)["monitor_interval"]; got != float64(interval) {
			t.Fatal(got)
		}
	}
	for _, invalid := range []any{0, 4, -1, 86401, 5.5, "120"} {
		p["monitor_interval"] = invalid
		c.call("PUT", "/api/settings", p, 400)
		if got := c.call("GET", "/api/settings", nil, 200)["monitor_interval"]; got != float64(86400) {
			t.Fatal("invalid update changed preference", got)
		}
	}
	delete(p, "monitor_interval")
	c.call("PUT", "/api/settings", p, 200)
	if got := c.call("GET", "/api/settings", nil, 200)["monitor_interval"]; got != float64(20) {
		t.Fatal("legacy request lost default", got)
	}
	if _, err := c.db.Exec(`UPDATE settings SET value=json_remove(value,'$.monitor_interval') WHERE key='appearance'`); err != nil {
		t.Fatal(err)
	}
	if got := c.call("GET", "/api/settings", nil, 200)["monitor_interval"]; got != float64(20) {
		t.Fatal("legacy settings lost default", got)
	}
	if _, err := c.db.Exec(`CREATE TRIGGER refuse_settings_audit BEFORE INSERT ON audit WHEN NEW.action='settings' BEGIN SELECT RAISE(ABORT,'audit unavailable'); END`); err != nil {
		t.Fatal(err)
	}
	p["monitor_interval"] = 120
	c.call("PUT", "/api/settings", p, 500)
	if got := c.call("GET", "/api/settings", nil, 200)["monitor_interval"]; got != float64(20) {
		t.Fatal("audit failure changed preference", got)
	}
}
