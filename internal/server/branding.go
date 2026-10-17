package server

import (
	"bytes"
	"database/sql"
	"encoding/base64"
	"image"
	_ "image/jpeg"
	"image/png"
	"io"
	"net/http"
	"time"
)

const logoLimit = 256 * 1024

func (s *Server) brandingRoutes(mux *http.ServeMux) {
	mux.Handle("GET /api/branding", s.auth(http.HandlerFunc(s.getBranding), false))
	mux.Handle("PUT /api/branding", s.auth(http.HandlerFunc(s.putBranding), true))
	mux.Handle("DELETE /api/branding", s.auth(http.HandlerFunc(s.deleteBranding), true))
}
func (s *Server) getBranding(w http.ResponseWriter, r *http.Request) {
	var logo string
	err := s.DB.QueryRowContext(r.Context(), "SELECT value FROM settings WHERE key='network_logo'").Scan(&logo)
	if err != nil && err != sql.ErrNoRows {
		s.dbError(w, err)
		return
	}
	jsonResponse(w, 200, map[string]string{"logo": logo})
}
func (s *Server) putBranding(w http.ResponseWriter, r *http.Request) {
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, logoLimit))
	if err != nil {
		problem(w, 413, "logo_size", "Логотип должен быть не больше 256 КиБ")
		return
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil || (format != "png" && format != "jpeg") {
		problem(w, 415, "logo_format", "Выберите изображение PNG или JPEG")
		return
	}
	if cfg.Width < 1 || cfg.Height < 1 || cfg.Width > 512 || cfg.Height > 512 {
		problem(w, 400, "logo_dimensions", "Максимальное разрешение логотипа — 512 × 512")
		return
	}
	img, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		problem(w, 400, "logo_decode", "Не удалось прочитать изображение")
		return
	}
	// Re-encode pixels to discard metadata and trailing payloads.
	var clean bytes.Buffer
	if err = png.Encode(&clean, img); err != nil {
		problem(w, 400, "logo_decode", "Не удалось обработать изображение")
		return
	}
	if clean.Len() > logoLimit {
		problem(w, 413, "logo_size", "После обработки логотип больше 256 КиБ. Уменьшите изображение.")
		return
	}
	s.saveBranding(w, r, "data:image/png;base64,"+base64.StdEncoding.EncodeToString(clean.Bytes()))
}
func (s *Server) deleteBranding(w http.ResponseWriter, r *http.Request) { s.saveBranding(w, r, "") }
func (s *Server) saveBranding(w http.ResponseWriter, r *http.Request, logo string) {
	tx, err := s.DB.BeginTx(r.Context(), nil)
	if err != nil {
		s.dbError(w, err)
		return
	}
	defer tx.Rollback()
	if logo == "" {
		_, err = tx.ExecContext(r.Context(), "DELETE FROM settings WHERE key='network_logo'")
	} else {
		_, err = tx.ExecContext(r.Context(), "INSERT INTO settings(key,value) VALUES('network_logo',?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", logo)
	}
	if err != nil {
		s.dbError(w, err)
		return
	}
	user := r.Context().Value(identityKey{}).(User)
	detail := "uploaded"
	if logo == "" {
		detail = "removed"
	}
	if _, err = tx.ExecContext(r.Context(), "INSERT INTO audit(created_at,user_id,action,detail) VALUES(?,?,?,?)", time.Now().Unix(), user.ID, "branding", detail); err != nil {
		s.dbError(w, err)
		return
	}
	if err = tx.Commit(); err != nil {
		s.dbError(w, err)
		return
	}
	s.publish("branding", map[string]bool{"changed": true})
	jsonResponse(w, 200, map[string]string{"logo": logo})
}
