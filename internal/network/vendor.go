package network

import (
	"bytes"
	"compress/gzip"
	_ "embed"
	"encoding/json"
	"net"
	"strings"
	"sync"
)

// Compact table inherited from the Python app; see docs/go-devices.md.
//
//go:embed oui.json.gz
var compressedVendors []byte
var vendorOnce sync.Once
var vendors map[string]string

func Vendor(mac string) string {
	parsed, e := net.ParseMAC(mac)
	if e != nil || len(parsed) != 6 {
		return ""
	}
	if parsed[0]&2 != 0 {
		return "Локальный MAC"
	}
	vendorOnce.Do(func() {
		r, e := gzip.NewReader(bytes.NewReader(compressedVendors))
		if e != nil {
			return
		}
		defer r.Close()
		_ = json.NewDecoder(r).Decode(&vendors)
	})
	prefix := strings.ToUpper(strings.ReplaceAll(parsed.String()[:8], ":", ""))
	return vendors[prefix]
}
