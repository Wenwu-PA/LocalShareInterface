package server

import (
	"encoding/json"
	"lanbridge/internal/notify"
	"strings"
	"testing"
)

func TestNotificationConfigurationAndAuthorization(t *testing.T) {
	c := newClient(t)
	c.call("GET", "/api/notifications", nil, 401)
	c.setup()
	channel := notify.Channel{Name: "Local webhook", Kind: "webhook", Enabled: true, Down: true, Up: true, Cooldown: 60, URLenv: "LANBRIDGE_TEST_URL"}
	saved := c.call("POST", "/api/notifications/channels", channel, 200)
	id := int64(saved["id"].(float64))
	c.call("POST", "/api/notifications/test", map[string]any{"id": id}, 202)
	c.call("POST", "/api/notifications/test", map[string]any{"id": id}, 409)
	t.Setenv("LANBRIDGE_TEST_URL", "https://example.invalid/private-secret")
	result := c.call("GET", "/api/notifications", nil, 200)
	raw, _ := json.Marshal(result)
	if string(raw) == "" || len(result["channels"].([]any)) != 1 {
		t.Fatal(result)
	}
	if strings.Contains(string(raw), "private-secret") {
		t.Fatal("environment secret exposed")
	}
	channel.URLenv = "https://example.invalid/private-secret"
	c.call("POST", "/api/notifications/channels", channel, 400)
	c.call("POST", "/api/notifications/heartbeat", map[string]any{"enabled": true, "url_env": "bad"}, 400)
	c.call("POST", "/api/notifications/heartbeat", map[string]any{"enabled": true, "url_env": "LANBRIDGE_TEST_OBSERVER"}, 200)
	c.call("POST", "/api/incidents/999/notes", map[string]any{"message": "note"}, 404)
	c.call("POST", "/api/users", map[string]any{"username": "regular", "password": "another-long-password", "role": "user"}, 201)
	c.call("POST", "/api/logout", map[string]any{}, 200)
	c.call("GET", "/api/status", nil, 200)
	c.call("POST", "/api/login", credentials{"regular", "another-long-password"}, 200)
	c.call("GET", "/api/notifications", nil, 403)
	c.call("POST", "/api/notifications/test", map[string]any{"id": id}, 403)
}

func TestNotificationTestReadFailures(t *testing.T) {
	c := newClient(t)
	c.setup()
	channel := notify.Channel{Name: "Disabled", Kind: "webhook", Enabled: false, Cooldown: 60, URLenv: "LANBRIDGE_TEST_URL"}
	saved := c.call("POST", "/api/notifications/channels", channel, 200)
	id := saved["id"]
	c.call("POST", "/api/notifications/test", map[string]any{"id": 999}, 404)
	c.call("POST", "/api/notifications/test", map[string]any{"id": id}, 409)
	if _, err := c.db.Exec("UPDATE notification_channels SET config='invalid json' WHERE id=?", id); err != nil {
		t.Fatal(err)
	}
	c.call("POST", "/api/notifications/test", map[string]any{"id": id}, 500)
	if _, err := c.db.Exec("ALTER TABLE notification_channels RENAME COLUMN config TO unavailable_config"); err != nil {
		t.Fatal(err)
	}
	c.call("POST", "/api/notifications/test", map[string]any{"id": id}, 500)
	if _, err := c.db.Exec("ALTER TABLE notification_channels RENAME COLUMN unavailable_config TO config"); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{
		"SELECT COUNT(*) FROM notification_queue",
		"SELECT COUNT(*) FROM audit WHERE action='notification_test'",
	} {
		var count int
		if err := c.db.QueryRow(query).Scan(&count); err != nil || count != 0 {
			t.Fatalf("failed request left state: count=%d err=%v", count, err)
		}
	}
	channel.ID = int64(id.(float64))
	channel.Enabled = true
	c.call("POST", "/api/notifications/channels", channel, 200)
	c.call("POST", "/api/notifications/test", map[string]any{"id": id}, 202)
}

func TestNotificationSettingsAuditRollback(t *testing.T) {
	c := newClient(t)
	c.setup()
	channel := notify.Channel{Name: "Original", Kind: "webhook", Enabled: false, Down: true, Up: true, Cooldown: 60, URLenv: "LANBRIDGE_TEST_URL"}
	saved := c.call("POST", "/api/notifications/channels", channel, 200)
	channel.ID = int64(saved["id"].(float64))
	c.call("POST", "/api/notifications/heartbeat", map[string]any{"enabled": false, "url_env": "LANBRIDGE_ORIGINAL"}, 200)
	if _, err := c.db.Exec(`CREATE TRIGGER notification_audit_failure BEFORE INSERT ON audit WHEN NEW.action IN ('notification_channel','external_heartbeat') BEGIN SELECT RAISE(ABORT,'audit unavailable'); END`); err != nil {
		t.Fatal(err)
	}
	channel.Name = "Changed"
	channel.Enabled = true
	c.call("POST", "/api/notifications/channels", channel, 500)
	channel.ID = 0
	c.call("POST", "/api/notifications/channels", channel, 500)
	c.call("POST", "/api/notifications/heartbeat", map[string]any{"enabled": true, "url_env": "LANBRIDGE_CHANGED"}, 500)
	state := c.call("GET", "/api/notifications", nil, 200)
	channels := state["channels"].([]any)
	if len(channels) != 1 {
		t.Fatal(state)
	}
	old := channels[0].(map[string]any)
	if old["name"] != "Original" || old["enabled"] != false {
		t.Fatal(old)
	}
	observer := state["observer"].(map[string]any)
	if observer["enabled"] != false || observer["url_env"] != "LANBRIDGE_ORIGINAL" {
		t.Fatal(observer)
	}
	if _, err := c.db.Exec("DROP TRIGGER notification_audit_failure"); err != nil {
		t.Fatal(err)
	}
	c.call("POST", "/api/notifications/channels", channel, 200)
	c.call("POST", "/api/notifications/heartbeat", map[string]any{"enabled": true, "url_env": "LANBRIDGE_CHANGED"}, 200)
	state = c.call("GET", "/api/notifications", nil, 200)
	if len(state["channels"].([]any)) != 2 || state["observer"].(map[string]any)["enabled"] != true {
		t.Fatal(state)
	}
	channel.ID = 999
	c.call("POST", "/api/notifications/channels", channel, 404)
}

func TestNotificationQueueAndNotesRequireAudit(t *testing.T) {
	c := newClient(t)
	c.setup()
	channel := notify.Channel{Name: "Test", Kind: "webhook", Enabled: true, Down: true, Cooldown: 60, URLenv: "LANBRIDGE_TEST_URL"}
	saved := c.call("POST", "/api/notifications/channels", channel, 200)
	id := saved["id"]
	if _, err := c.db.Exec("INSERT INTO incidents(id,name,opened,message) VALUES(1,'test',1,'down')"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.db.Exec(`CREATE TRIGGER notification_action_failure BEFORE INSERT ON audit WHEN NEW.action IN ('notification_test','incident_note') BEGIN SELECT RAISE(ABORT,'audit unavailable'); END`); err != nil {
		t.Fatal(err)
	}
	c.call("POST", "/api/notifications/test", map[string]any{"id": id}, 500)
	c.call("POST", "/api/incidents/1/notes", map[string]any{"message": "private note"}, 500)
	for _, table := range []string{"notification_queue", "incident_events"} {
		var count int
		if err := c.db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&count); err != nil || count != 0 {
			t.Fatal(table, count, err)
		}
	}
	if _, err := c.db.Exec("DROP TRIGGER notification_action_failure"); err != nil {
		t.Fatal(err)
	}
	c.call("POST", "/api/notifications/test", map[string]any{"id": id}, 202)
	c.call("POST", "/api/incidents/1/notes", map[string]any{"message": "private note"}, 201)
	var count int
	if err := c.db.QueryRow("SELECT COUNT(*) FROM audit WHERE action IN ('notification_test','incident_note') AND detail NOT LIKE '%private note%'").Scan(&count); err != nil || count != 2 {
		t.Fatal(count, err)
	}
	c.call("POST", "/api/notifications/test", map[string]any{"id": id}, 409)
}
