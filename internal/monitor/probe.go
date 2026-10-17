package monitor

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os/exec"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

type Definition struct {
	Name            string `json:"name"`
	Kind            string `json:"kind"`
	Target          string `json:"target"`
	Group           string `json:"group"`
	Tags            string `json:"tags"`
	Interval        int    `json:"interval"`
	Timeout         int    `json:"timeout"`
	Retries         int    `json:"retries"`
	LatencyLimit    int    `json:"latency_limit"`
	ExpectedStatus  int    `json:"expected_status"`
	Keyword         string `json:"keyword"`
	Method          string `json:"method"`
	Record          string `json:"record"`
	Resolver        string `json:"resolver"`
	CertificateDays int    `json:"certificate_days"`
	Paused          bool   `json:"paused"`
	Order           int    `json:"order"`
}
type State struct {
	Status   string  `json:"status"`
	Checked  int64   `json:"checked"`
	Latency  float64 `json:"latency"`
	Failures int     `json:"failures"`
	Message  string  `json:"message"`
}
type Monitor struct {
	ID int64 `json:"id"`
	Definition
	State State `json:"state"`
}
type Result struct {
	// Invalidated only on worker replacement, not on normal server shutdown.
	obsolete   *atomic.Bool
	definition *Definition
	ID         int64
	At         int64
	OK         bool
	Latency    float64
	Message    string
	Interval   int
	Retries    int
}

var hostPattern = regexp.MustCompile(`^[a-zA-Z0-9_.:-]+$`)
var pingReply = regexp.MustCompile(`(?i)(ttl[= :]|time[=<]|[=<]\s*\d+\s*[^\s\d]{1,4}(?:\s|$))`)

type limitedOutput struct{ bytes.Buffer }

func (b *limitedOutput) Write(p []byte) (int, error) {
	n := len(p)
	remaining := 4096 - b.Len()
	if remaining > 0 {
		if len(p) > remaining {
			p = p[:remaining]
		}
		_, _ = b.Buffer.Write(p)
	}
	return n, nil
}

var httpTransport = &http.Transport{Proxy: nil, MaxIdleConns: 32, MaxIdleConnsPerHost: 2, MaxConnsPerHost: 8, IdleConnTimeout: 30 * time.Second, TLSHandshakeTimeout: 5 * time.Second, ResponseHeaderTimeout: 10 * time.Second}

func Validate(d Definition) error {
	if strings.TrimSpace(d.Name) == "" || len(d.Name) > 128 || len(d.Group) > 80 || len(d.Tags) > 256 {
		return errors.New("проверьте имя, группу и теги")
	}
	if d.Interval < 5 || d.Interval > 86400 || d.Timeout < 1 || d.Timeout > 60 || d.Timeout > d.Interval || d.Retries < 1 || d.Retries > 20 || d.LatencyLimit < 0 || d.CertificateDays < 0 || d.CertificateDays > 365 {
		return errors.New("интервал 5–86400 с, таймаут 1–60 с не больше интервала, повторы 1–20")
	}
	switch d.Kind {
	case "http":
		u, e := url.Parse(d.Target)
		if e != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil {
			return errors.New("нужен HTTP(S)-адрес без логина и пароля в URL")
		}
		if d.Method != "GET" && d.Method != "HEAD" {
			return errors.New("допустимы GET и HEAD")
		}
		if d.ExpectedStatus < 100 || d.ExpectedStatus > 599 || len(d.Keyword) > 1024 {
			return errors.New("проверьте ожидаемый код и ключевое слово")
		}
		if d.Method == "HEAD" && d.Keyword != "" {
			return errors.New("HEAD не проверяет ключевое слово")
		}
	case "tcp":
		if _, p, e := net.SplitHostPort(d.Target); e != nil {
			return errors.New("нужен адрес host:port")
		} else if n, e := strconv.Atoi(p); e != nil || n < 1 || n > 65535 {
			return errors.New("порт должен быть от 1 до 65535")
		}
	case "icmp", "dns":
		if !hostPattern.MatchString(d.Target) || strings.HasPrefix(d.Target, "-") || len(d.Target) > 253 {
			return errors.New("некорректное имя хоста или IP")
		}
		if d.Kind == "dns" {
			switch d.Record {
			case "A", "AAAA", "CNAME", "MX", "TXT", "NS":
			default:
				return errors.New("поддерживаются A, AAAA, CNAME, MX, TXT, NS")
			}
			if d.Resolver != "" {
				if _, _, e := net.SplitHostPort(d.Resolver); e != nil {
					return errors.New("DNS-резолвер задаётся как IP:порт")
				}
			}
		}
	case "heartbeat":
	default:
		return errors.New("неизвестный тип проверки")
	}
	return nil
}
func HashToken(value string) string {
	h := sha256.Sum256([]byte(value))
	return hex.EncodeToString(h[:])
}

func Probe(ctx context.Context, id int64, d Definition, lastHeartbeat int64) (r Result) {
	started := time.Now()
	r = Result{definition: &d, ID: id, At: started.Unix(), Interval: d.Interval, Retries: d.Retries}
	defer func() {
		r.Latency = float64(time.Since(started).Microseconds()) / 1000
		if recover() != nil {
			r.OK = false
			r.Message = "Внутренняя ошибка проверки"
		}
		if r.OK && d.LatencyLimit > 0 && r.Latency > float64(d.LatencyLimit) {
			r.OK = false
			r.Message = "Превышен порог задержки"
		}
	}()
	ctx, cancel := context.WithTimeout(ctx, time.Duration(d.Timeout)*time.Second)
	defer cancel()
	var err error
	switch d.Kind {
	case "tcp":
		var conn net.Conn
		conn, err = (&net.Dialer{}).DialContext(ctx, "tcp", d.Target)
		if err == nil {
			_ = conn.Close()
		}
	case "http":
		var request *http.Request
		request, err = http.NewRequestWithContext(ctx, d.Method, d.Target, nil)
		if err != nil {
			break
		}
		client := &http.Client{Transport: httpTransport, CheckRedirect: func(req *http.Request, via []*http.Request) error { return http.ErrUseLastResponse }}
		var response *http.Response
		response, err = client.Do(request)
		if err != nil {
			break
		}
		defer response.Body.Close()
		if response.StatusCode != d.ExpectedStatus {
			err = fmt.Errorf("HTTP %d вместо %d", response.StatusCode, d.ExpectedStatus)
			break
		}
		if d.Keyword != "" {
			var body []byte
			body, err = io.ReadAll(io.LimitReader(response.Body, 512*1024))
			if err == nil && !strings.Contains(string(body), d.Keyword) {
				err = errors.New("ключевое слово не найдено в первых 512 КиБ")
			}
		}
		if err == nil && response.TLS != nil && len(response.TLS.PeerCertificates) > 0 && time.Until(response.TLS.PeerCertificates[0].NotAfter) < time.Duration(d.CertificateDays)*24*time.Hour {
			err = errors.New("приближается окончание срока сертификата")
		}
	case "dns":
		resolver := &net.Resolver{PreferGo: true}
		if d.Resolver != "" {
			resolver.Dial = func(ctx context.Context, network, address string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, network, d.Resolver)
			}
		}
		count := 0
		switch d.Record {
		case "A", "AAAA":
			network := "ip4"
			if d.Record == "AAAA" {
				network = "ip6"
			}
			var v []net.IP
			v, err = resolver.LookupIP(ctx, network, d.Target)
			count = len(v)
		case "CNAME":
			var v string
			v, err = resolver.LookupCNAME(ctx, d.Target)
			if v != "" {
				count = 1
			}
		case "MX":
			var v []*net.MX
			v, err = resolver.LookupMX(ctx, d.Target)
			count = len(v)
		case "TXT":
			var v []string
			v, err = resolver.LookupTXT(ctx, d.Target)
			count = len(v)
		case "NS":
			var v []*net.NS
			v, err = resolver.LookupNS(ctx, d.Target)
			count = len(v)
		}
		if err == nil && count == 0 {
			err = errors.New("DNS-запись не найдена")
		}
	case "icmp":
		args := []string{"-n", "-c", "1", "-W", strconv.Itoa(d.Timeout), d.Target}
		if runtime.GOOS == "windows" {
			args = []string{"-n", "1", "-w", strconv.Itoa(d.Timeout * 1000), d.Target}
		}
		command := exec.CommandContext(ctx, "ping", args...)
		hideWindow(command)
		var output limitedOutput
		command.Stdout = &output
		command.Stderr = &output
		err = command.Run()
		if err == nil && !pingReply.Match(output.Bytes()) {
			err = errors.New("ICMP: ответ не подтверждён")
		}
		if err != nil {
			err = errors.New("ICMP: нет ответа или системный ping недоступен")
		}
	case "heartbeat":
		if lastHeartbeat == 0 || started.Unix()-lastHeartbeat > int64(d.Interval) {
			err = errors.New("heartbeat не получен вовремя")
		}
	default:
		err = errors.New("неизвестный тип проверки")
	}
	r.OK = err == nil
	if err != nil {
		switch {
		case ctx.Err() != nil:
			r.Message = "Тайм-аут проверки"
		case d.Kind == "tcp":
			r.Message = "TCP-соединение недоступно"
		case d.Kind == "dns":
			r.Message = "DNS-проверка не прошла"
		case d.Kind == "http" && strings.Contains(err.Error(), "Get "):
			r.Message = "HTTP(S)-соединение недоступно"
		default:
			r.Message = err.Error()
		}
		if len(r.Message) > 256 {
			r.Message = r.Message[:256]
		}
	}
	return r
}
