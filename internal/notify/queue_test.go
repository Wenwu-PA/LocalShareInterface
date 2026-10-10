package notify

import (
	"context"
	"encoding/json"
	"io"
	"lanbridge/internal/store"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestWebhookRetryAndDurableQueue(t *testing.T) {
	var healthy atomic.Bool
	var received atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !healthy.Load() {
			w.WriteHeader(503)
			return
		}
		received.Add(1)
		w.WriteHeader(204)
	}))
	defer srv.Close()
	t.Setenv("LANBRIDGE_TEST_WEBHOOK", srv.URL)
	path := filepath.Join(t.TempDir(), "notify.sqlite3")
	db, e := store.Open(path)
	if e != nil {
		t.Fatal(e)
	}
	defer func() { db.Close() }()
	channel := Channel{Name: "Local test", Kind: "webhook", Enabled: true, Down: true, Up: true, Cooldown: 60, URLenv: "LANBRIDGE_TEST_WEBHOOK"}
	raw, _ := json.Marshal(channel)
	_, e = db.Exec("INSERT INTO notification_channels(config) VALUES(?)", string(raw))
	if e != nil {
		t.Fatal(e)
	}
	event := Event{ID: 1, MonitorID: 1, Kind: "down", Name: "Local", Message: "offline", At: time.Now().Unix()}
	tx, e := db.Begin()
	if e != nil {
		t.Fatal(e)
	}
	if e = Enqueue(context.Background(), tx, event); e != nil {
		t.Fatal(e)
	}
	if e = tx.Commit(); e != nil {
		t.Fatal(e)
	}
	w := New(db, slog.New(slog.NewTextHandler(io.Discard, nil)), func(string, any) {})
	if e = w.Step(context.Background()); e != nil {
		t.Fatal(e)
	}
	var status string
	var attempts int
	_ = db.QueryRow("SELECT status,attempts FROM notification_queue").Scan(&status, &attempts)
	if status != "pending" || attempts != 1 {
		t.Fatal(status, attempts)
	}
	db.Close()
	db, e = store.Open(path)
	if e != nil {
		t.Fatal(e)
	}
	healthy.Store(true)
	_, e = db.Exec("UPDATE notification_queue SET next_at=0")
	if e != nil {
		t.Fatal(e)
	}
	w = New(db, slog.New(slog.NewTextHandler(io.Discard, nil)), func(string, any) {})
	if e = w.Step(context.Background()); e != nil {
		t.Fatal(e)
	}
	_ = db.QueryRow("SELECT status,attempts FROM notification_queue").Scan(&status, &attempts)
	if status != "delivered" || attempts != 2 || received.Load() != 1 {
		t.Fatal(status, attempts, received.Load())
	}
}
func TestFlappingAndHeartbeat(t *testing.T) {
	db, e := store.Open(filepath.Join(t.TempDir(), "notify.sqlite3"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	c := Channel{Name: "Test", Kind: "webhook", Enabled: true, Down: true, Up: true, Cooldown: 60, URLenv: "LANBRIDGE_TEST_WEBHOOK"}
	raw, _ := json.Marshal(c)
	_, e = db.Exec("INSERT INTO notification_channels(config) VALUES(?)", string(raw))
	if e != nil {
		t.Fatal(e)
	}
	tx, _ := db.Begin()
	now := time.Now().Unix()
	for _, event := range []Event{{ID: 1, MonitorID: 1, Kind: "down", At: now}, {ID: 1, MonitorID: 1, Kind: "up", At: now + 1}, {ID: 2, MonitorID: 1, Kind: "down", At: now + 2}} {
		if e = Enqueue(context.Background(), tx, event); e != nil {
			t.Fatal(e)
		}
	}
	if e = tx.Commit(); e != nil {
		t.Fatal(e)
	}
	var count int
	_ = db.QueryRow("SELECT COUNT(*) FROM notification_queue").Scan(&count)
	if count != 2 {
		t.Fatal(count)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	defer srv.Close()
	t.Setenv("LANBRIDGE_TEST_OBSERVER", srv.URL)
	if e = Heartbeat(context.Background(), "LANBRIDGE_TEST_OBSERVER"); e != nil {
		t.Fatal(e)
	}
}
func TestChannelValidationAndSecretsNotReturned(t *testing.T) {
	c := Channel{Name: "test", Kind: "webhook", Cooldown: 60, URLenv: "https://secret.example/token"}
	if Validate(c) == nil {
		t.Fatal("accepted secret instead of env reference")
	}
	c.URLenv = "LANBRIDGE_TEST_SECRET"
	if e := Validate(c); e != nil {
		t.Fatal(e)
	}
	t.Setenv(c.URLenv, "http://remote.example/secret-token")
	e := Deliver(context.Background(), c, Event{})
	if e == nil {
		t.Fatal("accepted unencrypted remote webhook")
	}
	if strings.Contains(e.Error(), "secret-token") {
		t.Fatal("secret exposed")
	}
}

func TestLargePendingQueueDoesNotRejectIncident(t *testing.T) {
	db, e := store.Open(filepath.Join(t.TempDir(), "queue.sqlite3"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	c := Channel{Name: "Offline", Kind: "webhook", Enabled: true, Down: true, Cooldown: 5, URLenv: "LANBRIDGE_TEST_URL"}
	raw, _ := json.Marshal(c)
	if _, e = db.Exec("INSERT INTO notification_channels(config) VALUES(?)", string(raw)); e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec(`WITH RECURSIVE n(x) AS (SELECT 1 UNION ALL SELECT x+1 FROM n WHERE x<10000) INSERT INTO notification_queue(channel_id,event_id,kind,payload,next_at,created_at) SELECT 1,x,'test','{}',0,0 FROM n`); e != nil {
		t.Fatal(e)
	}
	tx, e := db.Begin()
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback()
	if e = Enqueue(context.Background(), tx, Event{ID: 10001, MonitorID: 1, Kind: "down", At: time.Now().Unix()}); e != nil {
		t.Fatal(e)
	}
	if e = tx.Commit(); e != nil {
		t.Fatal(e)
	}
	var count int
	if e = db.QueryRow("SELECT COUNT(*) FROM notification_queue").Scan(&count); e != nil || count != 10001 {
		t.Fatal(e, count)
	}
}
