package server

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"testing"
)

func TestFileAPIUploadRangeZipAndTrash(t *testing.T) {
	c := newClient(t)
	c.setup()
	data := []byte("streamed file content")
	hash := sha256.Sum256(data)
	u := c.call("POST", "/api/uploads", map[string]any{"path": "folder/data.txt", "size": len(data), "sha256": hex.EncodeToString(hash[:]), "conflict": "skip"}, 201)
	id := u["id"].(string)
	r, _ := http.NewRequest("PATCH", c.srv.URL+"/api/uploads/"+id, bytes.NewReader(data))
	r.Header.Set("X-CSRF-Token", c.csrf)
	r.Header.Set("Upload-Offset", "0")
	resp, e := c.http.Do(r)
	if e != nil {
		t.Fatal(e)
	}
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatal(resp.StatusCode, string(raw))
	}
	c.call("POST", "/api/uploads/"+id+"/finish", map[string]any{}, 200)
	r, _ = http.NewRequest("GET", c.srv.URL+"/api/files/download?path=folder/data.txt", nil)
	r.Header.Set("Range", "bytes=0-7")
	resp, e = c.http.Do(r)
	if e != nil {
		t.Fatal(e)
	}
	raw, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 206 || string(raw) != "streamed" {
		t.Fatal(resp.StatusCode, string(raw))
	}
	var transferred int64
	var transferState string
	if e = c.db.QueryRow("SELECT size,status FROM transfers WHERE direction='download'").Scan(&transferred, &transferState); e != nil || transferred != 8 || transferState != "partial" {
		t.Fatal(transferred, transferState, e)
	}
	resp, e = c.http.Get(c.srv.URL + "/api/files/download?path=folder/data.txt")
	if e != nil {
		t.Fatal(e)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if e = c.db.QueryRow("SELECT size,status FROM transfers WHERE direction='download' ORDER BY id DESC LIMIT 1").Scan(&transferred, &transferState); e != nil || transferred != int64(len(data)) || transferState != "complete" {
		t.Fatal(transferred, transferState, e)
	}
	ticket := c.call("POST", "/api/files/zip-ticket", map[string]any{"paths": []string{"folder"}}, 201)
	resp, e = c.http.Get(c.srv.URL + ticket["url"].(string))
	if e != nil {
		t.Fatal(e)
	}
	raw, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	archive, e := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if e != nil || len(archive.File) != 2 {
		t.Fatal(e, string(raw))
	}
	c.call("GET", ticket["url"].(string), nil, 404)
	deleted := c.call("POST", "/api/files/operation", map[string]any{"action": "trash", "source": "folder"}, 200)
	c.call("GET", "/api/files/download?path=folder/data.txt", nil, 404)
	c.call("POST", "/api/files/restore", map[string]any{"id": deleted["result"], "conflict": "skip"}, 200)
	c.call("GET", "/api/files/download?path=../outside", nil, 403)
}
