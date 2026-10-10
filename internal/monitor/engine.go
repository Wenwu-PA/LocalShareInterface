package monitor

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"lanbridge/internal/notify"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"
)

type job struct {
	cancel context.CancelFunc
	config string
}
type Engine struct {
	db        *sql.DB
	log       *slog.Logger
	publish   func(string, any)
	mu        sync.Mutex
	ctx       context.Context
	cancel    context.CancelFunc
	jobs      map[int64]job
	workers   sync.WaitGroup
	writer    sync.WaitGroup
	queue     chan Result
	pool      chan struct{}
	Dropped   atomic.Uint64
	LastWrite atomic.Int64
}

func New(db *sql.DB, log *slog.Logger, publish func(string, any)) *Engine {
	return &Engine{db: db, log: log, publish: publish, jobs: map[int64]job{}, queue: make(chan Result, 256), pool: make(chan struct{}, 16)}
}
func (e *Engine) Start(parent context.Context) error {
	e.mu.Lock()
	if e.ctx != nil {
		e.mu.Unlock()
		return errors.New("engine already started")
	}
	e.ctx, e.cancel = context.WithCancel(parent)
	e.mu.Unlock()
	e.writer.Add(1)
	go e.writeLoop()
	return e.Reload()
}
func (e *Engine) Reload() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.ctx == nil || e.ctx.Err() != nil {
		return nil
	}
	list, err := List(e.ctx, e.db)
	if err != nil {
		return err
	}
	desired := map[int64]bool{}
	for _, m := range list {
		if m.Paused {
			continue
		}
		desired[m.ID] = true
		raw, _ := json.Marshal(m.Definition)
		if old, ok := e.jobs[m.ID]; ok {
			if old.config == string(raw) {
				continue
			}
			old.cancel()
		}
		ctx, cancel := context.WithCancel(e.ctx)
		e.jobs[m.ID] = job{cancel, string(raw)}
		e.workers.Add(1)
		go e.run(ctx, m)
	}
	for id, j := range e.jobs {
		if !desired[id] {
			j.cancel()
			delete(e.jobs, id)
		}
	}
	return nil
}
func (e *Engine) run(ctx context.Context, m Monitor) {
	defer e.workers.Done()
	defer func() {
		if recover() != nil {
			e.log.Error("monitor worker panic", "monitor_id", m.ID)
		}
	}()
	timer := time.NewTimer(time.Duration(m.ID%20) * 50 * time.Millisecond)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			select {
			case e.pool <- struct{}{}:
			case <-ctx.Done():
				return
			}
			heartbeat := int64(0)
			if m.Kind == "heartbeat" {
				if err := e.db.QueryRowContext(ctx, "SELECT heartbeat_at FROM monitors WHERE id=?", m.ID).Scan(&heartbeat); err != nil {
					<-e.pool
					if ctx.Err() != nil {
						return
					}
					timer.Reset(time.Duration(m.Interval) * time.Second)
					continue
				}
			}
			started := time.Now()
			result := Probe(ctx, m.ID, m.Definition, heartbeat)
			<-e.pool
			if ctx.Err() != nil {
				return
			}
			select {
			case e.queue <- result:
			default:
				e.Dropped.Add(1)
				e.log.Warn("monitor result queue full", "monitor_id", m.ID)
			}
			remaining := time.Until(started.Add(time.Duration(m.Interval) * time.Second))
			if remaining < time.Millisecond {
				remaining = time.Millisecond
			}
			timer.Reset(remaining)
		}
	}
}
func (e *Engine) Close() {
	e.mu.Lock()
	if e.cancel == nil {
		e.mu.Unlock()
		return
	}
	e.cancel()
	e.cancel = nil
	e.mu.Unlock()
	e.workers.Wait()
	close(e.queue)
	e.writer.Wait()
	httpTransport.CloseIdleConnections()
}
func (e *Engine) QueueDepth() int { return len(e.queue) }
func List(ctx context.Context, db *sql.DB) ([]Monitor, error) {
	rows, err := db.QueryContext(ctx, `SELECT m.id,m.config,COALESCE(s.status,'pending'),COALESCE(s.checked,0),COALESCE(s.latency,0),COALESCE(s.failures,0),COALESCE(s.message,'') FROM monitors m LEFT JOIN monitor_state s ON m.id=s.monitor_id ORDER BY CAST(json_extract(m.config,'$.order') AS INTEGER),m.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list := []Monitor{}
	for rows.Next() {
		var m Monitor
		var raw string
		if err = rows.Scan(&m.ID, &raw, &m.State.Status, &m.State.Checked, &m.State.Latency, &m.State.Failures, &m.State.Message); err != nil {
			return nil, err
		}
		if err = json.Unmarshal([]byte(raw), &m.Definition); err != nil {
			return nil, err
		}
		if m.Paused {
			m.State.Status = "paused"
		}
		list = append(list, m)
	}
	return list, rows.Err()
}
func (e *Engine) writeLoop() {
	defer e.writer.Done()
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	batch := make([]Result, 0, 128)
	flush := func() {
		if len(batch) == 0 {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		events, err := WriteBatch(ctx, e.db, batch)
		if err != nil {
			e.Dropped.Add(uint64(len(batch)))
			e.log.Error("monitor batch failed", "count", len(batch), "error", err)
		} else {
			e.LastWrite.Store(time.Now().Unix())
			for _, m := range events {
				e.publish("monitor", map[string]any{"id": m.ID, "state": m.State})
			}
		}
		batch = batch[:0]
	}
	for {
		select {
		case r, ok := <-e.queue:
			if !ok {
				flush()
				return
			}
			batch = append(batch, r)
			if len(batch) >= 128 {
				flush()
			}
		case <-ticker.C:
			flush()
		}
	}
}
func WriteBatch(ctx context.Context, db *sql.DB, results []Result) ([]Monitor, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	updated := []Monitor{}
	for _, r := range results {
		var m Monitor
		var raw string
		m.ID = r.ID
		err = tx.QueryRowContext(ctx, "SELECT config FROM monitors WHERE id=?", r.ID).Scan(&raw)
		if err == sql.ErrNoRows {
			continue
		}
		if err != nil {
			return nil, err
		}
		if err = json.Unmarshal([]byte(raw), &m.Definition); err != nil {
			return nil, err
		}
		if m.Paused {
			continue
		}
		err = tx.QueryRowContext(ctx, "SELECT status,checked,latency,failures,message FROM monitor_state WHERE monitor_id=?", r.ID).Scan(&m.State.Status, &m.State.Checked, &m.State.Latency, &m.State.Failures, &m.State.Message)
		if err != nil && err != sql.ErrNoRows {
			return nil, err
		}
		if r.At < m.State.Checked {
			continue
		}
		previous := m.State.Status
		if r.OK {
			m.State.Status = "up"
			m.State.Failures = 0
		} else {
			m.State.Failures++
			if m.State.Failures >= m.Retries {
				m.State.Status = "down"
			} else if m.State.Status == "" {
				m.State.Status = "pending"
			}
		}
		m.State.Checked = r.At
		m.State.Latency = r.Latency
		m.State.Message = r.Message
		if _, err = tx.ExecContext(ctx, `INSERT INTO monitor_state(monitor_id,status,checked,latency,failures,message) VALUES(?,?,?,?,?,?) ON CONFLICT(monitor_id) DO UPDATE SET status=excluded.status,checked=excluded.checked,latency=excluded.latency,failures=excluded.failures,message=excluded.message`, r.ID, m.State.Status, r.At, r.Latency, m.State.Failures, r.Message); err != nil {
			return nil, err
		}
		if _, err = tx.ExecContext(ctx, "INSERT INTO samples VALUES(?,?,?,?)", r.ID, r.At, r.OK, r.Latency); err != nil {
			return nil, err
		}
		up := 0
		if r.OK {
			up = 1
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO aggregates VALUES(?,?,60,1,?,?) ON CONFLICT(monitor_id,bucket,resolution) DO UPDATE SET total=total+1,up=up+excluded.up,latency_sum=latency_sum+excluded.latency_sum`, r.ID, r.At/60*60, up, r.Latency); err != nil {
			return nil, err
		}
		if m.State.Status == "down" && previous != "down" {
			res, e := tx.ExecContext(ctx, "INSERT OR IGNORE INTO incidents(monitor_id,name,opened,message) VALUES(?,?,?,?)", r.ID, m.Name, r.At, r.Message)
			if e != nil {
				return nil, e
			}
			affected, _ := res.RowsAffected()
			if affected > 0 {
				id, _ := res.LastInsertId()
				if err = notify.Enqueue(ctx, tx, notify.Event{ID: id, MonitorID: r.ID, Kind: "down", Name: m.Name, Message: r.Message, At: r.At}); err != nil {
					return nil, err
				}
				if _, err = tx.ExecContext(ctx, "INSERT INTO incident_events(incident_id,at,kind,message) VALUES(?,?,'opened',?)", id, r.At, r.Message); err != nil {
					return nil, err
				}
			}
		}
		if r.OK {
			var openID int64
			if e := tx.QueryRowContext(ctx, "SELECT id FROM incidents WHERE monitor_id=? AND closed IS NULL", r.ID).Scan(&openID); e == nil {
				if err = notify.Enqueue(ctx, tx, notify.Event{ID: openID, MonitorID: r.ID, Kind: "up", Name: m.Name, Message: "Соединение восстановлено", At: r.At}); err != nil {
					return nil, err
				}
			} else if e != sql.ErrNoRows {
				return nil, e
			}
			if _, err = tx.ExecContext(ctx, "INSERT INTO incident_events(incident_id,at,kind,message) SELECT id,?,'recovered','Соединение восстановлено' FROM incidents WHERE monitor_id=? AND closed IS NULL", r.At, r.ID); err != nil {
				return nil, err
			}
			if _, err = tx.ExecContext(ctx, "UPDATE incidents SET closed=? WHERE monitor_id=? AND closed IS NULL", r.At, r.ID); err != nil {
				return nil, err
			}
		}
		updated = append(updated, m)
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return updated, nil
}

func Compact(ctx context.Context, db *sql.DB, now time.Time) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, step := range []struct {
		from, to int
		before   int64
	}{{60, 3600, now.Add(-48*time.Hour).Unix() / 3600 * 3600}, {3600, 86400, now.Add(-30*24*time.Hour).Unix() / 86400 * 86400}} {
		if _, err = tx.ExecContext(ctx, `INSERT INTO aggregates SELECT monitor_id,(bucket/?)*?,?,SUM(total),SUM(up),SUM(latency_sum) FROM aggregates WHERE resolution=? AND bucket<? GROUP BY monitor_id,bucket/? ON CONFLICT(monitor_id,bucket,resolution) DO UPDATE SET total=total+excluded.total,up=up+excluded.up,latency_sum=latency_sum+excluded.latency_sum`, step.to, step.to, step.to, step.from, step.before, step.to); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, "DELETE FROM aggregates WHERE resolution=? AND bucket<?", step.from, step.before); err != nil {
			return err
		}
	}
	if _, err = tx.ExecContext(ctx, "DELETE FROM samples WHERE at<?", now.Add(-48*time.Hour).Unix()); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "DELETE FROM aggregates WHERE bucket<?", now.Add(-365*24*time.Hour).Unix()); err != nil {
		return err
	}
	return tx.Commit()
}
