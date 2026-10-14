package server

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
)

func TestResourceThresholds(t *testing.T) {
	for _, test := range []struct {
		free, critical uint64
		want           string
	}{
		{64 << 20, 64 << 20, "critical"}, {65 << 20, 64 << 20, "warning"}, {1 << 30, 64 << 20, "warning"}, {2 << 30, 64 << 20, "ok"}, {^uint64(0), ^uint64(0) - 1, "ok"},
	} {
		if got := diskLevel(test.free, test.critical); got != test.want {
			t.Fatal(test, got)
		}
	}
	for _, test := range []struct {
		used, free, limit int64
		want              string
	}{
		{89, 0, 100, "ok"}, {90, 0, 100, "warning"}, {98, 0, 100, "critical"}, {100, 20, 100, "ok"}, {100, 0, 0, "unknown"},
	} {
		if got := databaseLevel(test.used, test.free, test.limit); got != test.want {
			t.Fatal(test, got)
		}
	}
}

func TestResourcesAuthorizationAndWarningTransitions(t *testing.T) {
	c := newClient(t)
	c.server.Config.Storage.Data = t.TempDir()
	c.call("GET", "/api/resources", nil, 401)
	c.setup()
	status := c.call("GET", "/api/resources", nil, 200)
	if status["database_bytes"].(float64) <= 0 || status["heap_bytes"].(float64) <= 0 {
		t.Fatal(status)
	}
	// Trigger a real low-space measurement without filling the host disk.
	if _, err := c.db.Exec("UPDATE file_settings SET critical_free=?", int64(1<<62)); err != nil {
		t.Fatal(err)
	}
	status = c.call("GET", "/api/resources", nil, 200)
	found := false
	for _, item := range status["alerts"].([]any) {
		a := item.(map[string]any)
		if a["code"] == "storage_disk" && a["level"] == "critical" {
			found = true
		}
	}
	if !found {
		t.Fatal(status)
	}
	var output bytes.Buffer
	c.server.Log = slog.New(slog.NewJSONHandler(&output, nil))
	c.server.CheckResources(context.Background())
	first := output.Len()
	c.server.CheckResources(context.Background())
	if output.Len() != first {
		t.Fatal("duplicate warning logged")
	}
	if _, err := c.db.Exec("UPDATE file_settings SET critical_free=0"); err != nil {
		t.Fatal(err)
	}
	c.server.CheckResources(context.Background())
	if !strings.Contains(output.String(), "resource warning cleared") {
		t.Fatal(output.String())
	}
	c.call("POST", "/api/users", map[string]any{"username": "regular", "password": "another-long-password", "role": "user"}, 201)
	c.call("POST", "/api/logout", map[string]any{}, 200)
	c.call("GET", "/api/status", nil, 200)
	c.call("POST", "/api/login", credentials{"regular", "another-long-password"}, 200)
	c.call("GET", "/api/resources", nil, 403)
}
