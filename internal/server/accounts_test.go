package server

import "testing"

func TestAccountPasswordChangeRevokesSessionsAndShares(t *testing.T) {
	c := newClient(t)
	c.setup()
	if _, err := c.db.Exec("INSERT INTO shares(id,token_hash,owner_id,path,created,expires) VALUES('id','hash',1,'file',1,9999999999)"); err != nil {
		t.Fatal(err)
	}
	c.call("PATCH", "/api/users/1", map[string]any{"password": "new-password-long", "current_password": "wrong"}, 403)
	c.call("PATCH", "/api/users/1", map[string]any{"password": "new-password-long", "current_password": "a-long-test-password"}, 200)
	c.call("GET", "/api/users", nil, 401)
	var sessions, revoked int
	if err := c.db.QueryRow("SELECT COUNT(*) FROM sessions").Scan(&sessions); err != nil || sessions != 0 {
		t.Fatal(sessions, err)
	}
	if err := c.db.QueryRow("SELECT revoked FROM shares WHERE id='id'").Scan(&revoked); err != nil || revoked != 1 {
		t.Fatal(revoked, err)
	}
	c.call("GET", "/api/status", nil, 200)
	c.call("POST", "/api/login", credentials{"testadmin", "a-long-test-password"}, 401)
	c.call("POST", "/api/login", credentials{"testadmin", "new-password-long"}, 200)
}

func TestAccountRolesAndLastAdmin(t *testing.T) {
	c := newClient(t)
	c.setup()
	adminPassword := "a-long-test-password"
	c.call("PATCH", "/api/users/1", map[string]any{"role": "user", "current_password": adminPassword}, 409)
	c.call("PATCH", "/api/users/1", map[string]any{"password": "short", "current_password": adminPassword}, 400)
	c.call("PATCH", "/api/users/1", map[string]any{"role": "owner", "current_password": adminPassword}, 400)
	c.call("PATCH", "/api/users/999", map[string]any{"password": "another-long-password", "current_password": adminPassword}, 404)
	c.call("POST", "/api/users", map[string]any{"username": "second", "password": "another-long-password", "role": "admin"}, 201)
	c.call("PATCH", "/api/users/2", map[string]any{"role": "user", "current_password": adminPassword}, 200)
	c.call("POST", "/api/logout", map[string]any{}, 200)
	c.call("GET", "/api/status", nil, 200)
	c.call("POST", "/api/login", credentials{"second", "another-long-password"}, 200)
	c.call("PATCH", "/api/users/2", map[string]any{"role": "admin", "current_password": "another-long-password"}, 403)
	c.call("PATCH", "/api/users/1", map[string]any{"password": "reset-admin-password", "current_password": "another-long-password"}, 403)
	c.call("PATCH", "/api/users/2", map[string]any{"password": "changed-user-password", "current_password": "another-long-password"}, 200)
	c.call("GET", "/api/resources", nil, 401)
}
