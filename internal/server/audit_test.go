package server

import (
	"fmt"
	"testing"
)

func TestAuditPaginationAndAuthorization(t *testing.T) {
	c := newClient(t)
	c.call("GET", "/api/audit", nil, 401)
	c.setup()
	for i := 0; i < 105; i++ {
		if _, err := c.db.Exec("INSERT INTO audit(created_at,user_id,action,detail) VALUES(1,1,'test_event',?)", fmt.Sprint(i)); err != nil {
			t.Fatal(err)
		}
	}
	first := c.call("GET", "/api/audit?action=test_event&user_id=1&limit=50", nil, 200)
	if len(first["items"].([]any)) != 50 || first["has_more"] != true {
		t.Fatal(first)
	}
	before := int64(first["next_before"].(float64))
	if _, err := c.db.Exec("INSERT INTO audit(created_at,action,detail) VALUES(1,'test_event','new system event')"); err != nil {
		t.Fatal(err)
	}
	second := c.call("GET", fmt.Sprintf("/api/audit?action=test_event&user_id=1&limit=50&before=%d", before), nil, 200)
	for _, item := range second["items"].([]any) {
		if item.(map[string]any)["id"].(float64) >= float64(before) {
			t.Fatal("overlapping pages")
		}
	}
	last := c.call("GET", fmt.Sprintf("/api/audit?action=test_event&user_id=1&before=%d", int64(second["next_before"].(float64))), nil, 200)
	if len(last["items"].([]any)) != 5 || last["has_more"] != false {
		t.Fatal(last)
	}
	for _, q := range []string{"limit=0", "limit=101", "before=-1", "user_id=invalid", "action=%27OR%201=1"} {
		c.call("GET", "/api/audit?"+q, nil, 400)
	}
	c.call("POST", "/api/users", map[string]any{"username": "regular", "password": "another-long-password", "role": "user"}, 201)
	c.call("POST", "/api/logout", map[string]any{}, 200)
	c.call("GET", "/api/status", nil, 200)
	c.call("POST", "/api/login", credentials{"regular", "another-long-password"}, 200)
	c.call("GET", "/api/audit", nil, 403)
}

func TestStorageAuditFailureRollsBackSettings(t *testing.T) {
	c := newClient(t)
	c.setup()
	settings := c.call("GET", "/api/files/storage", nil, 200)["settings"].(map[string]any)
	original := settings["critical_free"]
	settings["critical_free"] = float64(128 << 20)
	if _, err := c.db.Exec("CREATE TRIGGER fail_audit BEFORE INSERT ON audit BEGIN SELECT RAISE(ABORT,'audit unavailable'); END"); err != nil {
		t.Fatal(err)
	}
	c.call("PUT", "/api/files/storage", settings, 500)
	after := c.call("GET", "/api/files/storage", nil, 200)["settings"].(map[string]any)
	if after["critical_free"] != original {
		t.Fatal("settings changed without audit")
	}
	if _, err := c.db.Exec("DROP TRIGGER fail_audit"); err != nil {
		t.Fatal(err)
	}
	c.call("PUT", "/api/files/storage", settings, 200)
	result := c.call("GET", "/api/audit?action=storage_settings", nil, 200)
	if len(result["items"].([]any)) != 1 {
		t.Fatal(result)
	}
}

func TestAccessAuditFailureRollsBackACL(t *testing.T) {
	c := newClient(t)
	c.setup()
	if _, err := c.db.Exec("CREATE TRIGGER fail_audit BEFORE INSERT ON audit BEGIN SELECT RAISE(ABORT,'audit unavailable'); END"); err != nil {
		t.Fatal(err)
	}
	c.call("PUT", "/api/files/access", map[string]any{"path": ".", "access": "admin"}, 500)
	var access string
	if err := c.db.QueryRow("SELECT access FROM file_acl WHERE path='.'").Scan(&access); err != nil || access != "users" {
		t.Fatal(access, err)
	}
	if _, err := c.db.Exec("DROP TRIGGER fail_audit"); err != nil {
		t.Fatal(err)
	}
	c.call("PUT", "/api/files/access", map[string]any{"path": ".", "access": "admin"}, 200)
	result := c.call("GET", "/api/audit?action=file_access", nil, 200)
	if len(result["items"].([]any)) != 1 {
		t.Fatal(result)
	}
}
