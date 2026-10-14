package server

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type externalEvent struct {
	Time   int64  `json:"time"`
	Method string `json:"method"`
	Route  string `json:"route"`
	Status int    `json:"status"`
	Bytes  int64  `json:"bytes"`
}

var guestPagePath = regexp.MustCompile(`^/s/[A-Za-z0-9_-]{43}$`)
var guestAPIPath = regexp.MustCompile(`^/api/guest/[A-Za-z0-9_-]{43}$`)
var guestTicketPath = regexp.MustCompile(`^/api/guest/[A-Za-z0-9_-]{43}/ticket$`)
var guestDownloadPath = regexp.MustCompile(`^/api/guest-download/[A-Za-z0-9_-]{43}$`)

// A strict allowlist is applied before routing/redirect normalization.
func externalRoute(r *http.Request) string {
	p := r.URL.Path
	if r.URL.RawPath != "" || strings.Contains(p, "..") || strings.Contains(p, "//") || strings.Contains(p, "\\") {
		return "blocked"
	}
	if r.Method == "GET" || r.Method == "HEAD" {
		switch {
		case guestPagePath.MatchString(p):
			return "guest-page"
		case guestAPIPath.MatchString(p):
			return "guest-info"
		case guestDownloadPath.MatchString(p):
			return "guest-download"
		case p == "/api/status":
			return "guest-status"
		case strings.HasPrefix(p, "/assets/"):
			return "static"
		case strings.HasPrefix(p, "/licenses/"):
			return "static"
		}
	}
	if r.Method == "POST" && guestTicketPath.MatchString(p) {
		return "guest-ticket"
	}
	return "blocked"
}
func (s *Server) guestGateway(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tracker := &downloadWriter{ResponseWriter: w}
		route := externalRoute(r)
		defer func() {
			s.externalMu.Lock()
			defer s.externalMu.Unlock()
			s.externalRequests++
			s.externalBytes += uint64(tracker.bytes)
			status := tracker.status
			if status == 0 {
				status = 200
			}
			method := r.Method
			if len(method) > 16 {
				method = "OTHER"
			}
			s.externalEvents = append(s.externalEvents, externalEvent{time.Now().Unix(), method, route, status, tracker.bytes})
			if len(s.externalEvents) > 100 {
				s.externalEvents = s.externalEvents[len(s.externalEvents)-100:]
			}
		}()
		state := s.External.Status()
		public, e := url.Parse(state.URL)
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if state.State != "running" || e != nil || public.Host == "" {
			problem(tracker, 503, "external_off", "Внешний доступ выключен")
			return
		}
		if r.Host != public.Host {
			problem(tracker, 421, "host", "Неизвестный адрес шлюза")
			return
		}
		if r.Header.Get("X-Forwarded-Proto") != "https" {
			problem(tracker, 426, "https", "Откройте ссылку через HTTPS")
			return
		}
		if route == "blocked" {
			problem(tracker, 404, "not_found", "Снаружи доступны только гостевые ссылки")
			return
		}
		if origin := r.Header.Get("Origin"); origin != "" && origin != public.Scheme+"://"+public.Host {
			problem(tracker, 403, "origin", "Источник запроса не разрешён")
			return
		}
		// Never let a copied LAN session cookie affect the public gateway.
		clone := r.Clone(r.Context())
		clone.Header.Del("Authorization")
		var csrf *http.Cookie
		if c, e := r.Cookie(csrfCookie); e == nil {
			csrf = c
		}
		clone.Header.Del("Cookie")
		if csrf != nil {
			clone.AddCookie(csrf)
		}
		// Cloudflare supplies this header, but a local process can forge it. Use one
		// gateway bucket for password throttling rather than trusting forwarded IPs.
		clone.RemoteAddr = "external-gateway"
		if route == "guest-status" {
			token := csrfValue(clone)
			if token == "" {
				token = randomToken()
				setCookie(tracker, csrfCookie, token, 86400)
			}
			jsonResponse(tracker, 200, map[string]any{"csrf": token, "authenticated": false})
			return
		}
		next.ServeHTTP(tracker, clone)
	})
}
func (s *Server) externalRoutes(mux *http.ServeMux) {
	mux.Handle("GET /api/external", s.auth(http.HandlerFunc(s.externalStatus), true))
	mux.Handle("POST /api/external/start", s.auth(http.HandlerFunc(s.startExternal), true))
	mux.Handle("POST /api/external/stop", s.auth(http.HandlerFunc(s.stopExternal), true))
	mux.Handle("POST /api/external/network", s.auth(http.HandlerFunc(s.externalNetwork), true))
}
func (s *Server) externalStatus(w http.ResponseWriter, r *http.Request) {
	var admins, shares int
	if e := s.DB.QueryRowContext(r.Context(), "SELECT COUNT(*) FROM users WHERE role='admin'").Scan(&admins); e != nil {
		s.dbError(w, e)
		return
	}
	if e := s.DB.QueryRowContext(r.Context(), "SELECT COUNT(*) FROM shares WHERE revoked=0 AND expires>? AND (max_downloads=0 OR downloads<max_downloads)", time.Now().Unix()).Scan(&shares); e != nil {
		s.dbError(w, e)
		return
	}
	s.externalMu.Lock()
	events := append([]externalEvent{}, s.externalEvents...)
	requests, bytes := s.externalRequests, s.externalBytes
	s.externalMu.Unlock()
	jsonResponse(w, 200, map[string]any{"tunnel": s.External.Status(), "installed": s.External.Available(), "https": r.TLS != nil, "admin_ready": admins > 0, "storage_ready": s.Files != nil, "guest_only": true, "active_links": shares, "requests": requests, "bytes": bytes, "events": events, "providers": []map[string]any{{"id": "cloudflare", "name": "Cloudflare Quick Tunnel", "implemented": true}, {"id": "netbird", "name": "NetBird", "implemented": false}, {"id": "ssh", "name": "SSH / VPS", "implemented": false}}})
}
func (s *Server) startExternal(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Provider string `json:"provider"`
		Confirm  bool   `json:"confirm_public_guest_access"`
	}
	if !readJSON(w, r, &b) {
		return
	}
	if b.Provider != "cloudflare" || !b.Confirm {
		problem(w, 400, "confirmation", "Выберите Cloudflare и подтвердите публикацию гостевых ссылок")
		return
	}
	if r.TLS == nil || s.Files == nil || s.externalHandler == nil {
		problem(w, 409, "preflight", "Нужны HTTPS и готовое хранилище")
		return
	}
	if e := s.External.Start(s.externalHandler); e != nil {
		problem(w, 409, "tunnel", e.Error())
		return
	}
	if e := s.audit(r.Context(), fileActor(r).ID, "external.start", "cloudflare guest-only"); e != nil {
		s.Log.Error("external audit failed", "error", e)
	}
	jsonResponse(w, 202, s.External.Status())
}
func (s *Server) stopExternal(w http.ResponseWriter, r *http.Request) {
	if e := s.External.Stop(); e != nil {
		problem(w, 503, "tunnel", e.Error())
		return
	}
	if e := s.audit(r.Context(), fileActor(r).ID, "external.stop", "gateway closed"); e != nil {
		s.Log.Error("external audit failed", "error", e)
	}
	jsonResponse(w, 200, s.External.Status())
}
func classifyWAN(raw, observed string) string {
	ip := net.ParseIP(raw)
	public := net.ParseIP(observed)
	if ip == nil || ip.To4() == nil {
		return "unknown"
	}
	_, shared, _ := net.ParseCIDR("100.64.0.0/10")
	if shared.Contains(ip) {
		return "shared"
	}
	if ip.IsPrivate() {
		return "private"
	}
	if !ip.IsGlobalUnicast() || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
		return "invalid"
	}
	if public == nil {
		return "unknown"
	}
	if ip.Equal(public) {
		return "match"
	}
	return "different"
}
func (s *Server) externalNetwork(w http.ResponseWriter, r *http.Request) {
	var b struct {
		WAN string `json:"wan_ip"`
	}
	if !readJSON(w, r, &b) {
		return
	}
	if b.WAN != "" && (net.ParseIP(b.WAN) == nil || net.ParseIP(b.WAN).To4() == nil) {
		problem(w, 400, "wan_ip", "Укажите IPv4 WAN-интерфейса роутера или оставьте поле пустым")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", "https://www.cloudflare.com/cdn-cgi/trace", nil)
	client := &http.Client{Timeout: 8 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, e := client.Do(req)
	if e != nil {
		problem(w, 502, "network", "Не удалось получить внешний IP от Cloudflare")
		return
	}
	defer resp.Body.Close()
	data, e := io.ReadAll(io.LimitReader(resp.Body, 8193))
	if e != nil || len(data) > 8192 || resp.StatusCode != 200 {
		problem(w, 502, "network", "Некорректный ответ сервиса внешнего IP")
		return
	}
	observed := ""
	for _, line := range strings.Split(string(data), "\n") {
		key, value, ok := strings.Cut(line, "=")
		if ok && key == "ip" && net.ParseIP(value) != nil {
			observed = value
		}
	}
	if observed == "" {
		problem(w, 502, "network", "Сервис не вернул IP")
		return
	}
	jsonResponse(w, 200, map[string]string{"public_ip": observed, "wan_ip": b.WAN, "classification": classifyWAN(b.WAN, observed), "checked": strconv.FormatInt(time.Now().Unix(), 10)})
}
