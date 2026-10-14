package server

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"time"

	"lanbridge/internal/files"
)

type share struct {
	ID        string `json:"id"`
	Path      string `json:"path"`
	Owner     int64  `json:"owner_id"`
	Created   int64  `json:"created"`
	Expires   int64  `json:"expires"`
	Limit     int    `json:"max_downloads"`
	Downloads int    `json:"downloads"`
	Revoked   bool   `json:"revoked"`
	Protected bool   `json:"password_required"`
	password  string
	user      User
}
type shareTicket struct {
	hash    string
	expires time.Time
}

type guestShareKey struct{}

func transferSource(r *http.Request, direction string) (string, string) {
	id, _ := r.Context().Value(guestShareKey{}).(string)
	if id != "" {
		direction = "guest_" + direction
	}
	return direction, id
}

func (s *Server) shareRoutes(mux *http.ServeMux) {
	mux.Handle("GET /api/shares", s.auth(http.HandlerFunc(s.listShares), false))
	mux.Handle("POST /api/shares", s.auth(http.HandlerFunc(s.createShare), false))
	mux.Handle("DELETE /api/shares/{id}", s.auth(http.HandlerFunc(s.revokeShare), false))
	mux.HandleFunc("GET /api/guest/{token}", s.guestInfo)
	mux.HandleFunc("POST /api/guest/{token}/ticket", s.guestTicket)
	mux.HandleFunc("GET /api/guest-download/{ticket}", s.guestDownload)
}
func (s *Server) listShares(w http.ResponseWriter, r *http.Request) {
	u := r.Context().Value(identityKey{}).(User)
	rows, e := s.DB.QueryContext(r.Context(), `SELECT id,path,owner_id,created,expires,max_downloads,downloads,revoked,password_hash<>'' FROM shares WHERE (? OR owner_id=?) ORDER BY created DESC,id LIMIT 1000`, u.Role == "admin", u.ID)
	if e != nil {
		s.dbError(w, e)
		return
	}
	defer rows.Close()
	result := []share{}
	for rows.Next() {
		var v share
		if e = rows.Scan(&v.ID, &v.Path, &v.Owner, &v.Created, &v.Expires, &v.Limit, &v.Downloads, &v.Revoked, &v.Protected); e != nil {
			s.dbError(w, e)
			return
		}
		result = append(result, v)
	}
	if e = rows.Err(); e != nil {
		s.dbError(w, e)
		return
	}
	jsonResponse(w, 200, result)
}
func (s *Server) createShare(w http.ResponseWriter, r *http.Request) {
	if s.Files == nil {
		problem(w, 503, "storage", "Хранилище недоступно")
		return
	}
	var b struct {
		Path     string `json:"path"`
		TTL      int64  `json:"ttl_seconds"`
		Password string `json:"password"`
		Limit    int    `json:"max_downloads"`
	}
	if !readJSON(w, r, &b) {
		return
	}
	if b.TTL < 60 || b.TTL > 30*86400 || b.Limit < 0 || b.Limit > 1000000 || (b.Password != "" && (len(b.Password) < 8 || len(b.Password) > 256)) {
		problem(w, 400, "share", "Срок: 60 секунд–30 дней; лимит: 0–1000000; пароль: 8–256 байт или пустой")
		return
	}
	name, e := files.Clean(b.Path)
	if e != nil {
		s.fileError(w, e)
		return
	}
	if _, e = s.Files.ShareInfo(r.Context(), fileActor(r), name, true); e != nil {
		s.fileError(w, e)
		return
	}
	hash := ""
	if b.Password != "" {
		if !s.enterAuth(w) {
			return
		}
		hash = passwordHash(b.Password)
		<-s.authGate
	}
	token, id := randomToken(), files.ID()
	now := time.Now().Unix()
	u := fileActor(r)
	// Bound retained link metadata per owner; keep expired records for 30 days.
	_, e = s.DB.ExecContext(r.Context(), "DELETE FROM shares WHERE owner_id=? AND expires<?", u.ID, now-30*86400)
	if e != nil {
		s.dbError(w, e)
		return
	}
	result, e := s.DB.ExecContext(r.Context(), `INSERT INTO shares(id,token_hash,owner_id,path,password_hash,created,expires,max_downloads) SELECT ?,?,?,?,?,?,?,? WHERE (SELECT COUNT(*) FROM shares WHERE owner_id=?)<1000`, id, digest(token), u.ID, name, hash, now, now+b.TTL, b.Limit, u.ID)
	if e != nil {
		s.dbError(w, e)
		return
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		problem(w, 409, "share_limit", "Достигнут лимит 1000 сохранённых ссылок")
		return
	}
	if e = s.audit(r.Context(), u.ID, "share.create", id); e != nil {
		s.Log.Error("share audit failed", "error", e)
	}
	jsonResponse(w, 201, map[string]any{"id": id, "url": "/s/" + token, "expires": now + b.TTL})
}
func (s *Server) revokeShare(w http.ResponseWriter, r *http.Request) {
	u := fileActor(r)
	id := r.PathValue("id")
	res, e := s.DB.ExecContext(r.Context(), "UPDATE shares SET revoked=1 WHERE id=? AND (? OR owner_id=?)", id, u.Admin, u.ID)
	if e != nil {
		s.dbError(w, e)
		return
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		problem(w, 404, "share", "Ссылка не найдена")
		return
	}
	if e = s.audit(r.Context(), u.ID, "share.revoke", id); e != nil {
		s.Log.Error("share audit failed", "error", e)
	}
	jsonResponse(w, 200, map[string]bool{"ok": true})
}
func (s *Server) lookupShare(ctx context.Context, hash string) (share, error) {
	var v share
	e := s.DB.QueryRowContext(ctx, `SELECT s.id,s.path,s.owner_id,s.expires,s.max_downloads,s.downloads,s.password_hash,u.id,u.username,u.role FROM shares s JOIN users u ON u.id=s.owner_id WHERE s.token_hash=? AND s.revoked=0 AND s.expires>? AND (s.max_downloads=0 OR s.downloads<s.max_downloads)`, hash, time.Now().Unix()).Scan(&v.ID, &v.Path, &v.Owner, &v.Expires, &v.Limit, &v.Downloads, &v.password, &v.user.ID, &v.user.Username, &v.user.Role)
	return v, e
}
func (s *Server) guestLookup(w http.ResponseWriter, r *http.Request, hash string) (share, bool) {
	v, e := s.lookupShare(r.Context(), hash)
	if errors.Is(e, sql.ErrNoRows) {
		problem(w, 404, "share", "Ссылка недоступна, истекла или исчерпана")
		return v, false
	}
	if e != nil {
		s.dbError(w, e)
		return v, false
	}
	if s.Files == nil {
		problem(w, 503, "storage", "Хранилище недоступно")
		return v, false
	}
	return v, true
}
func (s *Server) guestInfo(w http.ResponseWriter, r *http.Request) {
	v, ok := s.guestLookup(w, r, digest(r.PathValue("token")))
	if !ok {
		return
	}
	name := path.Base(v.Path)
	if v.password != "" {
		name = "Защищённый объект"
	}
	jsonResponse(w, 200, map[string]any{"name": name, "password_required": v.password != "", "expires": v.Expires, "max_downloads": v.Limit, "downloads": v.Downloads})
}
func (s *Server) guestTicket(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Password string `json:"password"`
	}
	if !readJSON(w, r, &b) {
		return
	}
	if len(b.Password) > 256 {
		problem(w, 400, "password", "Пароль слишком длинный")
		return
	}
	hash := digest(r.PathValue("token"))
	v, ok := s.guestLookup(w, r, hash)
	if !ok {
		return
	}
	if v.password != "" {
		if !s.enterAuth(w) {
			return
		}
		defer func() { <-s.authGate }()
		now := time.Now().Unix()
		ip := digest(clientIP(r))
		var attempts int
		var started int64
		if _, e := s.DB.ExecContext(r.Context(), "DELETE FROM share_attempts WHERE window_start<=?", now-900); e != nil {
			s.dbError(w, e)
			return
		}
		e := s.DB.QueryRowContext(r.Context(), "SELECT attempts,window_start FROM share_attempts WHERE ip_hash=?", ip).Scan(&attempts, &started)
		if e != nil && !errors.Is(e, sql.ErrNoRows) {
			s.dbError(w, e)
			return
		}
		if attempts >= 10 {
			w.Header().Set("Retry-After", strconv.FormatInt(900-(now-started), 10))
			problem(w, 429, "rate_limit", "Слишком много попыток. Повторите позже.")
			return
		}
		if !checkPassword(b.Password, v.password) {
			res, e := s.DB.ExecContext(r.Context(), `INSERT INTO share_attempts(ip_hash,attempts,window_start) SELECT ?,1,? WHERE (SELECT COUNT(*) FROM share_attempts)<10000 OR EXISTS(SELECT 1 FROM share_attempts WHERE ip_hash=?) ON CONFLICT(ip_hash) DO UPDATE SET attempts=attempts+1`, ip, now, ip)
			if e != nil {
				s.dbError(w, e)
				return
			}
			n, _ := res.RowsAffected()
			if n == 0 {
				problem(w, 429, "busy", "Повторите позже")
				return
			}
			problem(w, 401, "password", "Неверный пароль")
			return
		}
	}
	// Tickets carry no quota reservation and expire quickly. Quota is claimed by GET.
	s.shareMu.Lock()
	defer s.shareMu.Unlock()
	if s.shareTickets == nil {
		s.shareTickets = make(map[string]shareTicket)
	}
	for key, t := range s.shareTickets {
		if time.Now().After(t.expires) {
			delete(s.shareTickets, key)
		}
	}
	if len(s.shareTickets) >= 1024 {
		problem(w, 429, "busy", "Повторите через минуту")
		return
	}
	token := randomToken()
	s.shareTickets[digest(token)] = shareTicket{hash: hash, expires: time.Now().Add(time.Minute)}
	jsonResponse(w, 201, map[string]string{"url": "/api/guest-download/" + token, "name": path.Base(v.Path)})
}
func (s *Server) guestDownload(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		w.Header().Set("Allow", "GET")
		problem(w, 405, "method", "Нужен GET")
		return
	}
	select {
	case s.guestGate <- struct{}{}:
		defer func() { <-s.guestGate }()
	default:
		problem(w, 429, "busy", "Две гостевые передачи уже выполняются. Запросите скачивание позже.")
		return
	}
	key := digest(r.PathValue("ticket"))
	s.shareMu.Lock()
	t, ok := s.shareTickets[key]
	delete(s.shareTickets, key)
	s.shareMu.Unlock()
	if !ok || time.Now().After(t.expires) {
		problem(w, 404, "ticket", "Запросите скачивание заново")
		return
	}
	v, ok := s.guestLookup(w, r, t.hash)
	if !ok {
		return
	}
	info, e := s.Files.ShareInfo(r.Context(), files.Actor{ID: v.Owner, Admin: v.user.Role == "admin"}, v.Path, false)
	if e != nil {
		problem(w, 404, "share", "Объект недоступен")
		return
	}
	res, e := s.DB.ExecContext(r.Context(), `UPDATE shares SET downloads=downloads+1 WHERE id=? AND revoked=0 AND expires>? AND (max_downloads=0 OR downloads<max_downloads)`, v.ID, time.Now().Unix())
	if e != nil {
		s.dbError(w, e)
		return
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		problem(w, 404, "share", "Ссылка недоступна, истекла или исчерпана")
		return
	}
	// Do not forward guest-supplied path/inline parameters or conditional headers.
	ctx := context.WithValue(r.Context(), identityKey{}, v.user)
	request := r.Clone(context.WithValue(ctx, guestShareKey{}, v.ID))
	u := *r.URL
	u.RawQuery = url.Values{"path": {v.Path}}.Encode()
	request.URL = &u
	for _, key := range []string{"If-None-Match", "If-Modified-Since", "If-Unmodified-Since", "If-Match"} {
		request.Header.Del(key)
	}
	if info.IsDir() {
		s.streamZip(w, request, []string{v.Path})
	} else {
		defer http.NewResponseController(w).SetWriteDeadline(time.Time{})
		s.downloadFile(&streamingFileWriter{ResponseWriter: w}, request)
	}
}
