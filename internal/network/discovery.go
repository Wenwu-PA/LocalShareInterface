package network

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"lanbridge/internal/monitor"
	"lanbridge/internal/notify"
	"log/slog"
	"net"
	"net/netip"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"
)

type Interface struct {
	Name   string `json:"name"`
	IP     string `json:"ip"`
	Subnet string `json:"subnet"`
	MAC    string `json:"mac"`
}

func Interfaces() []Interface {
	result := []Interface{}
	list, _ := net.Interfaces()
	for _, device := range list {
		if device.Flags&net.FlagUp == 0 || device.Flags&net.FlagLoopback != 0 {
			continue
		}
		addresses, _ := device.Addrs()
		for _, address := range addresses {
			p, e := netip.ParsePrefix(address.String())
			if e != nil || !p.Addr().Is4() || !p.Addr().IsPrivate() {
				continue
			}
			bits := max(p.Bits(), 24)
			result = append(result, Interface{device.Name, p.Addr().String(), netip.PrefixFrom(p.Addr(), bits).Masked().String(), device.HardwareAddr.String()})
		}
	}
	return result
}
func ValidateSubnet(raw string) (netip.Prefix, error) {
	prefix, e := netip.ParsePrefix(raw)
	if e != nil || !prefix.Addr().Is4() || !prefix.Addr().IsPrivate() || prefix.Bits() < 24 {
		return netip.Prefix{}, errors.New("выберите локальную IPv4-подсеть /24 или меньше")
	}
	prefix = prefix.Masked()
	for _, iface := range Interfaces() {
		local, _ := netip.ParsePrefix(iface.Subnet)
		if local.Contains(prefix.Addr()) && prefix.Bits() >= local.Bits() {
			return prefix, nil
		}
	}
	return netip.Prefix{}, errors.New("подсеть не принадлежит активному локальному интерфейсу")
}

type Observation struct {
	IP, MAC, Hostname string
	Online            bool
}
type Progress struct {
	Running bool   `json:"running"`
	Subnet  string `json:"subnet"`
	Checked int    `json:"checked"`
	Total   int    `json:"total"`
	Error   string `json:"error"`
}
type Discovery struct {
	db         *sql.DB
	log        *slog.Logger
	publish    func(string, any)
	mu         sync.Mutex
	state      Progress
	ctx        context.Context
	cancel     context.CancelFunc
	scanCancel context.CancelFunc
	wg         sync.WaitGroup
	closing    bool
}

func New(db *sql.DB, log *slog.Logger, publish func(string, any)) *Discovery {
	return &Discovery{db: db, log: log, publish: publish, ctx: context.Background()}
}
func (d *Discovery) Status() Progress { d.mu.Lock(); defer d.mu.Unlock(); return d.state }
func (d *Discovery) Start(parent context.Context) {
	d.ctx, d.cancel = context.WithCancel(parent)
	d.wg.Add(1)
	go func() {
		defer d.wg.Done()
		tick := time.NewTicker(5 * time.Second)
		defer tick.Stop()
		for {
			select {
			case <-d.ctx.Done():
				return
			case <-tick.C:
				var enabled bool
				var subnet string
				var interval int
				var last int64
				if e := d.db.QueryRowContext(d.ctx, "SELECT enabled,subnet,interval,last_scan FROM discovery_config WHERE id=1").Scan(&enabled, &subnet, &interval, &last); e == nil && enabled && time.Now().Unix()-last >= int64(interval) {
					if e = d.Scan(subnet); e != nil && !d.Status().Running {
						_, _ = d.db.ExecContext(d.ctx, "UPDATE discovery_config SET last_scan=?,last_error=? WHERE id=1", time.Now().Unix(), e.Error())
					}
				}
			}
		}
	}()
}
func (d *Discovery) Close() {
	d.mu.Lock()
	d.closing = true
	if d.cancel != nil {
		d.cancel()
	}
	if d.scanCancel != nil {
		d.scanCancel()
	}
	d.mu.Unlock()
	d.wg.Wait()
}
func (d *Discovery) Cancel() {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.scanCancel != nil {
		d.scanCancel()
	}
}
func (d *Discovery) Scan(subnet string) error {
	prefix, e := ValidateSubnet(subnet)
	if e != nil {
		return e
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closing {
		return errors.New("сервер останавливается")
	}
	if d.state.Running {
		return errors.New("сканирование уже выполняется")
	}
	ctx, cancel := context.WithTimeout(d.ctx, 2*time.Minute)
	d.scanCancel = cancel
	d.state = Progress{Running: true, Subnet: prefix.String(), Total: 1 << (32 - prefix.Bits())}
	d.wg.Add(1)
	go func() {
		defer d.wg.Done()
		defer cancel()
		var scanErr error
		defer func() {
			if recover() != nil {
				scanErr = errors.New("сканирование прервано внутренней ошибкой")
			}
			message := ""
			if scanErr != nil {
				message = scanErr.Error()
			}
			save, c := context.WithTimeout(context.Background(), 5*time.Second)
			defer c()
			if _, e := d.db.ExecContext(save, "UPDATE discovery_config SET last_scan=?,last_error=? WHERE id=1", time.Now().Unix(), message); e != nil {
				d.log.Error("discovery state write failed", "error", e)
			}
			d.mu.Lock()
			d.state.Running = false
			d.state.Error = message
			d.mu.Unlock()
			d.publish("devices", d.Status())
		}()
		scanErr = d.run(ctx, prefix)
	}()
	return nil
}
func (d *Discovery) run(ctx context.Context, prefix netip.Prefix) error {
	observations := map[string]Observation{}
	var lock sync.Mutex
	var workers sync.WaitGroup
	slots := make(chan struct{}, 4)
	tick := time.NewTicker(250 * time.Millisecond)
	defer tick.Stop()
	for ip := prefix.Addr(); prefix.Contains(ip); ip = ip.Next() {
		select {
		case <-ctx.Done():
			workers.Wait()
			return ctx.Err()
		case <-tick.C:
		}
		select {
		case slots <- struct{}{}:
		case <-ctx.Done():
			workers.Wait()
			return ctx.Err()
		}
		workers.Add(1)
		go func(ip netip.Addr) {
			defer workers.Done()
			defer func() { <-slots }()
			r := monitor.Probe(ctx, 0, monitor.Definition{Kind: "icmp", Target: ip.String(), Timeout: 1}, 0)
			lock.Lock()
			if r.OK {
				observations[ip.String()] = Observation{IP: ip.String(), Online: true}
			}
			lock.Unlock()
			d.mu.Lock()
			d.state.Checked++
			d.mu.Unlock()
		}(ip)
	}
	workers.Wait()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	for ip, mac := range neighbors(ctx, prefix) {
		o := observations[ip]
		o.IP = ip
		o.MAC = mac
		observations[ip] = o
	}
	for _, iface := range Interfaces() {
		if ip, e := netip.ParseAddr(iface.IP); e == nil && prefix.Contains(ip) {
			o := observations[iface.IP]
			o.IP = iface.IP
			o.MAC = iface.MAC
			o.Online = true
			observations[iface.IP] = o
		}
	}
	discoverMDNS(ctx, prefix, observations)
	return d.Persist(ctx, prefix, observations, time.Now().Unix())
}
func neighbors(ctx context.Context, prefix netip.Prefix) map[string]string {
	name, args := "arp", []string{"-an"}
	if runtime.GOOS == "windows" {
		args = []string{"-a"}
	} else if runtime.GOOS == "linux" {
		name = "ip"
		args = []string{"neigh", "show"}
	}
	commandCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	cmd := exec.CommandContext(commandCtx, name, args...)
	hideWindow(cmd)
	output := &limitedOutput{}
	cmd.Stdout = output
	if cmd.Run() != nil {
		return map[string]string{}
	}
	return parseNeighbors(output.String(), prefix)
}
func parseNeighbors(raw string, prefix netip.Prefix) map[string]string {
	result := map[string]string{}
	for _, line := range strings.Split(raw, "\n") {
		var ip, mac string
		for _, word := range strings.Fields(line) {
			word = strings.Trim(word, "()")
			if parsed, e := netip.ParseAddr(word); e == nil && prefix.Contains(parsed) {
				ip = word
			}
			if parsed, e := net.ParseMAC(word); e == nil && len(parsed) == 6 && parsed[0]&1 == 0 && parsed.String() != "00:00:00:00:00:00" {
				mac = parsed.String()
			}
		}
		if ip != "" && mac != "" {
			result[ip] = mac
		}
	}
	return result
}
func (d *Discovery) Persist(ctx context.Context, prefix netip.Prefix, observations map[string]Observation, now int64) error {
	tx, e := d.db.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	rows, e := tx.QueryContext(ctx, "SELECT id,ip,status,mac FROM devices")
	if e != nil {
		return e
	}
	type prior struct {
		id              int64
		ip, status, mac string
	}
	old := []prior{}
	for rows.Next() {
		var p prior
		if e = rows.Scan(&p.id, &p.ip, &p.status, &p.mac); e != nil {
			rows.Close()
			return e
		}
		old = append(old, p)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	for _, p := range old {
		if p.mac != "" {
			moved := false
			for _, observed := range observations {
				if observed.MAC == p.mac {
					moved = true
					break
				}
			}
			if moved {
				continue
			}
		}
		ip, err := netip.ParseAddr(p.ip)
		if err != nil || !prefix.Contains(ip) {
			continue
		}
		if observed, found := observations[p.ip]; !found || (p.mac != "" && observed.MAC != "" && p.mac != observed.MAC) {
			if _, e = tx.ExecContext(ctx, "UPDATE devices SET status='offline',checked=? WHERE id=?", now, p.id); e != nil {
				return e
			}
			if p.status != "offline" {
				if _, e = tx.ExecContext(ctx, "INSERT INTO device_events(device_id,at,status) VALUES(?,?,'offline')", p.id, now); e != nil {
					return e
				}
			}
		}
	}
	for _, o := range observations {
		identity := "ip:" + o.IP
		if o.MAC != "" {
			identity = "mac:" + o.MAC
		}
		var id int64
		var previous string
		var previousIdentity string
		e = tx.QueryRowContext(ctx, "SELECT id,status,identity FROM devices WHERE identity=? OR (ip=? AND (mac='' OR ?='')) ORDER BY identity=? DESC,last_seen DESC LIMIT 1", identity, o.IP, o.MAC, identity).Scan(&id, &previous, &previousIdentity)
		fresh := e == sql.ErrNoRows
		if !fresh && o.MAC == "" {
			identity = previousIdentity
		}
		if e != nil && !fresh {
			return e
		}
		status := "unknown"
		seen := int64(0)
		if o.Online {
			status = "online"
			seen = now
		}
		if fresh {
			res, err := tx.ExecContext(ctx, "INSERT INTO devices(identity,ip,mac,hostname,status,first_seen,last_seen,checked) VALUES(?,?,?,?,?,?,?,?)", identity, o.IP, o.MAC, o.Hostname, status, now, seen, now)
			if err != nil {
				return err
			}
			id, _ = res.LastInsertId()
			if e = notify.Enqueue(ctx, tx, notify.Event{ID: id, MonitorID: -id, Kind: "device", Name: o.Hostname, Message: fmt.Sprintf("Новое устройство: %s %s", o.IP, o.MAC), At: now}); e != nil {
				return e
			}
		} else {
			if _, e = tx.ExecContext(ctx, "UPDATE devices SET identity=?,ip=?,mac=CASE WHEN ?='' THEN mac ELSE ? END,hostname=CASE WHEN ?='' THEN hostname ELSE ? END,status=?,last_seen=MAX(last_seen,?),checked=? WHERE id=?", identity, o.IP, o.MAC, o.MAC, o.Hostname, o.Hostname, status, seen, now, id); e != nil {
				return e
			}
		}
		if fresh || previous != status {
			if _, e = tx.ExecContext(ctx, "INSERT INTO device_events(device_id,at,status) VALUES(?,?,?)", id, now, status); e != nil {
				return e
			}
		}
	}
	if _, e = tx.ExecContext(ctx, "DELETE FROM device_events WHERE at<?", now-365*86400); e != nil {
		return e
	}
	return tx.Commit()
}

type limitedOutput struct{ strings.Builder }

func (b *limitedOutput) Write(p []byte) (int, error) {
	n := len(p)
	if b.Len() < 65536 {
		b.Builder.Write(p[:min(n, 65536-b.Len())])
	}
	return n, nil
}
