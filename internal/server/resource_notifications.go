package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"lanbridge/internal/notify"
)

// Negative IDs keep resource cooldowns separate from positive monitor IDs and
// device discovery's own key. Stable keys survive restarts without schema changes.
var resourceDefinitions = []struct {
	code, name string
	id         int64
}{
	{"database", "Лимит SQLite", -1001},
	{"data_disk", "Диск базы данных", -1002},
	{"storage_disk", "Диск файлов", -1003},
	{"process", "Измерение процесса", -1004},
}

func (s *Server) queueResourceTransitions(ctx context.Context, status resourceSnapshot) error {
	levels := map[string]string{}
	messages := map[string]string{}
	for _, alert := range status.Alerts {
		levels[alert.Code] = alert.Level
		messages[alert.Code] = alert.Message
	}
	raw, err := json.Marshal(levels)
	if err != nil {
		return err
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var previousRaw string
	err = tx.QueryRowContext(ctx, "SELECT value FROM settings WHERE key='resource_alert_state'").Scan(&previousRaw)
	if err != nil && err != sql.ErrNoRows {
		return err
	}
	previous := map[string]string{}
	if previousRaw != "" {
		if err = json.Unmarshal([]byte(previousRaw), &previous); err != nil {
			return err
		}
	}
	if previousRaw == string(raw) {
		return nil
	}
	now := time.Now()
	sequence := now.UnixNano()
	for _, definition := range resourceDefinitions {
		level := levels[definition.code]
		if previous[definition.code] == level {
			continue
		}
		kind, message := "resource_"+level, messages[definition.code]
		if level == "" {
			kind = "resource_recovered"
			message = "Ресурс вернулся в нормальное состояние."
		}
		sequence++
		event := notify.Event{ID: sequence, MonitorID: definition.id, Kind: kind, Name: definition.name, Message: message, At: now.Unix()}
		if err = notify.Enqueue(ctx, tx, event); err != nil {
			return err
		}
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO settings(key,value) VALUES('resource_alert_state',?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", string(raw)); err != nil {
		return err
	}
	return tx.Commit()
}
