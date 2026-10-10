package server

import (
	"context"
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"lanbridge/internal/notify"
	"net/http"
	"strconv"
	"strings"
	"time"
)

func (s *Server) notificationRoutes(mux *http.ServeMux) {
	mux.Handle("GET /api/notifications", s.auth(http.HandlerFunc(s.getNotifications), true))
	mux.Handle("POST /api/notifications/channels", s.auth(http.HandlerFunc(s.saveChannel), true))
	mux.Handle("POST /api/notifications/test", s.auth(http.HandlerFunc(s.testChannel), true))
	mux.Handle("POST /api/notifications/heartbeat", s.auth(http.HandlerFunc(s.saveObserver), true))
	mux.Handle("GET /api/incidents/{id}/events", s.auth(http.HandlerFunc(s.incidentEvents), false))
	mux.Handle("POST /api/incidents/{id}/notes", s.auth(http.HandlerFunc(s.incidentNote), true))
	mux.Handle("GET /api/incidents/export", s.auth(http.HandlerFunc(s.exportIncidents), true))
}
func (s *Server) getNotifications(w http.ResponseWriter, r *http.Request) {
	rows, e := s.DB.QueryContext(r.Context(), "SELECT id,config FROM notification_channels ORDER BY id")
	if e != nil {
		s.dbError(w, e)
		return
	}
	channels := []notify.Channel{}
	for rows.Next() {
		var id int64
		var raw string
		if e = rows.Scan(&id, &raw); e != nil {
			rows.Close()
			s.dbError(w, e)
			return
		}
		var c notify.Channel
		if e = json.Unmarshal([]byte(raw), &c); e != nil {
			rows.Close()
			s.dbError(w, e)
			return
		}
		c.ID = id
		channels = append(channels, c)
	}
	if e = rows.Err(); e != nil {
		rows.Close()
		s.dbError(w, e)
		return
	}
	rows.Close()
	rows, e = s.DB.QueryContext(r.Context(), "SELECT id,channel_id,kind,status,attempts,created_at,COALESCE(delivered_at,0),last_error FROM notification_queue ORDER BY id DESC LIMIT 100")
	if e != nil {
		s.dbError(w, e)
		return
	}
	deliveries := []map[string]any{}
	for rows.Next() {
		var id, channelID, created, delivered int64
		var kind, status, message string
		var attempts int
		if e = rows.Scan(&id, &channelID, &kind, &status, &attempts, &created, &delivered, &message); e != nil {
			rows.Close()
			s.dbError(w, e)
			return
		}
		deliveries = append(deliveries, map[string]any{"id": id, "channel_id": channelID, "kind": kind, "status": status, "attempts": attempts, "created": created, "delivered": delivered, "error": message})
	}
	if e = rows.Err(); e != nil {
		rows.Close()
		s.dbError(w, e)
		return
	}
	rows.Close()
	var enabled bool
	var env, message string
	var attempted, success int64
	if e = s.DB.QueryRowContext(r.Context(), "SELECT enabled,url_env,last_attempt,last_success,last_error FROM external_heartbeat WHERE id=1").Scan(&enabled, &env, &attempted, &success, &message); e != nil {
		s.dbError(w, e)
		return
	}
	jsonResponse(w, 200, map[string]any{"channels": channels, "deliveries": deliveries, "observer": map[string]any{"enabled": enabled, "url_env": env, "last_attempt": attempted, "last_success": success, "error": message}})
}
func (s *Server) saveChannel(w http.ResponseWriter, r *http.Request) {
	var c notify.Channel
	if !readJSON(w, r, &c) {
		return
	}
	if e := notify.Validate(c); e != nil {
		problem(w, 400, "channel", e.Error())
		return
	}
	raw, _ := json.Marshal(c)
	var e error
	if c.ID == 0 {
		var count int
		if e = s.DB.QueryRowContext(r.Context(), "SELECT COUNT(*) FROM notification_channels").Scan(&count); e != nil {
			s.dbError(w, e)
			return
		}
		if count >= 16 {
			problem(w, 409, "limit", "Лимит 16 каналов достигнут")
			return
		}
		res, err := s.DB.ExecContext(r.Context(), "INSERT INTO notification_channels(config) VALUES(?)", string(raw))
		e = err
		if e == nil {
			c.ID, _ = res.LastInsertId()
		}
	} else {
		res, err := s.DB.ExecContext(r.Context(), "UPDATE notification_channels SET config=? WHERE id=?", string(raw), c.ID)
		e = err
		if e == nil {
			n, _ := res.RowsAffected()
			if n == 0 {
				problem(w, 404, "channel", "Канал не найден")
				return
			}
		}
	}
	if e != nil {
		s.dbError(w, e)
		return
	}
	u := r.Context().Value(identityKey{}).(User)
	if e = s.audit(r.Context(), u.ID, "notification_channel", strconv.FormatInt(c.ID, 10)); e != nil {
		s.dbError(w, e)
		return
	}
	jsonResponse(w, 200, c)
}
func (s *Server) testChannel(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ID int64 `json:"id"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	var raw string
	if e := s.DB.QueryRowContext(r.Context(), "SELECT config FROM notification_channels WHERE id=?", body.ID).Scan(&raw); e != nil {
		problem(w, 404, "channel", "Канал не найден")
		return
	}
	var c notify.Channel
	if e := json.Unmarshal([]byte(raw), &c); e != nil {
		s.dbError(w, e)
		return
	}
	if !c.Enabled {
		problem(w, 409, "channel", "Сначала включите канал")
		return
	}
	var pending int
	if e := s.DB.QueryRowContext(r.Context(), "SELECT COUNT(*) FROM notification_queue WHERE channel_id=? AND kind='test' AND status='pending'", body.ID).Scan(&pending); e != nil {
		s.dbError(w, e)
		return
	}
	if pending > 0 {
		problem(w, 409, "pending", "Тестовое сообщение уже ожидает доставки")
		return
	}
	event := notify.Event{ID: time.Now().UnixNano(), Kind: "test", Name: "LANBridge", Message: "Проверка канала уведомлений", At: time.Now().Unix()}
	payload, _ := json.Marshal(event)
	if _, e := s.DB.ExecContext(r.Context(), "INSERT INTO notification_queue(channel_id,event_id,kind,payload,next_at,created_at) VALUES(?,?,'test',?,?,?)", body.ID, event.ID, string(payload), event.At, event.At); e != nil {
		s.dbError(w, e)
		return
	}
	jsonResponse(w, 202, map[string]bool{"queued": true})
}
func (s *Server) saveObserver(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Enabled bool   `json:"enabled"`
		URLenv  string `json:"url_env"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	if body.Enabled && !notify.EnvPattern.MatchString(body.URLenv) {
		problem(w, 400, "observer", "Укажите переменную LANBRIDGE_… с URL наблюдателя")
		return
	}
	if _, e := s.DB.ExecContext(r.Context(), "UPDATE external_heartbeat SET enabled=?,url_env=? WHERE id=1", body.Enabled, body.URLenv); e != nil {
		s.dbError(w, e)
		return
	}
	u := r.Context().Value(identityKey{}).(User)
	if e := s.audit(r.Context(), u.ID, "external_heartbeat", "configuration changed"); e != nil {
		s.dbError(w, e)
		return
	}
	jsonResponse(w, 200, body)
}
func (s *Server) incidentEvents(w http.ResponseWriter, r *http.Request) {
	id, e := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if e != nil {
		problem(w, 400, "id", "Некорректный номер")
		return
	}
	rows, e := s.DB.QueryContext(r.Context(), "SELECT at,kind,message FROM incident_events WHERE incident_id=? ORDER BY id LIMIT 1000", id)
	if e != nil {
		s.dbError(w, e)
		return
	}
	defer rows.Close()
	events := []map[string]any{}
	for rows.Next() {
		var at int64
		var kind, message string
		if e = rows.Scan(&at, &kind, &message); e != nil {
			s.dbError(w, e)
			return
		}
		events = append(events, map[string]any{"at": at, "kind": kind, "message": message})
	}
	if e = rows.Err(); e != nil {
		s.dbError(w, e)
		return
	}
	jsonResponse(w, 200, events)
}
func (s *Server) incidentNote(w http.ResponseWriter, r *http.Request) {
	id, e := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if e != nil {
		problem(w, 400, "id", "Некорректный номер")
		return
	}
	var body struct {
		Message string `json:"message"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	if len(strings.TrimSpace(body.Message)) == 0 || len(body.Message) > 2000 {
		problem(w, 400, "note", "Заметка: 1–2000 байт")
		return
	}
	var exists int
	if e = s.DB.QueryRowContext(r.Context(), "SELECT 1 FROM incidents WHERE id=?", id).Scan(&exists); e == sql.ErrNoRows {
		problem(w, 404, "incident", "Инцидент не найден")
		return
	} else if e != nil {
		s.dbError(w, e)
		return
	}
	if _, e = s.DB.ExecContext(r.Context(), "INSERT INTO incident_events(incident_id,at,kind,message) VALUES(?,?,'note',?)", id, time.Now().Unix(), body.Message); e != nil {
		s.dbError(w, e)
		return
	}
	jsonResponse(w, 201, map[string]bool{"ok": true})
}
func (s *Server) exportIncidents(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	rows, e := s.DB.QueryContext(ctx, "SELECT id,name,opened,COALESCE(closed,0),message FROM incidents ORDER BY id DESC LIMIT 10000")
	if e != nil {
		s.dbError(w, e)
		return
	}
	defer rows.Close()
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", "attachment; filename=incidents.csv")
	writer := csv.NewWriter(w)
	_ = writer.Write([]string{"id", "name", "opened", "closed", "message"})
	for rows.Next() {
		var id, opened, closed int64
		var name, message string
		if e = rows.Scan(&id, &name, &opened, &closed, &message); e != nil {
			return
		}
		safe := func(value string) string {
			if strings.ContainsAny(value[:min(1, len(value))], "=+-@\t\r") {
				return "'" + value
			}
			return value
		}
		_ = writer.Write([]string{strconv.FormatInt(id, 10), safe(name), strconv.FormatInt(opened, 10), strconv.FormatInt(closed, 10), safe(message)})
	}
	writer.Flush()
}
