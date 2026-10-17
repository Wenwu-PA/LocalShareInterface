package server

import (
	"context"
	"fmt"
	"lanbridge/internal/monitor"
	"testing"
)

func TestMonitorMutationAuditRollback(t *testing.T) {
	c := newClient(t)
	c.setup()
	original := monitor.Definition{Name: "Original", Kind: "tcp", Target: "127.0.0.1:9", Interval: 20, Timeout: 1, Retries: 2}
	first := int64(c.call("POST", "/api/monitors", original, 200)["id"].(float64))
	paused := original
	paused.Name = "Paused"
	paused.Paused = true
	second := int64(c.call("POST", "/api/monitors", paused, 200)["id"].(float64))
	if _, err := c.db.Exec("INSERT INTO monitor_state(monitor_id,status,checked,latency,failures,message) VALUES(?,'up',1,10,1,'original')", first); err != nil {
		t.Fatal(err)
	}
	if _, err := c.db.Exec("INSERT INTO samples(monitor_id,at,ok,latency) VALUES(?,1,1,10)", first); err != nil {
		t.Fatal(err)
	}
	if _, err := c.db.Exec("INSERT INTO incidents(monitor_id,name,opened,message) VALUES(?,'old',1,'down')", first); err != nil {
		t.Fatal(err)
	}
	if _, err := c.db.Exec(`CREATE TRIGGER monitor_audit_failure BEFORE INSERT ON audit WHEN NEW.action IN ('monitor_saved','monitor_bulk') BEGIN SELECT RAISE(ABORT,'audit unavailable'); END`); err != nil {
		t.Fatal(err)
	}
	check := func() {
		t.Helper()
		items, err := monitor.List(context.Background(), c.db)
		if err != nil || len(items) != 2 {
			t.Fatal(items, err)
		}
		if items[0].Definition != original || items[1].Definition != paused || items[0].State.Status != "up" || items[0].State.Failures != 1 {
			t.Fatal(items)
		}
		var count int
		if err = c.db.QueryRow("SELECT COUNT(*) FROM samples WHERE monitor_id=?", first).Scan(&count); err != nil || count != 1 {
			t.Fatal("samples changed", count, err)
		}
		if err = c.db.QueryRow("SELECT COUNT(*) FROM incidents WHERE monitor_id=?", first).Scan(&count); err != nil || count != 1 {
			t.Fatal("incident detached", count, err)
		}
	}
	changed := original
	changed.Name = "Changed"
	changed.Target = "127.0.0.1:10"
	c.call("PUT", fmt.Sprintf("/api/monitors/%d", first), changed, 500)
	check()
	heartbeat := original
	heartbeat.Kind = "heartbeat"
	c.call("POST", "/api/monitors", heartbeat, 500)
	check()
	for _, action := range []string{"pause", "resume", "delete"} {
		c.call("POST", "/api/monitors/bulk", map[string]any{"ids": []int64{first, second}, "action": action}, 500)
		check()
	}
	if _, err := c.db.Exec("DROP TRIGGER monitor_audit_failure"); err != nil {
		t.Fatal(err)
	}
	c.call("POST", "/api/monitors/bulk", map[string]any{"ids": []int64{first, second}, "action": "pause"}, 200)
	items, err := monitor.List(context.Background(), c.db)
	if err != nil || len(items) != 2 || !items[0].Paused || !items[1].Paused {
		t.Fatal(items, err)
	}
	saved := c.call("POST", "/api/monitors", heartbeat, 200)
	if _, ok := saved["heartbeat_path"].(string); !ok {
		t.Fatal("committed heartbeat token missing")
	}
	var audit int
	if err = c.db.QueryRow("SELECT COUNT(*) FROM audit WHERE action='monitor_bulk'").Scan(&audit); err != nil || audit != 1 {
		t.Fatal(audit, err)
	}
}
