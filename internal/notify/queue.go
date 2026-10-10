package notify

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// Enqueue runs in the incident transaction, so a committed transition has durable delivery jobs.
func Enqueue(ctx context.Context, tx *sql.Tx, event Event) error {
	rows, e := tx.QueryContext(ctx, "SELECT id,config FROM notification_channels")
	if e != nil {
		return e
	}
	channels := []Channel{}
	for rows.Next() {
		var c Channel
		var raw string
		if e = rows.Scan(&c.ID, &raw); e != nil {
			rows.Close()
			return e
		}
		id := c.ID
		if e = json.Unmarshal([]byte(raw), &c); e != nil {
			rows.Close()
			return e
		}
		c.ID = id
		if c.Enabled && ((event.Kind == "down" && c.Down) || (event.Kind == "up" && c.Up)) {
			channels = append(channels, c)
		}
	}
	if e = rows.Err(); e != nil {
		rows.Close()
		return e
	}
	rows.Close()
	payload, _ := json.Marshal(event)
	for _, c := range channels {
		var previous int64
		e = tx.QueryRowContext(ctx, "SELECT last_at FROM notification_cooldown WHERE channel_id=? AND monitor_id=? AND kind=?", c.ID, event.MonitorID, event.Kind).Scan(&previous)
		if e != nil && e != sql.ErrNoRows {
			return e
		}
		if event.At-previous < int64(c.Cooldown) {
			continue
		}
		if _, e = tx.ExecContext(ctx, "INSERT OR IGNORE INTO notification_queue(channel_id,event_id,kind,payload,next_at,created_at) VALUES(?,?,?,?,?,?)", c.ID, event.ID, event.Kind, string(payload), event.At, event.At); e != nil {
			return e
		}
		if _, e = tx.ExecContext(ctx, "INSERT INTO notification_cooldown VALUES(?,?,?,?) ON CONFLICT(channel_id,monitor_id,kind) DO UPDATE SET last_at=excluded.last_at", c.ID, event.MonitorID, event.Kind, event.At); e != nil {
			return e
		}
	}
	return nil
}

type Worker struct {
	db      *sql.DB
	log     *slog.Logger
	wg      sync.WaitGroup
	cancel  context.CancelFunc
	publish func(string, any)
}

func New(db *sql.DB, log *slog.Logger, publish func(string, any)) *Worker {
	return &Worker{db: db, log: log, publish: publish}
}
func (w *Worker) Start(parent context.Context) {
	ctx, cancel := context.WithCancel(parent)
	w.cancel = cancel
	w.wg.Add(1)
	go func() {
		defer w.wg.Done()
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		nextHeartbeat := time.Now()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := w.Step(ctx); err != nil && ctx.Err() == nil {
					w.log.Error("notification queue failed", "error", err)
				}
				if time.Now().After(nextHeartbeat) {
					w.heartbeat(ctx)
					nextHeartbeat = time.Now().Add(30 * time.Second)
				}
			}
		}
	}()
}
func (w *Worker) Close() {
	if w.cancel != nil {
		w.cancel()
		w.wg.Wait()
	}
}
func (w *Worker) Step(ctx context.Context) (stepErr error) {
	defer func() {
		if recover() != nil {
			stepErr = fmt.Errorf("notification worker panic; pending delivery retained")
		}
	}()
	var id, channelID int64
	var config, payload string
	var attempts int
	err := w.db.QueryRowContext(ctx, `SELECT q.id,c.id,c.config,q.payload,q.attempts FROM notification_queue q JOIN notification_channels c ON q.channel_id=c.id WHERE q.status='pending' AND q.next_at<=? AND json_extract(c.config,'$.enabled')=1 ORDER BY q.id LIMIT 1`, time.Now().Unix()).Scan(&id, &channelID, &config, &payload, &attempts)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return err
	}
	var c Channel
	var event Event
	if err = json.Unmarshal([]byte(config), &c); err != nil {
		return err
	}
	c.ID = channelID
	if err = json.Unmarshal([]byte(payload), &event); err != nil {
		return err
	}
	delivery, cancel := context.WithTimeout(ctx, 10*time.Second)
	err = Deliver(delivery, c, event)
	cancel()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err == nil {
		_, err = w.db.ExecContext(ctx, "UPDATE notification_queue SET status='delivered',attempts=attempts+1,delivered_at=?,last_error='' WHERE id=?", time.Now().Unix(), id)
	} else {
		message := err.Error()
		delay := int64(5) << min(attempts, 10)
		if delay > 3600 {
			delay = 3600
		}
		_, err = w.db.ExecContext(ctx, "UPDATE notification_queue SET attempts=attempts+1,next_at=?,last_error=? WHERE id=?", time.Now().Unix()+delay, message, id)
	}
	if err == nil {
		w.publish("delivery", map[string]any{"id": id})
		_, err = w.db.ExecContext(ctx, "DELETE FROM notification_queue WHERE status='delivered' AND delivered_at<?", time.Now().Add(-30*24*time.Hour).Unix())
	}
	return err
}
func (w *Worker) heartbeat(ctx context.Context) {
	defer func() {
		if recover() != nil {
			w.log.Error("heartbeat worker panic; retry scheduled")
		}
	}()
	var enabled bool
	var env string
	if e := w.db.QueryRowContext(ctx, "SELECT enabled,url_env FROM external_heartbeat WHERE id=1").Scan(&enabled, &env); e != nil || !enabled {
		return
	}
	check, cancel := context.WithTimeout(ctx, 10*time.Second)
	e := Heartbeat(check, env)
	cancel()
	now := time.Now().Unix()
	message := ""
	if e != nil {
		message = e.Error()
	}
	var err error
	if e == nil {
		_, err = w.db.ExecContext(ctx, "UPDATE external_heartbeat SET last_attempt=?,last_success=?,last_error='' WHERE id=1", now, now)
	} else {
		_, err = w.db.ExecContext(ctx, "UPDATE external_heartbeat SET last_attempt=?,last_error=? WHERE id=1", now, message)
	}
	if err != nil && ctx.Err() == nil {
		w.log.Error("heartbeat state write failed", "error", err)
	}
}
