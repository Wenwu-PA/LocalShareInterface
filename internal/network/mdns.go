package network

import (
	"context"
	"golang.org/x/net/dns/dnsmessage"
	"net"
	"net/netip"
	"strings"
	"time"
)

// An ephemeral source port requests legacy unicast replies (RFC 6762 section 6.7).
func discoverMDNS(ctx context.Context, prefix netip.Prefix, observations map[string]Observation) {
	source := ""
	for _, iface := range Interfaces() {
		ip, _ := netip.ParseAddr(iface.IP)
		if prefix.Contains(ip) {
			source = iface.IP
			break
		}
	}
	if source == "" {
		return
	}
	conn, e := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP(source)})
	if e != nil {
		return
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	conn.SetDeadline(time.Now().Add(2 * time.Second))
	target := &net.UDPAddr{IP: net.IPv4(224, 0, 0, 251), Port: 5353}
	questions := []dnsmessage.Question{{Name: dnsmessage.MustNewName("_services._dns-sd._udp.local."), Type: dnsmessage.TypePTR, Class: dnsmessage.ClassINET}}
	for ip := range observations {
		parts := strings.Split(ip, ".")
		if len(parts) == 4 {
			questions = append(questions, dnsmessage.Question{Name: dnsmessage.MustNewName(parts[3] + "." + parts[2] + "." + parts[1] + "." + parts[0] + ".in-addr.arpa."), Type: dnsmessage.TypePTR, Class: dnsmessage.ClassINET})
		}
	}
	for len(questions) > 0 {
		n := min(16, len(questions))
		packet, e := (&dnsmessage.Message{Questions: questions[:n]}).Pack()
		questions = questions[n:]
		if e == nil {
			conn.WriteToUDP(packet, target)
		}
	}
	queried := map[string]bool{}
	buf := make([]byte, 4096)
	for count := 0; count < 512; count++ {
		n, peer, e := conn.ReadFromUDP(buf)
		if e != nil {
			return
		}
		ip, ok := netip.AddrFromSlice(peer.IP)
		if !ok || !prefix.Contains(ip.Unmap()) || peer.Port != 5353 {
			continue
		}
		services := applyMDNS(buf[:n], prefix, observations)
		for _, service := range services {
			if len(queried) >= 32 || queried[service] {
				continue
			}
			queried[service] = true
			name, e := dnsmessage.NewName(service)
			if e != nil {
				continue
			}
			packet, e := (&dnsmessage.Message{Questions: []dnsmessage.Question{{Name: name, Type: dnsmessage.TypePTR, Class: dnsmessage.ClassINET}}}).Pack()
			if e == nil {
				conn.WriteToUDP(packet, target)
			}
		}
	}
}
func applyMDNS(packet []byte, prefix netip.Prefix, observations map[string]Observation) []string {
	var message dnsmessage.Message
	if e := message.Unpack(packet); e != nil || !message.Header.Response {
		return nil
	}
	services := []string{}
	for _, resource := range append(message.Answers, message.Additionals...) {
		if resource.Header.TTL == 0 {
			continue
		}
		switch body := resource.Body.(type) {
		case *dnsmessage.AResource:
			ip := netip.AddrFrom4(body.A)
			if !prefix.Contains(ip) {
				continue
			}
			name := strings.TrimSuffix(resource.Header.Name.String(), ".")
			if !strings.HasSuffix(strings.ToLower(name), ".local") {
				continue
			}
			o := observations[ip.String()]
			o.IP = ip.String()
			o.Hostname = name
			o.Online = true
			observations[o.IP] = o
		case *dnsmessage.PTRResource:
			owner := resource.Header.Name.String()
			name := body.PTR.String()
			if owner == "_services._dns-sd._udp.local." && strings.HasSuffix(name, ".local.") {
				services = append(services, name)
			}
			if strings.HasSuffix(owner, ".in-addr.arpa.") {
				parts := strings.Split(strings.TrimSuffix(owner, ".in-addr.arpa."), ".")
				if len(parts) != 4 {
					continue
				}
				ip, e := netip.ParseAddr(parts[3] + "." + parts[2] + "." + parts[1] + "." + parts[0])
				if e != nil || !prefix.Contains(ip) {
					continue
				}
				o := observations[ip.String()]
				o.IP = ip.String()
				o.Hostname = strings.TrimSuffix(name, ".")
				o.Online = true
				observations[o.IP] = o
			}
		}
	}
	return services
}
