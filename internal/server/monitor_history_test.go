package server

import (
	"testing"
	"time"
)

func TestMonitorHistoryGroupFilter(t *testing.T) {
	c := newClient(t)
	c.call("GET", "/api/monitors/history?group=WAN", nil, 401)
	c.setup()
	for i, group := range []string{"WAN", "LAN", "Хранилище"} {
		if _, err := c.db.Exec(`INSERT INTO monitors(id,config) VALUES(?,json_object('group',?))`, i+1, group); err != nil {
			t.Fatal(err)
		}
		if _, err := c.db.Exec(`INSERT INTO aggregates(monitor_id,bucket,resolution,total,up,latency_sum) VALUES(?,?,60,2,1,?)`, i+1, time.Now().Unix(), (i+1)*20); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		query            string
		samples, latency float64
	}{
		{"", 6, 20}, {"&group=WAN", 2, 10}, {"&group=LAN", 2, 20},
		{"&group=%D0%A5%D1%80%D0%B0%D0%BD%D0%B8%D0%BB%D0%B8%D1%89%D0%B5", 2, 30},
		{"&group=missing", 0, 0}, {"&group=WAN&id=2", 0, 0}, {"&group=WAN&id=1", 2, 10},
		{"&group=%27%20OR%201%3D1--", 0, 0},
	} {
		got := c.call("GET", "/api/monitors/history?window=1h"+tc.query, nil, 200)
		if got["samples"] != tc.samples {
			t.Fatalf("%s: %v", tc.query, got)
		}
		points := got["points"].([]any)
		if tc.samples == 0 {
			if len(points) != 0 || got["availability"] != nil {
				t.Fatal(got)
			}
			continue
		}
		if len(points) != 1 || points[0].(map[string]any)["latency"] != tc.latency || got["availability"] != float64(50) {
			t.Fatal(got)
		}
	}
}
