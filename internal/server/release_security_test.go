package server

import (
	"testing"
)

func TestReleaseEncodedTraversalAndFolderACL(t *testing.T) {
	c := newClient(t)
	c.setup()
	for _, path := range []string{"%2e%2e%2foutside", "%2e%2e%5coutside", "%2fetc%2fpasswd", ".lanbridge%2fuploads"} {
		c.call("GET", "/api/files/download?path="+path, nil, 403)
	}
	c.call("POST", "/api/files/mkdir", map[string]any{"path": "private"}, 201)
	if err := c.server.Files.Root.WriteFile("private/secret.txt", []byte("private content"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := c.db.Exec("INSERT INTO file_acl(path,access) VALUES('private','admin')"); err != nil {
		t.Fatal(err)
	}
	c.call("POST", "/api/users", map[string]any{"username": "releaseuser", "password": "a-long-test-password", "role": "user"}, 201)
	c.call("POST", "/api/logout", map[string]any{}, 200)
	c.call("GET", "/api/status", nil, 200)
	c.call("POST", "/api/login", credentials{"releaseuser", "a-long-test-password"}, 200)
	c.call("GET", "/api/files/download?path=private/secret.txt", nil, 403)
	c.call("POST", "/api/uploads", map[string]any{"path": "private/new.txt", "size": 1, "conflict": "skip"}, 403)
	ticket := c.call("POST", "/api/files/zip-ticket", map[string]any{"paths": []string{"private"}}, 201)
	c.call("GET", ticket["url"].(string), nil, 403)
	c.call("POST", "/api/shares", map[string]any{"path": "private/secret.txt", "ttl_seconds": 3600}, 403)
}
