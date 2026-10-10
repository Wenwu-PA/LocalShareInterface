package server

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"lanbridge/internal/files"
	"mime"
	"net/http"
	"os"
	"path"
	"strconv"
	"strings"
	"time"
)

func (s *Server) InitFiles() error {
	var e error
	s.Files, e = files.New(s.Config.Storage.Root, s.DB, s.Log)
	return e
}
func fileActor(r *http.Request) files.Actor {
	u := r.Context().Value(identityKey{}).(User)
	return files.Actor{ID: u.ID, Admin: u.Role == "admin"}
}
func (s *Server) fileError(w http.ResponseWriter, e error) {
	switch {
	case errors.Is(e, files.ErrAccess):
		problem(w, 403, "access", e.Error())
	case errors.Is(e, files.ErrConflict):
		problem(w, 409, "conflict", e.Error())
	case errors.Is(e, files.ErrQuota):
		problem(w, 507, "quota", e.Error())
	case errors.Is(e, os.ErrNotExist) || errors.Is(e, sql.ErrNoRows):
		problem(w, 404, "file", "Файл или передача не найдены")
	case errors.Is(e, context.Canceled):
		problem(w, 408, "cancelled", "Операция отменена")
	default:
		s.Log.Warn("file operation failed", "error", e)
		problem(w, 400, "file", e.Error())
	}
}
func (s *Server) fileRoutes(mux *http.ServeMux) {
	wrap := func(handler http.HandlerFunc, admin bool) http.Handler {
		return s.auth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if s.Files == nil {
				problem(w, 503, "storage", "Хранилище недоступно")
				return
			}
			handler(w, r)
		}), admin)
	}
	mux.Handle("GET /api/files", wrap(s.listFiles, false))
	mux.Handle("GET /api/files/download", wrap(s.downloadFile, false))
	mux.Handle("POST /api/files/mkdir", wrap(s.mkdir, false))
	mux.Handle("POST /api/files/operation", wrap(s.fileOperation, false))
	mux.Handle("POST /api/files/zip", wrap(s.zipFiles, false))
	mux.Handle("POST /api/files/zip-ticket", wrap(s.createZipTicket, false))
	mux.Handle("GET /api/files/zip/{ticket}", wrap(s.downloadZip, false))
	mux.Handle("GET /api/files/storage", wrap(s.storageStatus, false))
	mux.Handle("PUT /api/files/storage", wrap(s.saveStorage, true))
	mux.Handle("PUT /api/files/access", wrap(s.saveFileAccess, true))
	mux.Handle("GET /api/files/trash", wrap(s.listTrash, false))
	mux.Handle("POST /api/files/restore", wrap(s.restoreFile, false))
	mux.Handle("GET /api/uploads", wrap(s.listUploads, false))
	mux.Handle("POST /api/uploads", wrap(s.beginUpload, false))
	mux.Handle("GET /api/uploads/{id}", wrap(s.getUpload, false))
	mux.Handle("PATCH /api/uploads/{id}", wrap(s.uploadChunk, false))
	mux.Handle("POST /api/uploads/{id}/finish", wrap(s.finishUpload, false))
	mux.Handle("DELETE /api/uploads/{id}", wrap(s.cancelUpload, false))
	mux.Handle("GET /api/transfers", wrap(s.transfers, false))
}
func (s *Server) listFiles(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	offset, _ := strconv.Atoi(q.Get("offset"))
	limit, _ := strconv.Atoi(q.Get("limit"))
	entries, e := s.Files.List(r.Context(), fileActor(r), q.Get("path"), q.Get("q"), q.Get("sort"), q.Get("order"), offset, limit)
	if e != nil {
		s.fileError(w, e)
		return
	}
	jsonResponse(w, 200, entries)
}
func (s *Server) mkdir(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Path string `json:"path"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	if e := s.Files.Mkdir(r.Context(), fileActor(r), body.Path); e != nil {
		s.fileError(w, e)
		return
	}
	jsonResponse(w, 201, map[string]bool{"ok": true})
}
func (s *Server) beginUpload(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Path     string `json:"path"`
		Size     int64  `json:"size"`
		SHA256   string `json:"sha256"`
		Conflict string `json:"conflict"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	u, e := s.Files.Begin(r.Context(), fileActor(r), body.Path, body.Size, body.SHA256, body.Conflict)
	if e != nil {
		s.fileError(w, e)
		return
	}
	jsonResponse(w, 201, u)
}
func (s *Server) getUpload(w http.ResponseWriter, r *http.Request) {
	u, e := s.Files.Upload(r.Context(), fileActor(r), r.PathValue("id"))
	if e != nil {
		s.fileError(w, e)
		return
	}
	jsonResponse(w, 200, u)
}
func (s *Server) uploadChunk(w http.ResponseWriter, r *http.Request) {
	offset, e := strconv.ParseInt(r.Header.Get("Upload-Offset"), 10, 64)
	if e != nil {
		problem(w, 400, "offset", "Нужен заголовок Upload-Offset")
		return
	}
	if r.ContentLength < 1 || r.ContentLength > files.ChunkLimit {
		problem(w, 413, "chunk", "Кусок должен содержать 1–1048576 байт")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, files.ChunkLimit)
	control := http.NewResponseController(w)
	_ = control.SetReadDeadline(time.Now().Add(60 * time.Second))
	defer control.SetReadDeadline(time.Time{})
	u, e := s.Files.Chunk(r.Context(), fileActor(r), r.PathValue("id"), offset, r.ContentLength, r.Body)
	if e != nil {
		s.fileError(w, e)
		return
	}
	jsonResponse(w, 200, u)
}
func (s *Server) finishUpload(w http.ResponseWriter, r *http.Request) {
	u, e := s.Files.Finish(r.Context(), fileActor(r), r.PathValue("id"))
	if e != nil {
		s.fileError(w, e)
		return
	}
	jsonResponse(w, 200, u)
}
func (s *Server) cancelUpload(w http.ResponseWriter, r *http.Request) {
	if e := s.Files.CancelUpload(r.Context(), fileActor(r), r.PathValue("id")); e != nil {
		s.fileError(w, e)
		return
	}
	jsonResponse(w, 200, map[string]bool{"ok": true})
}
func (s *Server) listUploads(w http.ResponseWriter, r *http.Request) {
	a := fileActor(r)
	rows, e := s.DB.QueryContext(r.Context(), "SELECT id FROM uploads WHERE owner_id=? AND state IN ('pending','committing') ORDER BY created DESC LIMIT 1000", a.ID)
	if e != nil {
		s.dbError(w, e)
		return
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if e = rows.Scan(&id); e != nil {
			rows.Close()
			s.dbError(w, e)
			return
		}
		ids = append(ids, id)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		s.dbError(w, e)
		return
	}
	uploads := []files.Upload{}
	for _, id := range ids {
		u, e := s.Files.Upload(r.Context(), a, id)
		if e != nil {
			s.fileError(w, e)
			return
		}
		uploads = append(uploads, u)
	}
	jsonResponse(w, 200, uploads)
}
func (s *Server) downloadFile(w http.ResponseWriter, r *http.Request) {
	a := fileActor(r)
	name := r.URL.Query().Get("path")
	f, e := s.Files.Open(r.Context(), a, name)
	if e != nil {
		s.fileError(w, e)
		return
	}
	defer f.Close()
	info, e := f.Stat()
	if e != nil {
		s.fileError(w, e)
		return
	}
	kind := mime.TypeByExtension(path.Ext(info.Name()))
	if kind == "" {
		kind = "application/octet-stream"
	}
	disposition := "attachment"
	inline := r.URL.Query().Get("inline") == "1"
	if inline && (strings.HasPrefix(kind, "image/") && kind != "image/svg+xml" || strings.HasPrefix(kind, "video/") || kind == "application/pdf" || strings.HasPrefix(kind, "text/plain")) {
		disposition = "inline"
	}
	w.Header().Set("Content-Type", kind)
	w.Header().Set("Content-Disposition", mime.FormatMediaType(disposition, map[string]string{"filename": info.Name()}))
	w.Header().Set("Content-Security-Policy", "sandbox; default-src 'none'")
	w.Header().Set("Cache-Control", "private, no-cache")
	w.Header().Set("ETag", fmt.Sprintf(`W/"%x-%x"`, info.ModTime().UnixNano(), info.Size()))
	http.ServeContent(w, r, info.Name(), info.ModTime(), f)
}
func (s *Server) fileOperation(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Action   string `json:"action"`
		Source   string `json:"source"`
		Target   string `json:"target"`
		Conflict string `json:"conflict"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	a := fileActor(r)
	var result string
	var e error
	switch body.Action {
	case "move":
		result, e = s.Files.Move(r.Context(), a, body.Source, body.Target, body.Conflict)
	case "copy":
		result, e = s.Files.Copy(r.Context(), a, body.Source, body.Target, body.Conflict)
	case "trash":
		result, e = s.Files.Trash(r.Context(), a, body.Source)
	default:
		problem(w, 400, "action", "Операция: move, copy или trash")
		return
	}
	if e != nil {
		s.fileError(w, e)
		return
	}
	jsonResponse(w, 200, map[string]string{"result": result})
}

type streamingFileWriter struct {
	http.ResponseWriter
	written bool
}

func (w *streamingFileWriter) Write(data []byte) (int, error) {
	w.written = true
	_ = http.NewResponseController(w.ResponseWriter).SetWriteDeadline(time.Now().Add(30 * time.Second))
	return w.ResponseWriter.Write(data)
}
func (s *Server) zipFiles(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Paths []string `json:"paths"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	s.streamZip(w, r, body.Paths)
}
func (s *Server) streamZip(w http.ResponseWriter, r *http.Request, paths []string) {
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", "attachment; filename=lanbridge.zip")
	writer := &streamingFileWriter{ResponseWriter: w}
	defer http.NewResponseController(w).SetWriteDeadline(time.Time{})
	if e := s.Files.Zip(r.Context(), fileActor(r), paths, writer); e != nil {
		if !writer.written {
			w.Header().Del("Content-Disposition")
			s.fileError(w, e)
		} else {
			s.Log.Warn("ZIP stream interrupted", "error", e)
			panic(http.ErrAbortHandler)
		}
	}
}

type zipTicket struct {
	session string
	paths   []string
	expires time.Time
}

func (s *Server) createZipTicket(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Paths []string `json:"paths"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	if len(body.Paths) < 1 || len(body.Paths) > 1000 {
		problem(w, 400, "paths", "Выберите 1–1000 объектов")
		return
	}
	for _, name := range body.Paths {
		if _, e := files.Clean(name); e != nil {
			s.fileError(w, e)
			return
		}
	}
	cookie, _ := r.Cookie(sessionCookie)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.zipTickets == nil {
		s.zipTickets = make(map[string]zipTicket)
	}
	for id, ticket := range s.zipTickets {
		if time.Now().After(ticket.expires) {
			delete(s.zipTickets, id)
		}
	}
	if len(s.zipTickets) >= 100 {
		problem(w, 429, "busy", "Слишком много запросов ZIP; повторите через минуту")
		return
	}
	id := randomToken()
	s.zipTickets[id] = zipTicket{digest(cookie.Value), body.Paths, time.Now().Add(time.Minute)}
	jsonResponse(w, 201, map[string]string{"url": "/api/files/zip/" + id})
}
func (s *Server) downloadZip(w http.ResponseWriter, r *http.Request) {
	cookie, _ := r.Cookie(sessionCookie)
	id := r.PathValue("ticket")
	s.mu.Lock()
	ticket, ok := s.zipTickets[id]
	if ok && ticket.session == digest(cookie.Value) {
		delete(s.zipTickets, id)
	}
	s.mu.Unlock()
	if !ok || ticket.session != digest(cookie.Value) || time.Now().After(ticket.expires) {
		problem(w, 404, "ticket", "Запрос ZIP истёк; выберите файлы повторно")
		return
	}
	s.streamZip(w, r, ticket.paths)
}
func (s *Server) storageStatus(w http.ResponseWriter, r *http.Request) {
	settings, e := s.Files.Settings(r.Context())
	if e != nil {
		s.dbError(w, e)
		return
	}
	var used, count int64
	if e = s.DB.QueryRowContext(r.Context(), "SELECT COALESCE(SUM(size),0),COUNT(*) FROM file_index WHERE is_dir=0").Scan(&used, &count); e != nil {
		s.dbError(w, e)
		return
	}
	available, total, e := s.Files.Space()
	if e != nil {
		s.fileError(w, e)
		return
	}
	jsonResponse(w, 200, map[string]any{"settings": settings, "used": used, "files": count, "available": available, "total": total})
}
func (s *Server) saveStorage(w http.ResponseWriter, r *http.Request) {
	var c files.Settings
	if !readJSON(w, r, &c) {
		return
	}
	if c.GlobalLimit < 0 || c.UserLimit < 0 || c.CriticalFree < 16*1024*1024 || c.TrashDays < 1 || c.TrashDays > 365 || c.TrashLimit < 0 || c.PartialHours < 1 || c.PartialHours > 720 {
		problem(w, 400, "settings", "Проверьте квоты, резерв места (от 16 МиБ), корзину (1–365 дней) и срок докачки (1–720 часов)")
		return
	}
	if _, e := s.DB.ExecContext(r.Context(), "UPDATE file_settings SET global_limit=?,user_limit=?,critical_free=?,trash_days=?,trash_limit=?,partial_hours=? WHERE id=1", c.GlobalLimit, c.UserLimit, c.CriticalFree, c.TrashDays, c.TrashLimit, c.PartialHours); e != nil {
		s.dbError(w, e)
		return
	}
	jsonResponse(w, 200, c)
}
func (s *Server) saveFileAccess(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Path   string `json:"path"`
		Access string `json:"access"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	name, e := files.Clean(body.Path)
	if e != nil {
		s.fileError(w, e)
		return
	}
	if body.Access != "admin" && body.Access != "users" && body.Access != "read" {
		problem(w, 400, "access", "Доступ: admin, users или read")
		return
	}
	if e = s.Files.SetAccess(r.Context(), fileActor(r), name, body.Access); e != nil {
		s.fileError(w, e)
		return
	}
	jsonResponse(w, 200, body)
}
func (s *Server) listTrash(w http.ResponseWriter, r *http.Request) {
	a := fileActor(r)
	rows, e := s.DB.QueryContext(r.Context(), "SELECT id,path,size,created,state FROM trash WHERE (? OR owner_id=?) ORDER BY created DESC LIMIT 1000", a.Admin, a.ID)
	if e != nil {
		s.dbError(w, e)
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id, name, state string
		var size, created int64
		if e = rows.Scan(&id, &name, &size, &created, &state); e != nil {
			s.dbError(w, e)
			return
		}
		items = append(items, map[string]any{"id": id, "path": name, "size": size, "created": created, "state": state})
	}
	if e = rows.Err(); e != nil {
		s.dbError(w, e)
		return
	}
	jsonResponse(w, 200, items)
}
func (s *Server) restoreFile(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ID       string `json:"id"`
		Conflict string `json:"conflict"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	name, e := s.Files.Restore(r.Context(), fileActor(r), body.ID, body.Conflict)
	if e != nil {
		s.fileError(w, e)
		return
	}
	jsonResponse(w, 200, map[string]string{"path": name})
}
func (s *Server) transfers(w http.ResponseWriter, r *http.Request) {
	a := fileActor(r)
	rows, e := s.DB.QueryContext(r.Context(), "SELECT id,user_id,path,direction,size,started,COALESCE(finished,0),status,error FROM transfers WHERE (? OR user_id=?) ORDER BY id DESC LIMIT 200", a.Admin, a.ID)
	if e != nil {
		s.dbError(w, e)
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id, user, size, started, finished int64
		var name, direction, status, message string
		if e = rows.Scan(&id, &user, &name, &direction, &size, &started, &finished, &status, &message); e != nil {
			s.dbError(w, e)
			return
		}
		items = append(items, map[string]any{"id": id, "user_id": user, "path": name, "direction": direction, "size": size, "started": started, "finished": finished, "status": status, "error": message})
	}
	if e = rows.Err(); e != nil {
		s.dbError(w, e)
		return
	}
	jsonResponse(w, 200, items)
}
