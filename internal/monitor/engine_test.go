package monitor

import (
	"context"
	"encoding/json"
	"io"
	"lanbridge/internal/store"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"
)

func definition() Definition {
	return Definition{Name: "test", Kind: "tcp", Target: "127.0.0.1:1", Interval: 5, Timeout: 1, Retries: 3, ExpectedStatus: 200, Method: "GET", Record: "A"}
}
func TestProbeHTTPAndTCP(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "healthy") }))
	defer srv.Close()
	d := definition()
	d.Kind = "http"
	d.Target = srv.URL
	d.Keyword = "healthy"
	if err := Validate(d); err != nil {
		t.Fatal(err)
	}
	if r := Probe(context.Background(), 1, d, 0); !r.OK {
		t.Fatal(r)
	}
	d.Keyword = "absent"
	if r := Probe(context.Background(), 1, d, 0); r.OK {
		t.Fatal("keyword accepted")
	}
	d.Kind = "tcp"
	d.Target = srv.Listener.Addr().String()
	if r := Probe(context.Background(), 1, d, 0); !r.OK {
		t.Fatal(r)
	}
}
func TestProbeTimeoutAndHeartbeat(t *testing.T) {
	d := definition()
	d.Kind = "heartbeat"
	if Probe(context.Background(), 1, d, 0).OK {
		t.Fatal("missing heartbeat accepted")
	}
	if !Probe(context.Background(), 1, d, time.Now().Unix()).OK {
		t.Fatal("heartbeat rejected")
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer srv.Close()
	d.Kind = "http"
	d.Target = srv.URL
	start := time.Now()
	if Probe(context.Background(), 1, d, 0).OK {
		t.Fatal("timeout accepted")
	}
	if time.Since(start) > 2*time.Second {
		t.Fatal("timeout not bounded")
	}
}
func TestDefinitionValidation(t *testing.T) {
	for _, change := range []func(*Definition){func(d *Definition) { d.Interval = 1 }, func(d *Definition) { d.Timeout = 99 }, func(d *Definition) { d.Kind = "icmp"; d.Target = "-n 20" }, func(d *Definition) { d.Kind = "http"; d.Target = "file:///etc/passwd" }, func(d *Definition) { d.Kind = "dns"; d.Record = "ANY" }} {
		d := definition()
		change(&d)
		if Validate(d) == nil {
			t.Fatal(d)
		}
	}
}
func TestIncidentThresholdRecoveryAndAggregates(t *testing.T) {
	db, e := store.Open(filepath.Join(t.TempDir(), "test.sqlite3"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	d := definition()
	raw, _ := json.Marshal(d)
	res, e := db.Exec("INSERT INTO monitors(config) VALUES(?)", string(raw))
	if e != nil {
		t.Fatal(e)
	}
	id, _ := res.LastInsertId()
	now := time.Now().Add(-72 * time.Hour).Unix()
	batch := []Result{}
	for i := 0; i < 3; i++ {
		batch = append(batch, Result{ID: id, At: now + int64(i), OK: false, Latency: 1, Message: "down", Retries: 3})
	}
	if _, e = WriteBatch(context.Background(), db, batch[:2]); e != nil {
		t.Fatal(e)
	}
	var count int
	_ = db.QueryRow("SELECT COUNT(*) FROM incidents").Scan(&count)
	if count != 0 {
		t.Fatal("incident before threshold")
	}
	if _, e = WriteBatch(context.Background(), db, batch[2:]); e != nil {
		t.Fatal(e)
	}
	_ = db.QueryRow("SELECT COUNT(*) FROM incidents WHERE closed IS NULL").Scan(&count)
	if count != 1 {
		t.Fatal(count)
	}
	if _, e = WriteBatch(context.Background(), db, []Result{{ID: id, At: now + 3, OK: true, Latency: 2, Retries: 3}}); e != nil {
		t.Fatal(e)
	}
	_ = db.QueryRow("SELECT COUNT(*) FROM incidents WHERE closed IS NOT NULL").Scan(&count)
	if count != 1 {
		t.Fatal(count)
	}
	for i := 0; i < 2; i++ {
		if e = Compact(context.Background(), db, time.Now()); e != nil {
			t.Fatal(e)
		}
	}
	var total, up int
	_ = db.QueryRow("SELECT SUM(total),SUM(up) FROM aggregates").Scan(&total, &up)
	if total != 4 || up != 1 {
		t.Fatal(total, up)
	}
	_ = db.QueryRow("SELECT COUNT(*) FROM samples").Scan(&count)
	if count != 0 {
		t.Fatal("raw samples not removed")
	}
}
func TestEngineStartsStopsAndPreservesUnchangedWorker(t *testing.T) {
	listener, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	defer listener.Close()
	go func() {
		for {
			c, e := listener.Accept()
			if e != nil {
				return
			}
			c.Close()
		}
	}()
	db, e := store.Open(filepath.Join(t.TempDir(), "test.sqlite3"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	d := definition()
	d.Target = listener.Addr().String()
	raw, _ := json.Marshal(d)
	_, e = db.Exec("INSERT INTO monitors(config) VALUES(?)", string(raw))
	if e != nil {
		t.Fatal(e)
	}
	events := make(chan any, 5)
	engine := New(db, slog.New(slog.NewTextHandler(io.Discard, nil)), func(_ string, v any) { events <- v })
	if e = engine.Start(context.Background()); e != nil {
		t.Fatal(e)
	}
	defer engine.Close()
	if e = engine.Reload(); e != nil {
		t.Fatal(e)
	}
	select {
	case <-events:
	case <-time.After(3 * time.Second):
		t.Fatal("no measurement")
	}
}
