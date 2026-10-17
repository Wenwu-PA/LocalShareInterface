package server

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"lanbridge/internal/monitor"
	"net/http"
	"strconv"
	"time"
)

func (s *Server) monitorRoutes(mux *http.ServeMux) {
	mux.Handle("GET /api/monitors", s.auth(http.HandlerFunc(s.listMonitors), false))
	mux.Handle("POST /api/monitors", s.auth(http.HandlerFunc(s.saveMonitor), true))
	mux.Handle("PUT /api/monitors/{id}", s.auth(http.HandlerFunc(s.saveMonitor), true))
	mux.Handle("POST /api/monitors/bulk", s.auth(http.HandlerFunc(s.bulkMonitors), true))
	mux.Handle("GET /api/monitors/history", s.auth(http.HandlerFunc(s.monitorHistory), false))
	mux.Handle("GET /api/incidents", s.auth(http.HandlerFunc(s.listIncidents), false))
	mux.HandleFunc("POST /api/heartbeat/{token}", s.heartbeat)
}
func (s *Server) listMonitors(w http.ResponseWriter, r *http.Request) {
	list, e := monitor.List(r.Context(), s.DB)
	if e != nil {
		s.dbError(w, e)
		return
	}
	jsonResponse(w, 200, list)
}
func (s *Server) saveMonitor(w http.ResponseWriter, r *http.Request) {
	var d monitor.Definition
	if !readJSON(w, r, &d) {
		return
	}
	if e := monitor.Validate(d); e != nil {
		problem(w, 400, "monitor", e.Error())
		return
	}
	raw, _ := json.Marshal(d)
	var id int64
	var token string
	tx, e := s.DB.BeginTx(r.Context(), nil)
	if e != nil {
		s.dbError(w, e)
		return
	}
	defer tx.Rollback()
	if r.Method == "POST" {
		var count int
		if e = tx.QueryRowContext(r.Context(), "SELECT COUNT(*) FROM monitors").Scan(&count); e != nil {
			s.dbError(w, e)
			return
		}
		if count >= 1000 {
			problem(w, 409, "limit", "Лимит 1000 проверок достигнут")
			return
		}
		key := ""
		if d.Kind == "heartbeat" {
			token = randomToken()
			key = monitor.HashToken(token)
		}
		res, err := tx.ExecContext(r.Context(), "INSERT INTO monitors(config,heartbeat_hash) VALUES(?,?)", string(raw), key)
		if err != nil {
			s.dbError(w, err)
			return
		}
		id, _ = res.LastInsertId()
	} else {
		var err error
		id, err = strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil {
			problem(w, 400, "id", "Некорректный номер проверки")
			return
		}
		var existing string
		if e = tx.QueryRowContext(r.Context(), "SELECT config FROM monitors WHERE id=?", id).Scan(&existing); e == sql.ErrNoRows {
			problem(w, 404, "monitor", "Проверка не найдена")
			return
		} else if e != nil {
			s.dbError(w, e)
			return
		}
		var old monitor.Definition
		if e = json.Unmarshal([]byte(existing), &old); e != nil {
			s.dbError(w, e)
			return
		}
		if old.Kind != d.Kind {
			problem(w, 400, "kind", "Для смены типа создайте отдельную проверку")
			return
		}
		if _, e = tx.ExecContext(r.Context(), "UPDATE monitors SET config=? WHERE id=?", string(raw), id); e != nil {
			s.dbError(w, e)
			return
		}
		if _, e = tx.ExecContext(r.Context(), "UPDATE monitor_state SET status='pending',failures=0 WHERE monitor_id=?", id); e != nil {
			s.dbError(w, e)
			return
		}
	}
	u := r.Context().Value(identityKey{}).(User)
	if _, e = tx.ExecContext(r.Context(), "INSERT INTO audit(created_at,user_id,action,detail) VALUES(?,?,'monitor_saved',?)", time.Now().Unix(), u.ID, strconv.FormatInt(id, 10)); e != nil {
		s.dbError(w, e)
		return
	}
	if e = tx.Commit(); e != nil {
		s.dbError(w, e)
		return
	}
	if e = s.Monitors.Reload(); e != nil {
		s.dbError(w, e)
		return
	}
	s.publish("monitors_changed", map[string]any{"id": id})
	result := map[string]any{"id": id}
	if token != "" {
		result["heartbeat_path"] = "/api/heartbeat/" + token
	}
	jsonResponse(w, 200, result)
}
func (s *Server) bulkMonitors(w http.ResponseWriter, r *http.Request) {
	var body struct {
		IDs    []int64 `json:"ids"`
		Action string  `json:"action"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	if len(body.IDs) < 1 || len(body.IDs) > 1000 {
		problem(w, 400, "ids", "Выберите от 1 до 1000 проверок")
		return
	}
	if body.Action != "pause" && body.Action != "resume" && body.Action != "delete" {
		problem(w, 400, "action", "Действие: pause, resume или delete")
		return
	}
	tx, e := s.DB.BeginTx(r.Context(), nil)
	if e != nil {
		s.dbError(w, e)
		return
	}
	defer tx.Rollback()
	for _, id := range body.IDs {
		if body.Action == "delete" {
			_, e = tx.ExecContext(r.Context(), "DELETE FROM monitors WHERE id=?", id)
		} else {
			var raw string
			e = tx.QueryRowContext(r.Context(), "SELECT config FROM monitors WHERE id=?", id).Scan(&raw)
			if e == sql.ErrNoRows {
				continue
			}
			if e != nil {
				s.dbError(w, e)
				return
			}
			var d monitor.Definition
			if e = json.Unmarshal([]byte(raw), &d); e != nil {
				s.dbError(w, e)
				return
			}
			d.Paused = body.Action == "pause"
			b, _ := json.Marshal(d)
			_, e = tx.ExecContext(r.Context(), "UPDATE monitors SET config=? WHERE id=?", string(b), id)
		}
		if e != nil {
			s.dbError(w, e)
			return
		}
	}
	u := r.Context().Value(identityKey{}).(User)
	if _, e = tx.ExecContext(r.Context(), "INSERT INTO audit(created_at,user_id,action,detail) VALUES(?,?,'monitor_bulk',?)", time.Now().Unix(), u.ID, body.Action); e != nil {
		s.dbError(w, e)
		return
	}
	if e = tx.Commit(); e != nil {
		s.dbError(w, e)
		return
	}
	if e = s.Monitors.Reload(); e != nil {
		s.dbError(w, e)
		return
	}
	s.publish("monitors_changed", body)
	jsonResponse(w, 200, map[string]bool{"ok": true})
}
func (s *Server) heartbeat(w http.ResponseWriter, r *http.Request) {
	token := r.PathValue("token")
	if len(token) != 43 {
		problem(w, 404, "heartbeat", "Проверка не найдена")
		return
	}
	res, e := s.DB.ExecContext(r.Context(), "UPDATE monitors SET heartbeat_at=? WHERE heartbeat_hash=? AND json_extract(config,'$.kind')='heartbeat'", time.Now().Unix(), monitor.HashToken(token))
	if e != nil {
		s.dbError(w, e)
		return
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		problem(w, 404, "heartbeat", "Проверка не найдена")
		return
	}
	jsonResponse(w, 200, map[string]bool{"ok": true})
}
func (s *Server) monitorHistory(w http.ResponseWriter, r *http.Request) {
	seconds := int64(3600)
	switch r.URL.Query().Get("window") {
	case "24h":
		seconds = 86400
	case "7d":
		seconds = 7 * 86400
	case "30d":
		seconds = 30 * 86400
	}
	bucket := seconds / 180
	if bucket < 60 {
		bucket = 60
	}
	since := time.Now().Unix() - seconds
	id, _ := strconv.ParseInt(r.URL.Query().Get("id"), 10, 64)
	group := r.URL.Query().Get("group")
	if len(group) > 96 {
		problem(w, 400, "group", "Слишком длинное название группы")
		return
	}
	rows, e := s.DB.QueryContext(r.Context(), `SELECT (bucket/?)*?,SUM(total),SUM(up),SUM(latency_sum) FROM aggregates WHERE bucket>=? AND (?=0 OR monitor_id=?) AND (?='' OR monitor_id IN (SELECT id FROM monitors WHERE json_extract(config,'$.group')=?)) GROUP BY bucket/? ORDER BY bucket/?`, bucket, bucket, since, id, id, group, group, bucket, bucket)
	if e != nil {
		s.dbError(w, e)
		return
	}
	defer rows.Close()
	points := []map[string]any{}
	total, up := int64(0), int64(0)
	for rows.Next() {
		var at, n, ok int64
		var latency float64
		if e = rows.Scan(&at, &n, &ok, &latency); e != nil {
			s.dbError(w, e)
			return
		}
		total += n
		up += ok
		points = append(points, map[string]any{"at": at, "latency": latency / float64(n), "total": n, "up": ok})
	}
	if e = rows.Err(); e != nil {
		s.dbError(w, e)
		return
	}
	var uptime any
	if total > 0 {
		uptime = 100 * float64(up) / float64(total)
	}
	jsonResponse(w, 200, map[string]any{"points": points, "availability": uptime, "samples": total})
}
func (s *Server) listIncidents(w http.ResponseWriter, r *http.Request) {
	rows, e := s.DB.QueryContext(r.Context(), "SELECT id,COALESCE(monitor_id,0),name,opened,COALESCE(closed,0),message FROM incidents ORDER BY id DESC LIMIT 500")
	if e != nil {
		s.dbError(w, e)
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, monitorID, opened, closed int64
		var name, message string
		if e = rows.Scan(&id, &monitorID, &name, &opened, &closed, &message); e != nil {
			s.dbError(w, e)
			return
		}
		out = append(out, map[string]any{"id": id, "number": fmt.Sprintf("INC-%03d", id), "monitor_id": monitorID, "name": name, "opened": opened, "closed": closed, "message": message})
	}
	if e = rows.Err(); e != nil {
		s.dbError(w, e)
		return
	}
	jsonResponse(w, 200, out)
}
