package server

import (
	"net"
	"net/http"
	"sort"
	"strconv"
)

func (s *Server) connection(w http.ResponseWriter, r *http.Request) {
	host := s.Config.Server.Host
	ip := net.ParseIP(host)
	loopback := host == "localhost" || ip != nil && ip.IsLoopback()
	addresses := []string{}
	if !loopback {
		if ip != nil && !ip.IsUnspecified() {
			addresses = append(addresses, "https://"+net.JoinHostPort(host, strconv.Itoa(s.Config.Server.Port)))
		} else {
			entries, e := net.InterfaceAddrs()
			if e != nil {
				problem(w, 503, "network", "Не удалось получить адреса интерфейсов")
				return
			}
			for _, entry := range entries {
				v, _, e := net.ParseCIDR(entry.String())
				if e == nil && v.To4() != nil && v.IsPrivate() {
					addresses = append(addresses, "https://"+net.JoinHostPort(v.String(), strconv.Itoa(s.Config.Server.Port)))
				}
			}
		}
	}
	sort.Strings(addresses)
	jsonResponse(w, 200, map[string]any{"addresses": addresses, "loopback_only": loopback, "public_url": s.External.Status().URL})
}
