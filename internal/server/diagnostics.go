package server

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"lanbridge/internal/network"
	"net/http"
	"time"
)

func (s *Server) diagnosticRoutes(mux *http.ServeMux) {
	mux.Handle("POST /api/diagnostics", s.auth(http.HandlerFunc(s.diagnose), true))
	mux.Handle("GET /api/diagnostics/speed", s.auth(http.HandlerFunc(s.speed), true))
}
func (s *Server) diagnose(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Kind   string `json:"kind"`
		Target string `json:"target"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	select {
	case s.diagnosticGate <- struct{}{}:
		defer func() { <-s.diagnosticGate }()
	default:
		problem(w, 429, "busy", "Уже выполняются две диагностики")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()
	w.Header().Set("Content-Type", "application/x-ndjson; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	controller := http.NewResponseController(w)
	emit := func(kind, message string) error {
		_ = controller.SetWriteDeadline(time.Now().Add(5 * time.Second))
		if e := json.NewEncoder(w).Encode(map[string]string{"kind": kind, "message": message}); e != nil {
			return e
		}
		return controller.Flush()
	}
	e := network.Diagnose(ctx, body.Kind, body.Target, func(line string) error { return emit("line", line) })
	if e != nil {
		_ = emit("error", e.Error())
	}
	_ = emit("done", "")
}
func (s *Server) speed(w http.ResponseWriter, r *http.Request) {
	select {
	case s.diagnosticGate <- struct{}{}:
		defer func() { <-s.diagnosticGate }()
	default:
		problem(w, 429, "busy", "Диагностика занята")
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Length", "16777216")
	block := make([]byte, 65536)
	if _, e := rand.Read(block); e != nil {
		return
	}
	control := http.NewResponseController(w)
	deadline := time.Now().Add(30 * time.Second)
	for i := 0; i < 256; i++ {
		if r.Context().Err() != nil || time.Now().After(deadline) {
			return
		}
		next := time.Now().Add(5 * time.Second)
		if next.After(deadline) {
			next = deadline
		}
		_ = control.SetWriteDeadline(next)
		if _, e := w.Write(block); e != nil {
			return
		}
	}
}
