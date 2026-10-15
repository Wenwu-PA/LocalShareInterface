package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"lanbridge/internal/notify"
)

func TestResourceDeliveryTransitionsAndOptIn(t *testing.T) {
	c := newClient(t)
	c.setup()
	events := make(chan notify.Event, 8)
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var event notify.Event
		if err := json.NewDecoder(r.Body).Decode(&event); err != nil {
			t.Error(err)
		}
		events <- event
		w.WriteHeader(204)
	}))
	defer receiver.Close()
	t.Setenv("LANBRIDGE_TEST_RESOURCES", receiver.URL)
	channel := notify.Channel{Name: "Resources", Kind: "webhook", Enabled: true, Resources: true, Cooldown: 900, URLenv: "LANBRIDGE_TEST_RESOURCES"}
	c.call("POST", "/api/notifications/channels", channel, 200)
	channel.Name = "No opt in"
	channel.Resources = false
	c.call("POST", "/api/notifications/channels", channel, 200)
	ctx := context.Background()
	for _, level := range []string{"warning", "warning", "critical", ""} {
		status := resourceSnapshot{Alerts: []resourceAlert{}}
		if level != "" {
			status.Alerts = append(status.Alerts, resourceAlert{"storage_disk", level, "Low space fixture"})
		}
		// No in-memory state is used for deduplication.
		c.server.resourceLevels = nil
		if err := c.server.queueResourceTransitions(ctx, status); err != nil {
			t.Fatal(err)
		}
	}
	var count int
	if err := c.db.QueryRow("SELECT COUNT(*) FROM notification_queue").Scan(&count); err != nil || count != 3 {
		t.Fatal(count, err)
	}
	for _, kind := range []string{"resource_warning", "resource_critical", "resource_recovered"} {
		if err := c.server.Notifications.Step(ctx); err != nil {
			t.Fatal(err)
		}
		select {
		case event := <-events:
			if event.Kind != kind || event.MonitorID != -1003 {
				t.Fatal(event)
			}
		default:
			t.Fatal("no delivery")
		}
	}
}

func TestResourceQueueFailureKeepsPreviousState(t *testing.T) {
	c := newClient(t)
	c.setup()
	c.call("POST", "/api/notifications/channels", notify.Channel{Name: "Resources", Kind: "webhook", Enabled: true, Resources: true, Cooldown: 5, URLenv: "LANBRIDGE_TEST_RESOURCES"}, 200)
	if _, err := c.db.Exec("CREATE TRIGGER fail_queue BEFORE INSERT ON notification_queue BEGIN SELECT RAISE(ABORT,'queue unavailable'); END"); err != nil {
		t.Fatal(err)
	}
	status := resourceSnapshot{Alerts: []resourceAlert{{"database", "critical", "Full fixture"}}}
	if err := c.server.queueResourceTransitions(context.Background(), status); err == nil {
		t.Fatal("queue failure ignored")
	}
	var count int
	if err := c.db.QueryRow("SELECT COUNT(*) FROM settings WHERE key='resource_alert_state'").Scan(&count); err != nil || count != 0 {
		t.Fatal(count, err)
	}
	if _, err := c.db.Exec("DROP TRIGGER fail_queue"); err != nil {
		t.Fatal(err)
	}
	if err := c.server.queueResourceTransitions(context.Background(), status); err != nil {
		t.Fatal(err)
	}
	if err := c.db.QueryRow("SELECT COUNT(*) FROM notification_queue").Scan(&count); err != nil || count != 1 {
		t.Fatal(count, err)
	}
}
