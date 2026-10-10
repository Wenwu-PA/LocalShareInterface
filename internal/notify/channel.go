package notify

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/mail"
	"net/smtp"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"
)

type Channel struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	Kind        string `json:"kind"`
	Enabled     bool   `json:"enabled"`
	Down        bool   `json:"down"`
	Up          bool   `json:"up"`
	Device      bool   `json:"device"`
	Cooldown    int    `json:"cooldown"`
	URLenv      string `json:"url_env"`
	TokenEnv    string `json:"token_env"`
	ChatEnv     string `json:"chat_env"`
	UserEnv     string `json:"user_env"`
	PasswordEnv string `json:"password_env"`
	FromEnv     string `json:"from_env"`
	ToEnv       string `json:"to_env"`
	SourceIP    string `json:"source_ip"`
}
type Event struct {
	ID        int64  `json:"id"`
	MonitorID int64  `json:"monitor_id"`
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	Message   string `json:"message"`
	At        int64  `json:"at"`
}

var EnvPattern = regexp.MustCompile(`^LANBRIDGE_[A-Z0-9_]{1,100}$`)

func Validate(c Channel) error {
	if strings.TrimSpace(c.Name) == "" || len(c.Name) > 128 || c.Cooldown < 5 || c.Cooldown > 86400 {
		return errors.New("имя обязательно; период подавления повторов 5–86400 с")
	}
	refs := []string{}
	switch c.Kind {
	case "webhook":
		refs = []string{c.URLenv}
	case "telegram":
		refs = []string{c.TokenEnv, c.ChatEnv}
	case "smtp":
		refs = []string{c.URLenv, c.FromEnv, c.ToEnv}
	default:
		return errors.New("канал: webhook, telegram или smtp")
	}
	for _, ref := range refs {
		if !EnvPattern.MatchString(ref) {
			return errors.New("укажите имена переменных LANBRIDGE_… с адресами и секретами")
		}
	}
	for _, ref := range []string{c.URLenv, c.TokenEnv, c.ChatEnv, c.UserEnv, c.PasswordEnv, c.FromEnv, c.ToEnv} {
		if ref != "" && !EnvPattern.MatchString(ref) {
			return errors.New("некорректное имя переменной")
		}
	}
	if c.Kind == "smtp" && (c.UserEnv == "") != (c.PasswordEnv == "") {
		return errors.New("укажите обе переменные: логин и пароль SMTP")
	}
	if c.SourceIP != "" && net.ParseIP(c.SourceIP) == nil {
		return errors.New("исходящий IP должен быть адресом интерфейса сервера")
	}
	return nil
}
func safeURL(raw string) (*url.URL, error) {
	u, e := url.Parse(raw)
	if e != nil || u.Hostname() == "" || u.User != nil {
		return nil, errors.New("некорректный адрес канала")
	}
	ip := net.ParseIP(u.Hostname())
	if u.Scheme != "https" && (u.Scheme != "http" || (u.Hostname() != "localhost" && (ip == nil || !ip.IsLoopback()))) {
		return nil, errors.New("нужен HTTPS; HTTP допустим только для loopback")
	}
	return u, nil
}
func client(source string) (*http.Client, func()) {
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	if source != "" {
		dialer.LocalAddr = &net.TCPAddr{IP: net.ParseIP(source)}
	}
	transport := &http.Transport{DialContext: dialer.DialContext, TLSHandshakeTimeout: 10 * time.Second, DisableKeepAlives: true}
	return &http.Client{Transport: transport, Timeout: 10 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}, transport.CloseIdleConnections
}
func Deliver(ctx context.Context, c Channel, event Event) error {
	if err := Validate(c); err != nil {
		return err
	}
	if c.Kind == "smtp" {
		return deliverSMTP(ctx, c, event)
	}
	target := os.Getenv(c.URLenv)
	var payload any = event
	if c.Kind == "telegram" {
		token := os.Getenv(c.TokenEnv)
		chat := os.Getenv(c.ChatEnv)
		if token == "" || chat == "" || strings.ContainsAny(token, "/?#\r\n") {
			return errors.New("не заданы переменные Telegram")
		}
		target = "https://api.telegram.org/bot" + token + "/sendMessage"
		payload = map[string]string{"chat_id": chat, "text": fmt.Sprintf("LANBridge · %s\n%s\n%s", event.Kind, event.Name, event.Message)}
	}
	u, err := safeURL(target)
	if err != nil {
		return err
	}
	raw, _ := json.Marshal(payload)
	request, err := http.NewRequestWithContext(ctx, "POST", u.String(), bytes.NewReader(raw))
	if err != nil {
		return errors.New("не удалось подготовить уведомление")
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", fmt.Sprintf("lanbridge-%d-%d-%s", c.ID, event.ID, event.Kind))
	httpClient, closeClient := client(c.SourceIP)
	defer closeClient()
	response, err := httpClient.Do(request)
	if err != nil {
		return errors.New("канал недоступен или истёк таймаут")
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("канал вернул HTTP %d", response.StatusCode)
	}
	if c.Kind == "telegram" {
		var result struct {
			OK bool `json:"ok"`
		}
		if json.NewDecoder(io.LimitReader(response.Body, 65536)).Decode(&result) != nil || !result.OK {
			return errors.New("Telegram не подтвердил доставку")
		}
	}
	return nil
}
func Heartbeat(ctx context.Context, env string) error {
	if !EnvPattern.MatchString(env) {
		return errors.New("некорректное имя переменной heartbeat")
	}
	u, e := safeURL(os.Getenv(env))
	if e != nil {
		return e
	}
	r, e := http.NewRequestWithContext(ctx, "GET", u.String(), nil)
	if e != nil {
		return errors.New("некорректный адрес heartbeat")
	}
	c, closeClient := client("")
	defer closeClient()
	resp, e := c.Do(r)
	if e != nil {
		return errors.New("наблюдатель недоступен")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("наблюдатель вернул HTTP %d", resp.StatusCode)
	}
	return nil
}
func deliverSMTP(ctx context.Context, c Channel, event Event) error {
	u, e := url.Parse(os.Getenv(c.URLenv))
	if e != nil || u.Hostname() == "" || u.User != nil || (u.Scheme != "smtps" && u.Scheme != "smtp") {
		return errors.New("SMTP URL: smtp://host:587 или smtps://host:465")
	}
	from, e := mail.ParseAddress(os.Getenv(c.FromEnv))
	if e != nil {
		return errors.New("некорректный отправитель SMTP")
	}
	to, e := mail.ParseAddress(os.Getenv(c.ToEnv))
	if e != nil {
		return errors.New("некорректный получатель SMTP")
	}
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	if c.SourceIP != "" {
		dialer.LocalAddr = &net.TCPAddr{IP: net.ParseIP(c.SourceIP)}
	}
	conn, e := dialer.DialContext(ctx, "tcp", u.Host)
	if e != nil {
		return errors.New("SMTP недоступен")
	}
	defer conn.Close()
	deadline := time.Now().Add(10 * time.Second)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	_ = conn.SetDeadline(deadline)
	cfg := &tls.Config{ServerName: u.Hostname(), MinVersion: tls.VersionTLS12}
	if u.Scheme == "smtps" {
		tlsConn := tls.Client(conn, cfg)
		if e = tlsConn.HandshakeContext(ctx); e != nil {
			return errors.New("не удалось установить SMTP TLS")
		}
		conn = tlsConn
	}
	smtpClient, e := smtp.NewClient(conn, u.Hostname())
	if e != nil {
		return errors.New("SMTP handshake не выполнен")
	}
	defer smtpClient.Close()
	if u.Scheme == "smtp" {
		if e = smtpClient.StartTLS(cfg); e != nil {
			return errors.New("SMTP STARTTLS обязателен")
		}
	}
	if c.UserEnv != "" {
		if e = smtpClient.Auth(smtp.PlainAuth("", os.Getenv(c.UserEnv), os.Getenv(c.PasswordEnv), u.Hostname())); e != nil {
			return errors.New("SMTP отказал в авторизации")
		}
	}
	if e = smtpClient.Mail(from.Address); e != nil {
		return errors.New("SMTP отклонил отправителя")
	}
	if e = smtpClient.Rcpt(to.Address); e != nil {
		return errors.New("SMTP отклонил получателя")
	}
	writer, e := smtpClient.Data()
	if e != nil {
		return errors.New("SMTP не принял письмо")
	}
	body := fmt.Sprintf("From: %s\r\nTo: %s\r\nSubject: LANBridge network event\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=UTF-8\r\n\r\n%s\n%s\n%s\n", from.String(), to.String(), event.Kind, event.Name, event.Message)
	if _, e = io.WriteString(writer, body); e != nil {
		return errors.New("SMTP: ошибка передачи")
	}
	if e = writer.Close(); e != nil {
		return errors.New("SMTP: доставка не подтверждена")
	}
	return nil
}
