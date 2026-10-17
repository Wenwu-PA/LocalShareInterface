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
	"sync/atomic"
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

func TestQueuedResultDoesNotApplyToChangedDefinition(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "test.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	d := definition()
	d.Kind = "heartbeat"
	d.Retries = 1
	raw, _ := json.Marshal(d)
	if _, err = db.Exec("INSERT INTO monitors(id,config) VALUES(1,?)", string(raw)); err != nil {
		t.Fatal(err)
	}
	queued := Probe(context.Background(), 1, d, 0)
	changed := d
	changed.Name = "Changed"
	changed.Interval = 10
	raw, _ = json.Marshal(changed)
	if _, err = db.Exec("UPDATE monitors SET config=? WHERE id=1", string(raw)); err != nil {
		t.Fatal(err)
	}
	events, err := WriteBatch(context.Background(), db, []Result{queued})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 0 {
		t.Fatal("queued result from old definition applied", events)
	}
	for _, table := range []string{"monitor_state", "samples", "aggregates", "incidents", "notification_queue"} {
		var count int
		if err = db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&count); err != nil || count != 0 {
			t.Fatal(table, count, err)
		}
	}
	current := Probe(context.Background(), 1, changed, time.Now().Unix())
	events, err = WriteBatch(context.Background(), db, []Result{current})
	if err != nil || len(events) != 1 || events[0].State.Status != "up" {
		t.Fatal(events, err)
	}
}

func TestQueuedResultFromCancelledWorkerIsIgnored(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "test.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	d := definition()
	d.Kind = "heartbeat"
	d.Retries = 1
	raw, _ := json.Marshal(d)
	if _, err = db.Exec("INSERT INTO monitors(id,config) VALUES(1,?)", string(raw)); err != nil {
		t.Fatal(err)
	}
	queued := Probe(context.Background(), 1, d, 0)
	queued.obsolete = &atomic.Bool{}
	queued.obsolete.Store(true)
	events, err := WriteBatch(context.Background(), db, []Result{queued})
	if err != nil || len(events) != 0 {
		t.Fatal("cancelled worker result applied", events, err)
	}
}

func TestReloadInvalidatesWorkersButShutdownFlushesCurrentResults(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "test.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	d := definition()
	d.Kind = "heartbeat"
	d.Retries = 1
	save := func() {
		t.Helper()
		raw, _ := json.Marshal(d)
		if _, err := db.Exec("INSERT INTO monitors(id,config) VALUES(19,?) ON CONFLICT(id) DO UPDATE SET config=excluded.config", string(raw)); err != nil {
			t.Fatal(err)
		}
	}
	save()
	engine := New(db, slog.New(slog.NewTextHandler(io.Discard, nil)), func(string, any) {})
	if err = engine.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	token := func() *atomic.Bool { engine.mu.Lock(); defer engine.mu.Unlock(); return engine.jobs[19].obsolete }
	first := token()
	if err = engine.Reload(); err != nil {
		t.Fatal(err)
	}
	if token() != first || first.Load() {
		t.Fatal("unchanged worker invalidated")
	}
	d.Name = "changed"
	save()
	if err = engine.Reload(); err != nil {
		t.Fatal(err)
	}
	second := token()
	if !first.Load() || second == first || second.Load() {
		t.Fatal("old worker not invalidated")
	}
	d.Paused = true
	save()
	if err = engine.Reload(); err != nil {
		t.Fatal(err)
	}
	if !second.Load() || token() != nil {
		t.Fatal("paused worker retained")
	}
	d.Paused = false
	save()
	if err = engine.Reload(); err != nil {
		t.Fatal(err)
	}
	current := token()
	result := Probe(context.Background(), 19, d, 0)
	result.obsolete = current
	engine.queue <- result
	engine.Close()
	if current.Load() {
		t.Fatal("normal shutdown invalidated completed results")
	}
	var status string
	if err = db.QueryRow("SELECT status FROM monitor_state WHERE monitor_id=19").Scan(&status); err != nil || status != "down" {
		t.Fatal("shutdown did not flush completed result", status, err)
	}
}
