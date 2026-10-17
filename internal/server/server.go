package server

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/argon2"
	"lanbridge/internal/config"
	"lanbridge/internal/files"
	"lanbridge/internal/monitor"
	"lanbridge/internal/network"
	"lanbridge/internal/notify"
	"lanbridge/internal/processmetrics"
	"lanbridge/internal/store"
	"lanbridge/internal/tunnels"
)

const sessionCookie = "__Host-lanbridge_go_session"
const csrfCookie = "__Host-lanbridge_go_csrf"

type User struct {
	ID       int64  `json:"id"`
	Username string `json:"username"`
	Role     string `json:"role"`
}
type identityKey struct{}
type event struct {
	ID   uint64 `json:"id"`
	Type string `json:"type"`
	Data any    `json:"data"`
}
type Server struct {
	resourceDeliveryFailed bool
	processMeter           processmetrics.Meter
	resourceMu             sync.Mutex
	resourceLevels         map[string]string
	DB                     *sql.DB
	Config                 config.Config
	Log                    *slog.Logger
	started                time.Time
	authGate               chan struct{}
	diagnosticGate         chan struct{}
	setupMu                sync.Mutex
	mu                     sync.Mutex
	subscribers            map[chan event]struct{}
	sequence               uint64
	Monitors               *monitor.Engine
	Notifications          *notify.Worker
	Discovery              *network.Discovery
	Files                  *files.Service
	zipTickets             map[string]zipTicket
	shareMu                sync.Mutex
	shareTickets           map[string]shareTicket
	guestGate              chan struct{}
	External               *tunnels.Manager
	externalHandler        http.Handler
	externalMu             sync.Mutex
	externalEvents         []externalEvent
	externalRequests       uint64
	externalBytes          uint64
}

func New(db *sql.DB, c config.Config, log *slog.Logger) *Server {
	s := &Server{DB: db, Config: c, Log: log, started: time.Now(), authGate: make(chan struct{}, 1), subscribers: make(map[chan event]struct{})}
	s.Monitors = monitor.New(db, log, s.publish)
	s.Notifications = notify.New(db, log, s.publish)
	s.Discovery = network.New(db, log, s.publish)
	s.diagnosticGate = make(chan struct{}, 2)
	s.guestGate = make(chan struct{}, 2)
	s.External = tunnels.New(tunnels.Cloudflare{Executable: c.External.Cloudflared})
	return s
}
func randomToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}
func digest(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }
func jsonResponse(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func problem(w http.ResponseWriter, status int, code, message string) {
	jsonResponse(w, status, map[string]string{"code": code, "error": message})
}
func readJSON(w http.ResponseWriter, r *http.Request, value any) bool {
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		problem(w, 415, "content_type", "Нужен Content-Type application/json")
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16*1024)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if err := d.Decode(value); err != nil {
		problem(w, 400, "invalid_json", "Некорректный запрос или превышен размер 16 КиБ")
		return false
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		problem(w, 400, "invalid_json", "Ожидается один JSON-объект")
		return false
	}
	return true
}
func setCookie(w http.ResponseWriter, name, value string, maxAge int) {
	http.SetCookie(w, &http.Cookie{Name: name, Value: value, Path: "/", MaxAge: maxAge, Secure: true, HttpOnly: name == sessionCookie, SameSite: http.SameSiteStrictMode})
}
func csrfValue(r *http.Request) string {
	c, e := r.Cookie(csrfCookie)
	if e != nil {
		return ""
	}
	b, e := base64.RawURLEncoding.DecodeString(c.Value)
	if e != nil || len(b) != 32 {
		return ""
	}
	return c.Value
}
func preCSRF(r *http.Request) bool {
	value := csrfValue(r)
	return value != "" && subtle.ConstantTimeCompare([]byte(value), []byte(r.Header.Get("X-CSRF-Token"))) == 1
}
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
func (s *Server) dbError(w http.ResponseWriter, err error) {
	s.Log.Error("database operation failed", "error", err)
	if store.IsFull(err) {
		problem(w, 507, "database_full", "SQLite достигла лимита database.max_mb или на диске нет места. Освободите место либо увеличьте лимит и перезапустите сервер.")
		return
	}
	problem(w, 500, "database", "Операция не выполнена. Проверьте журнал сервера.")
}
func (s *Server) audit(ctx context.Context, id any, action, detail string) error {
	_, err := s.DB.ExecContext(ctx, "INSERT INTO audit(created_at,user_id,action,detail) VALUES(?,?,?,?)", time.Now().Unix(), id, action, detail)
	return err
}

func (s *Server) Handler(assets fs.FS) http.Handler {
	mux := http.NewServeMux()
	s.monitorRoutes(mux)
	s.notificationRoutes(mux)
	mux.Handle("GET /api/audit", s.auth(http.HandlerFunc(s.getAudit), true))
	mux.Handle("GET /api/resources", s.auth(http.HandlerFunc(s.getResources), true))
	s.deviceRoutes(mux)
	s.diagnosticRoutes(mux)
	s.fileRoutes(mux)
	s.shareRoutes(mux)
	s.externalRoutes(mux)
	mux.Handle("GET /api/connection", s.auth(http.HandlerFunc(s.connection), false))
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), time.Second)
		defer cancel()
		if err := s.DB.PingContext(ctx); err != nil {
			problem(w, 503, "unhealthy", "База данных недоступна")
			return
		}
		jsonResponse(w, 200, map[string]any{"status": "ok", "uptime_seconds": int(time.Since(s.started).Seconds()), "version": "0.2.0-dev.1", "stage": "monitoring", "monitor_queue": s.Monitors.QueueDepth(), "dropped_samples": s.Monitors.Dropped.Load(), "last_measurement_write": s.Monitors.LastWrite.Load()})
	})
	mux.HandleFunc("GET /api/status", s.status)
	mux.HandleFunc("POST /api/setup", s.setup)
	mux.HandleFunc("POST /api/login", s.login)
	mux.Handle("POST /api/logout", s.auth(http.HandlerFunc(s.logout), false))
	mux.Handle("GET /api/me", s.auth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { jsonResponse(w, 200, r.Context().Value(identityKey{})) }), false))
	mux.Handle("GET /api/settings", s.auth(http.HandlerFunc(s.getSettings), false))
	mux.Handle("PUT /api/settings", s.auth(http.HandlerFunc(s.putSettings), true))
	mux.Handle("GET /api/events", s.auth(http.HandlerFunc(s.events), false))
	mux.Handle("GET /api/users", s.auth(http.HandlerFunc(s.users), true))
	mux.Handle("PATCH /api/users/{id}", s.auth(http.HandlerFunc(s.updateAccount), false))
	mux.Handle("POST /api/users", s.auth(http.HandlerFunc(s.createUser), true))
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		problem(w, 404, "not_found", "Маршрут не найден")
	})
	fileServer := http.FileServer(http.FS(assets))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" && r.Method != "HEAD" {
			problem(w, 405, "method", "Метод не поддерживается")
			return
		}
		path := strings.TrimPrefix(r.URL.Path, "/")
		if path != "" {
			if _, err := fs.Stat(assets, path); err == nil {
				if strings.HasPrefix(path, "assets/") {
					w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
				}
				fileServer.ServeHTTP(w, r)
				return
			}
		}
		content, err := fs.ReadFile(assets, "index.html")
		if err != nil {
			problem(w, 503, "frontend_missing", "Интерфейс не собран")
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(content)
	})
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if value := recover(); value != nil {
				if value == http.ErrAbortHandler {
					panic(value)
				}
				s.Log.Error("request panic", "method", r.Method, "route", r.Pattern)
				problem(w, 500, "internal", "Внутренняя ошибка. Повторите запрос.")
			}
		}()
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data: blob:; font-src 'self'; connect-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'")
		w.Header().Set("Cache-Control", "no-store")
		if origin := r.Header.Get("Origin"); origin != "" && origin != "https://"+r.Host {
			problem(w, 403, "origin", "Источник запроса не разрешён")
			return
		}
		if r.Method != "GET" && r.Method != "HEAD" && r.Method != "OPTIONS" && !strings.HasPrefix(r.URL.Path, "/api/heartbeat/") && !preCSRF(r) {
			problem(w, 403, "csrf", "Обновите страницу и повторите действие")
			return
		}
		mux.ServeHTTP(w, r)
	})
	s.externalHandler = s.guestGateway(handler)
	return handler
}

func (s *Server) identity(r *http.Request) (User, string, error) {
	var u User
	var csrf string
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return u, "", err
	}
	err = s.DB.QueryRowContext(r.Context(), `SELECT u.id,u.username,u.role,s.csrf_hash FROM sessions s JOIN users u ON s.user_id=u.id WHERE s.token_hash=? AND s.expires_at>?`, digest(c.Value), time.Now().Unix()).Scan(&u.ID, &u.Username, &u.Role, &csrf)
	return u, csrf, err
}
func (s *Server) auth(next http.Handler, admin bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, csrf, err := s.identity(r)
		if err != nil {
			if err != sql.ErrNoRows && err != http.ErrNoCookie {
				s.dbError(w, err)
				return
			}
			problem(w, 401, "unauthorized", "Войдите в учётную запись")
			return
		}
		if admin && u.Role != "admin" {
			problem(w, 403, "forbidden", "Доступно только администратору")
			return
		}
		if r.Method != "GET" && r.Method != "HEAD" && subtle.ConstantTimeCompare([]byte(csrf), []byte(digest(r.Header.Get("X-CSRF-Token")))) != 1 {
			problem(w, 403, "csrf", "Сессия изменилась. Обновите страницу.")
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), identityKey{}, u)))
	})
}
func (s *Server) status(w http.ResponseWriter, r *http.Request) {
	var count int
	if err := s.DB.QueryRowContext(r.Context(), "SELECT COUNT(*) FROM users").Scan(&count); err != nil {
		s.dbError(w, err)
		return
	}
	token := csrfValue(r)
	if token == "" {
		token = randomToken()
		setCookie(w, csrfCookie, token, 86400*7)
	}
	u, _, err := s.identity(r)
	var user any
	if err == nil {
		user = u
	}
	jsonResponse(w, 200, map[string]any{"setup_required": count == 0, "authenticated": err == nil, "user": user, "csrf": token, "version": "0.2.0-dev.1"})
}

type credentials struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

var usernamePattern = regexp.MustCompile(`^[a-zA-Z0-9_.-]{3,48}$`)

func validCredentials(c credentials) bool {
	return usernamePattern.MatchString(c.Username) && len(c.Password) >= 12 && len(c.Password) <= 256
}
func passwordHash(password string) string {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		panic(err)
	}
	key := argon2.IDKey([]byte(password), salt, 2, 19456, 1, 32)
	return "$argon2id$v=19$m=19456,t=2,p=1$" + base64.RawStdEncoding.EncodeToString(salt) + "$" + base64.RawStdEncoding.EncodeToString(key)
}
func checkPassword(password, encoded string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" || parts[2] != "v=19" || parts[3] != "m=19456,t=2,p=1" {
		return false
	}
	salt, e := base64.RawStdEncoding.DecodeString(parts[4])
	if e != nil || len(salt) != 16 {
		return false
	}
	expected, e := base64.RawStdEncoding.DecodeString(parts[5])
	if e != nil || len(expected) != 32 {
		return false
	}
	key := argon2.IDKey([]byte(password), salt, 2, 19456, 1, 32)
	return subtle.ConstantTimeCompare(key, expected) == 1
}
func (s *Server) enterAuth(w http.ResponseWriter) bool {
	select {
	case s.authGate <- struct{}{}:
		return true
	default:
		w.Header().Set("Retry-After", "1")
		problem(w, 429, "busy", "Вход уже обрабатывается. Повторите через секунду.")
		return false
	}
}
func (s *Server) issue(w http.ResponseWriter, r *http.Request, u User) error {
	token, csrf := randomToken(), randomToken()
	expires := time.Now().Add(s.Config.SessionDuration()).Unix()
	_, err := s.DB.ExecContext(r.Context(), "INSERT INTO sessions(token_hash,user_id,csrf_hash,expires_at) VALUES(?,?,?,?)", digest(token), u.ID, digest(csrf), expires)
	if err != nil {
		return err
	}
	age := int(s.Config.SessionDuration().Seconds())
	setCookie(w, sessionCookie, token, age)
	setCookie(w, csrfCookie, csrf, age)
	jsonResponse(w, 200, map[string]any{"user": u, "csrf": csrf})
	return nil
}
func (s *Server) setup(w http.ResponseWriter, r *http.Request) {
	s.setupMu.Lock()
	defer s.setupMu.Unlock()
	var count int
	if err := s.DB.QueryRowContext(r.Context(), "SELECT COUNT(*) FROM users").Scan(&count); err != nil {
		s.dbError(w, err)
		return
	}
	if count != 0 {
		problem(w, 409, "configured", "Администратор уже создан")
		return
	}
	var c credentials
	if !readJSON(w, r, &c) {
		return
	}
	if !validCredentials(c) {
		problem(w, 400, "credentials", "Логин: 3–48 латинских символов, цифр, . _ -; пароль: 12–256 байт")
		return
	}
	if !s.enterAuth(w) {
		return
	}
	defer func() { <-s.authGate }()
	result, err := s.DB.ExecContext(r.Context(), "INSERT INTO users(username,password_hash,role,created_at) VALUES(?,?,?,?)", c.Username, passwordHash(c.Password), "admin", time.Now().Unix())
	if err != nil {
		s.dbError(w, err)
		return
	}
	id, _ := result.LastInsertId()
	if err = s.audit(r.Context(), id, "setup", "administrator created"); err != nil {
		s.dbError(w, err)
		return
	}
	if err = s.issue(w, r, User{id, c.Username, "admin"}); err != nil {
		s.dbError(w, err)
	}
}
func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	var c credentials
	if !readJSON(w, r, &c) {
		return
	}
	if !validCredentials(c) {
		problem(w, 401, "credentials", "Неверный логин или пароль")
		return
	}
	if !s.enterAuth(w) {
		return
	}
	defer func() { <-s.authGate }()
	ip := clientIP(r)
	var failures int
	var started int64
	err := s.DB.QueryRowContext(r.Context(), "SELECT failures,window_start FROM login_attempts WHERE ip=?", ip).Scan(&failures, &started)
	if err != nil && err != sql.ErrNoRows {
		s.dbError(w, err)
		return
	}
	if time.Now().Unix()-started < 900 && failures >= s.Config.Security.LoginAttempts {
		w.Header().Set("Retry-After", strconv.FormatInt(900-(time.Now().Unix()-started), 10))
		problem(w, 429, "rate_limit", "Слишком много попыток. Повторите позже.")
		return
	}
	var u User
	var hash string
	err = s.DB.QueryRowContext(r.Context(), "SELECT id,username,password_hash,role FROM users WHERE username=?", c.Username).Scan(&u.ID, &u.Username, &hash, &u.Role)
	if err != nil && err != sql.ErrNoRows {
		s.dbError(w, err)
		return
	}
	if err == sql.ErrNoRows {
		_ = passwordHash(c.Password)
	}
	if err != nil || !checkPassword(c.Password, hash) {
		now := time.Now().Unix()
		if now-started >= 900 {
			failures = 0
			started = now
		}
		if _, e := s.DB.ExecContext(r.Context(), "INSERT INTO login_attempts(ip,failures,window_start) VALUES(?,?,?) ON CONFLICT(ip) DO UPDATE SET failures=excluded.failures,window_start=excluded.window_start", ip, failures+1, started); e != nil {
			s.dbError(w, e)
			return
		}
		if e := s.audit(r.Context(), nil, "login_failed", ip); e != nil {
			s.dbError(w, e)
			return
		}
		problem(w, 401, "credentials", "Неверный логин или пароль")
		return
	}
	if _, err = s.DB.ExecContext(r.Context(), "DELETE FROM login_attempts WHERE ip=?", ip); err != nil {
		s.dbError(w, err)
		return
	}
	if err = s.audit(r.Context(), u.ID, "login", ip); err != nil {
		s.dbError(w, err)
		return
	}
	if err = s.issue(w, r, u); err != nil {
		s.dbError(w, err)
	}
}
func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	c, _ := r.Cookie(sessionCookie)
	if _, err := s.DB.ExecContext(r.Context(), "DELETE FROM sessions WHERE token_hash=?", digest(c.Value)); err != nil {
		s.dbError(w, err)
		return
	}
	setCookie(w, sessionCookie, "", -1)
	setCookie(w, csrfCookie, "", -1)
	jsonResponse(w, 200, map[string]bool{"ok": true})
}

type Preferences struct {
	CardOrder  []string `json:"card_order"`
	GroupOrder []string `json:"group_order"`
	DateFormat string   `json:"date_format"`
	HomePage   string   `json:"home_page"`
	Name       string   `json:"name"`
	Subtitle   string   `json:"subtitle"`
	Theme      string   `json:"theme"`
	Accent     string   `json:"accent"`
	Density    string   `json:"density"`
	FontSize   int      `json:"font_size"`
	Timezone   string   `json:"timezone"`
}

func defaultPreferences() Preferences {
	return Preferences{CardOrder: []string{"availability", "internet", "checks", "devices"}, GroupOrder: []string{"WAN", "LAN", "DNS", "Хранилище", "Heartbeat"}, DateFormat: "dmy", HomePage: "overview", Name: "Домашняя сеть", Subtitle: "Локальный сервер", Theme: "system", Accent: "#568FFF", Density: "normal", FontSize: 14, Timezone: "Europe/Moscow"}
}
func (s *Server) preferences(ctx context.Context) (Preferences, error) {
	p := defaultPreferences()
	var raw string
	err := s.DB.QueryRowContext(ctx, "SELECT value FROM settings WHERE key='appearance'").Scan(&raw)
	if err == sql.ErrNoRows {
		return p, nil
	}
	if err != nil {
		return p, err
	}
	err = json.Unmarshal([]byte(raw), &p)
	return p, err
}
func (s *Server) getSettings(w http.ResponseWriter, r *http.Request) {
	p, e := s.preferences(r.Context())
	if e != nil {
		s.dbError(w, e)
		return
	}
	jsonResponse(w, 200, p)
}

var accentPattern = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

func (s *Server) putSettings(w http.ResponseWriter, r *http.Request) {
	var p Preferences
	if !readJSON(w, r, &p) {
		return
	}
	if p.HomePage == "" {
		p.HomePage = "overview"
	}
	switch p.HomePage {
	case "overview", "devices", "incidents", "files":
	default:
		problem(w, 400, "home_page", "Выберите стартовую страницу из списка")
		return
	}
	if p.CardOrder == nil {
		p.CardOrder = defaultPreferences().CardOrder
	}
	allowedCards := map[string]bool{"availability": true, "internet": true, "checks": true, "devices": true}
	if len(p.CardOrder) != len(allowedCards) {
		problem(w, 400, "card_order", "Укажите все четыре карточки обзора")
		return
	}
	for _, card := range p.CardOrder {
		if !allowedCards[card] {
			problem(w, 400, "card_order", "Неизвестная или повторяющаяся карточка")
			return
		}
		delete(allowedCards, card)
	}
	if p.GroupOrder == nil {
		p.GroupOrder = defaultPreferences().GroupOrder
	}
	if len(p.GroupOrder) > 32 {
		problem(w, 400, "group_order", "Не более 32 групп")
		return
	}
	seenGroups := map[string]bool{}
	for i, name := range p.GroupOrder {
		name = strings.TrimSpace(name)
		key := strings.ToLower(name)
		if name == "" || len(name) > 96 || strings.ContainsAny(name, "\r\n") || seenGroups[key] {
			problem(w, 400, "group_order", "Названия групп должны быть уникальны и содержать 1–96 байт")
			return
		}
		seenGroups[key] = true
		p.GroupOrder[i] = name
	}
	if p.DateFormat == "" {
		p.DateFormat = "dmy"
	}
	if p.DateFormat != "dmy" && p.DateFormat != "iso" {
		problem(w, 400, "date_format", "Выберите формат даты из списка")
		return
	}
	_, tzErr := time.LoadLocation(p.Timezone)
	if len(strings.TrimSpace(p.Name)) < 1 || len(p.Name) > 96 || len(p.Subtitle) > 128 || !accentPattern.MatchString(p.Accent) || (p.Theme != "dark" && p.Theme != "light" && p.Theme != "system") || (p.Density != "normal" && p.Density != "compact") || p.FontSize < 12 || p.FontSize > 18 || tzErr != nil {
		problem(w, 400, "preferences", "Проверьте название, тему, цвет, размер шрифта и часовой пояс")
		return
	}
	raw, _ := json.Marshal(p)
	tx, e := s.DB.BeginTx(r.Context(), nil)
	if e != nil {
		s.dbError(w, e)
		return
	}
	defer tx.Rollback()
	if _, e = tx.ExecContext(r.Context(), "INSERT INTO settings(key,value) VALUES('appearance',?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", string(raw)); e != nil {
		s.dbError(w, e)
		return
	}
	u := r.Context().Value(identityKey{}).(User)
	if _, e = tx.ExecContext(r.Context(), "INSERT INTO audit(created_at,user_id,action,detail) VALUES(?,?,?,?)", time.Now().Unix(), u.ID, "settings", "appearance updated"); e != nil {
		s.dbError(w, e)
		return
	}
	if e = tx.Commit(); e != nil {
		s.dbError(w, e)
		return
	}
	s.publish("settings", p)
	jsonResponse(w, 200, p)
}
func (s *Server) users(w http.ResponseWriter, r *http.Request) {
	rows, e := s.DB.QueryContext(r.Context(), "SELECT id,username,role FROM users ORDER BY id LIMIT 1000")
	if e != nil {
		s.dbError(w, e)
		return
	}
	defer rows.Close()
	list := []User{}
	for rows.Next() {
		var u User
		if e = rows.Scan(&u.ID, &u.Username, &u.Role); e != nil {
			s.dbError(w, e)
			return
		}
		list = append(list, u)
	}
	if e = rows.Err(); e != nil {
		s.dbError(w, e)
		return
	}
	jsonResponse(w, 200, list)
}
func (s *Server) createUser(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
		Role     string `json:"role"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	if !validCredentials(credentials{body.Username, body.Password}) || (body.Role != "admin" && body.Role != "user") {
		problem(w, 400, "credentials", "Проверьте логин, пароль и роль")
		return
	}
	if !s.enterAuth(w) {
		return
	}
	defer func() { <-s.authGate }()
	var n int
	if e := s.DB.QueryRowContext(r.Context(), "SELECT COUNT(*) FROM users WHERE username=?", body.Username).Scan(&n); e != nil {
		s.dbError(w, e)
		return
	}
	if n > 0 {
		problem(w, 409, "exists", "Такой пользователь уже существует")
		return
	}
	res, e := s.DB.ExecContext(r.Context(), "INSERT INTO users(username,password_hash,role,created_at) VALUES(?,?,?,?)", body.Username, passwordHash(body.Password), body.Role, time.Now().Unix())
	if e != nil {
		s.dbError(w, e)
		return
	}
	id, _ := res.LastInsertId()
	u := r.Context().Value(identityKey{}).(User)
	if e = s.audit(r.Context(), u.ID, "user_created", body.Username); e != nil {
		s.dbError(w, e)
		return
	}
	jsonResponse(w, 201, User{id, body.Username, body.Role})
}

func (s *Server) publish(kind string, data any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sequence++
	e := event{s.sequence, kind, data}
	for ch := range s.subscribers {
		select {
		case ch <- e:
		default:
			close(ch)
			delete(s.subscribers, ch)
		}
	}
}
func (s *Server) events(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	if len(s.subscribers) >= 64 {
		s.mu.Unlock()
		problem(w, 503, "busy", "Слишком много подключений")
		return
	}
	ch := make(chan event, 8)
	s.subscribers[ch] = struct{}{}
	id := s.sequence
	s.mu.Unlock()
	defer func() { s.mu.Lock(); delete(s.subscribers, ch); s.mu.Unlock() }()
	p, e := s.preferences(r.Context())
	if e != nil {
		s.dbError(w, e)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	controller := http.NewResponseController(w)
	send := func(ev event) error {
		_ = controller.SetWriteDeadline(time.Now().Add(5 * time.Second))
		raw, _ := json.Marshal(ev)
		if _, err := fmt.Fprintf(w, "id: %d\ndata: %s\n\n", ev.ID, raw); err != nil {
			return err
		}
		return controller.Flush()
	}
	if send(event{id, "snapshot", p}) != nil {
		return
	}
	ticker := time.NewTicker(20 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case ev, ok := <-ch:
			if !ok || send(ev) != nil {
				return
			}
		case <-ticker.C:
			if _, _, e := s.identity(r); e != nil {
				return
			}
			_ = controller.SetWriteDeadline(time.Now().Add(5 * time.Second))
			if _, e := io.WriteString(w, ": heartbeat\n\n"); e != nil {
				return
			}
			if controller.Flush() != nil {
				return
			}
		}
	}
}
func (s *Server) CloseStreams() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for ch := range s.subscribers {
		close(ch)
		delete(s.subscribers, ch)
	}
}
