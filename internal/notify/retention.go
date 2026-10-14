package notify

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"
)

const PendingLimit = 10000
const HistoryLimit = 10000

// Queue inserts and bounds delivery jobs in the caller's transaction. Overflow
// must never reject the incident transaction: retain the newest pending jobs.
func Queue(ctx context.Context, tx *sql.Tx, channelID int64, event Event) error {
	payload, err := json.Marshal(event)
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO notification_queue(channel_id,event_id,kind,payload,next_at,created_at) VALUES(?,?,?,?,?,?)`, channelID, event.ID, event.Kind, string(payload), event.At, event.At); err != nil {
		return err
	}
	return prune(ctx, tx, time.Now().Unix())
}

func prune(ctx context.Context, tx *sql.Tx, now int64) error {
	if _, err := tx.ExecContext(ctx, `UPDATE notification_queue SET status='dropped',last_error='Вытеснено: достигнут лимит очереди' WHERE id IN (SELECT id FROM notification_queue WHERE status='pending' ORDER BY id DESC LIMIT -1 OFFSET ?)`, PendingLimit); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM notification_queue WHERE status!='pending' AND COALESCE(delivered_at,created_at)<?`, now-int64(30*24*time.Hour/time.Second)); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `DELETE FROM notification_queue WHERE id IN (SELECT id FROM notification_queue WHERE status!='pending' ORDER BY id DESC LIMIT -1 OFFSET ?)`, HistoryLimit)
	return err
}

func (w *Worker) maintain(ctx context.Context) error {
	if time.Now().Before(w.nextMaintenance) {
		return nil
	}
	tx, err := w.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = prune(ctx, tx, time.Now().Unix()); err != nil {
		return err
	}
	if err = tx.Commit(); err == nil {
		w.nextMaintenance = time.Now().Add(time.Minute)
	}
	return err
}
