package network

import (
	"context"
	"golang.org/x/net/dns/dnsmessage"
	"io"
	"lanbridge/internal/store"
	"log/slog"
	"net/netip"
	"path/filepath"
	"testing"
)

func TestNeighborParsingAndSubnetBoundaries(t *testing.T) {
	prefix := netip.MustParsePrefix("192.168.1.0/24")
	raw := "  192.168.1.5  00-1b-21-22-33-44 dynamic\n? (192.168.1.6) at 00:03:93:11:22:33 on en0\n192.168.1.7 dev eth0 lladdr 00:17:f2:11:22:33 REACHABLE\n224.0.0.251 01-00-5e-00-00-fb static\n192.168.2.3 00:17:f2:11:22:33"
	result := parseNeighbors(raw, prefix)
	if len(result) != 3 || result["192.168.1.5"] != "00:1b:21:22:33:44" {
		t.Fatal(result)
	}
	for _, bad := range []string{"0.0.0.0/0", "8.8.8.0/24", "192.168.0.0/16", "127.0.0.1/32", "bad"} {
		if _, e := ValidateSubnet(bad); e == nil {
			t.Fatal("accepted", bad)
		}
	}
	if Vendor("02:00:00:00:00:01") != "Локальный MAC" || Vendor("00:1b:21:11:22:33") != "Intel" {
		t.Fatal("vendor lookup")
	}
}
func TestDiscoveryPersistenceAndIdentity(t *testing.T) {
	db, e := store.Open(filepath.Join(t.TempDir(), "devices.sqlite3"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	d := New(db, slog.New(slog.NewTextHandler(io.Discard, nil)), func(string, any) {})
	prefix := netip.MustParsePrefix("192.168.1.0/24")
	ctx := context.Background()
	o := Observation{IP: "192.168.1.5", MAC: "00:1b:21:11:22:33", Hostname: "test.local", Online: true}
	if e = d.Persist(ctx, prefix, map[string]Observation{o.IP: o}, 1000); e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec("UPDATE devices SET name='My PC',known=1"); e != nil {
		t.Fatal(e)
	}
	o.IP = "192.168.1.6"
	if e = d.Persist(ctx, prefix, map[string]Observation{o.IP: o}, 1010); e != nil {
		t.Fatal(e)
	}
	o.MAC = ""
	if e = d.Persist(ctx, prefix, map[string]Observation{o.IP: o}, 1020); e != nil {
		t.Fatal(e)
	}
	var count int
	var status, name string
	var known bool
	if e = db.QueryRow("SELECT COUNT(*) FROM devices").Scan(&count); e != nil || count != 1 {
		t.Fatal(e, count)
	}
	if e = db.QueryRow("SELECT status,name,known FROM devices").Scan(&status, &name, &known); e != nil || status != "online" || name != "My PC" || !known {
		t.Fatal(e, status, name, known)
	}
	if e = d.Persist(ctx, prefix, map[string]Observation{}, 1030); e != nil {
		t.Fatal(e)
	}
	if e = db.QueryRow("SELECT status FROM devices").Scan(&status); e != nil || status != "offline" {
		t.Fatal(e, status)
	}
	o.MAC = "00:03:93:11:22:33"
	if e = d.Persist(ctx, prefix, map[string]Observation{o.IP: o}, 1040); e != nil {
		t.Fatal(e)
	}
	if e = db.QueryRow("SELECT COUNT(*) FROM devices").Scan(&count); e != nil || count != 2 {
		t.Fatal(e, count)
	}
}
func TestMDNSResponseBounds(t *testing.T) {
	message := dnsmessage.Message{Header: dnsmessage.Header{Response: true}, Answers: []dnsmessage.Resource{{Header: dnsmessage.ResourceHeader{Name: dnsmessage.MustNewName("test.local."), Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET, TTL: 120}, Body: &dnsmessage.AResource{A: [4]byte{192, 168, 1, 8}}}, {Header: dnsmessage.ResourceHeader{Name: dnsmessage.MustNewName("other.local."), Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET, TTL: 120}, Body: &dnsmessage.AResource{A: [4]byte{8, 8, 8, 8}}}}}
	packet, e := message.Pack()
	if e != nil {
		t.Fatal(e)
	}
	observations := map[string]Observation{}
	prefix := netip.MustParsePrefix("192.168.1.0/24")
	applyMDNS(packet, prefix, observations)
	if len(observations) != 1 || !observations["192.168.1.8"].Online {
		t.Fatal(observations)
	}
	applyMDNS([]byte{0, 1, 2}, prefix, observations)
}
