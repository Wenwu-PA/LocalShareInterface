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
