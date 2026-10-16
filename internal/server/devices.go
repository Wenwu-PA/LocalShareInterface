package server

import (
	"encoding/json"
	"lanbridge/internal/network"
	"net/http"
	"strconv"
	"strings"
	"time"
)

func (s *Server) deviceRoutes(mux *http.ServeMux) {
	mux.Handle("GET /api/devices/summary", s.auth(http.HandlerFunc(s.deviceSummary), false))
	mux.Handle("GET /api/devices", s.auth(http.HandlerFunc(s.getDevices), false))
	mux.Handle("POST /api/devices/scan", s.auth(http.HandlerFunc(s.scanDevices), true))
	mux.Handle("POST /api/devices/cancel", s.auth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.Discovery.Cancel()
		jsonResponse(w, 200, map[string]bool{"ok": true})
	}), true))
	mux.Handle("POST /api/devices/config", s.auth(http.HandlerFunc(s.configureDiscovery), true))
	mux.Handle("PUT /api/devices/{id}", s.auth(http.HandlerFunc(s.updateDevice), true))
	mux.Handle("GET /api/devices/{id}/events", s.auth(http.HandlerFunc(s.deviceEvents), false))
}
func (s *Server) getDevices(w http.ResponseWriter, r *http.Request) {
	rows, e := s.DB.QueryContext(r.Context(), "SELECT id,ip,mac,hostname,name,icon,known,status,first_seen,last_seen,checked FROM devices ORDER BY last_seen DESC,id LIMIT 10000")
	if e != nil {
		s.dbError(w, e)
		return
	}
	devices := []map[string]any{}
	for rows.Next() {
		var id, first, last, checked int64
		var ip, mac, hostname, name, icon, status string
		var known bool
		if e = rows.Scan(&id, &ip, &mac, &hostname, &name, &icon, &known, &status, &first, &last, &checked); e != nil {
			rows.Close()
			s.dbError(w, e)
			return
		}
		devices = append(devices, map[string]any{"id": id, "ip": ip, "mac": mac, "hostname": hostname, "name": name, "icon": icon, "known": known, "status": status, "first_seen": first, "last_seen": last, "checked": checked, "vendor": network.Vendor(mac)})
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		s.dbError(w, e)
		return
	}
	var enabled bool
	var subnet, message string
	var interval int
	var last int64
	if e = s.DB.QueryRowContext(r.Context(), "SELECT enabled,subnet,interval,last_scan,last_error FROM discovery_config WHERE id=1").Scan(&enabled, &subnet, &interval, &last, &message); e != nil {
		s.dbError(w, e)
		return
	}
	jsonResponse(w, 200, map[string]any{"devices": devices, "interfaces": network.Interfaces(), "progress": s.Discovery.Status(), "config": map[string]any{"enabled": enabled, "subnet": subnet, "interval": interval, "last_scan": last, "error": message}})
}
func (s *Server) scanDevices(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Subnet string `json:"subnet"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	if e := s.Discovery.Scan(body.Subnet); e != nil {
		problem(w, 409, "scan", e.Error())
		return
	}
	u := r.Context().Value(identityKey{}).(User)
	_ = s.audit(r.Context(), u.ID, "network_scan", body.Subnet)
	jsonResponse(w, 202, s.Discovery.Status())
}
func (s *Server) configureDiscovery(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Enabled  bool   `json:"enabled"`
		Subnet   string `json:"subnet"`
		Interval int    `json:"interval"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	if body.Interval < 120 || body.Interval > 86400 {
		problem(w, 400, "interval", "Интервал: 120–86400 секунд")
		return
	}
	if body.Enabled {
		if _, e := network.ValidateSubnet(body.Subnet); e != nil {
			problem(w, 400, "subnet", e.Error())
			return
		}
	}
	tx, e := s.DB.BeginTx(r.Context(), nil)
	if e != nil {
		s.dbError(w, e)
		return
	}
	defer tx.Rollback()
	if _, e := tx.ExecContext(r.Context(), "UPDATE discovery_config SET enabled=?,subnet=?,interval=? WHERE id=1", body.Enabled, body.Subnet, body.Interval); e != nil {
		s.dbError(w, e)
		return
	}
	detail, _ := json.Marshal(body)
	u := r.Context().Value(identityKey{}).(User)
	if _, e = tx.ExecContext(r.Context(), "INSERT INTO audit(created_at,user_id,action,detail) VALUES(?,?,?,?)", time.Now().Unix(), u.ID, "discovery_config", string(detail)); e != nil {
		s.dbError(w, e)
		return
	}
	if e = tx.Commit(); e != nil {
		s.dbError(w, e)
		return
	}
	s.publish("devices", s.Discovery.Status())
	if !body.Enabled {
		s.Discovery.Cancel()
	}
	jsonResponse(w, 200, body)
}
func (s *Server) updateDevice(w http.ResponseWriter, r *http.Request) {
	id, e := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if e != nil {
		problem(w, 400, "id", "Некорректный номер")
		return
	}
	var body struct {
		Name  string `json:"name"`
		Icon  string `json:"icon"`
		Known bool   `json:"known"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	allowedIcon := map[string]bool{"device": true, "phone": true, "router": true, "server": true, "printer": true}
	if len(body.Name) > 128 || !allowedIcon[body.Icon] {
		problem(w, 400, "device", "Имя до 128 байт; выберите значок из списка")
		return
	}
	tx, e := s.DB.BeginTx(r.Context(), nil)
	if e != nil {
		s.dbError(w, e)
		return
	}
	defer tx.Rollback()
	res, e := tx.ExecContext(r.Context(), "UPDATE devices SET name=?,icon=?,known=? WHERE id=?", strings.TrimSpace(body.Name), body.Icon, body.Known, id)
	if e != nil {
		s.dbError(w, e)
		return
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		problem(w, 404, "device", "Устройство не найдено")
		return
	}
	u := r.Context().Value(identityKey{}).(User)
	detail, _ := json.Marshal(map[string]any{"id": id, "name": strings.TrimSpace(body.Name), "icon": body.Icon, "known": body.Known})
	if _, e = tx.ExecContext(r.Context(), "INSERT INTO audit(created_at,user_id,action,detail) VALUES(?,?,?,?)", time.Now().Unix(), u.ID, "device_updated", string(detail)); e != nil {
		s.dbError(w, e)
		return
	}
	if e = tx.Commit(); e != nil {
		s.dbError(w, e)
		return
	}
	s.publish("devices", s.Discovery.Status())
	jsonResponse(w, 200, body)
}
func (s *Server) deviceEvents(w http.ResponseWriter, r *http.Request) {
	id, e := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if e != nil {
		problem(w, 400, "id", "Некорректный номер")
		return
	}
	rows, e := s.DB.QueryContext(r.Context(), "SELECT at,status FROM device_events WHERE device_id=? ORDER BY id DESC LIMIT 100", id)
	if e != nil {
		s.dbError(w, e)
		return
	}
	defer rows.Close()
	events := []map[string]any{}
	for rows.Next() {
		var at int64
		var status string
		if e = rows.Scan(&at, &status); e != nil {
			s.dbError(w, e)
			return
		}
		events = append(events, map[string]any{"at": at, "status": status})
	}
	if e = rows.Err(); e != nil {
		s.dbError(w, e)
		return
	}
	jsonResponse(w, 200, events)
}
