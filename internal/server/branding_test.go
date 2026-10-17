package server

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestBrandingUploadValidationAndAudit(t *testing.T) {
	c := newClient(t)
	c.call("GET", "/api/branding", nil, 401)
	c.setup()
	if c.call("GET", "/api/branding", nil, 200)["logo"] != "" {
		t.Fatal("unexpected default logo")
	}
	send := func(method string, raw []byte, csrf string, status int) map[string]any {
		t.Helper()
		req, _ := http.NewRequest(method, c.srv.URL+"/api/branding", bytes.NewReader(raw))
		req.Header.Set("X-CSRF-Token", csrf)
		response, err := c.http.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		body, _ := io.ReadAll(response.Body)
		if response.StatusCode != status {
			t.Fatalf("%s: %d want %d: %s", method, response.StatusCode, status, body)
		}
		var value map[string]any
		json.Unmarshal(body, &value)
		return value
	}
	img := image.NewNRGBA(image.Rect(0, 0, 12, 12))
	img.Set(0, 0, color.NRGBA{R: 255, A: 255})
	var original bytes.Buffer
	png.Encode(&original, img)
	raw := append(append([]byte{}, original.Bytes()...), []byte("PRIVATE TRAILING DATA")...)
	send("PUT", raw, "", 403)
	saved := send("PUT", raw, c.csrf, 200)["logo"].(string)
	clean, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(saved, "data:image/png;base64,"))
	if err != nil || bytes.Contains(clean, []byte("PRIVATE TRAILING DATA")) {
		t.Fatal("logo not sanitized", err)
	}
	if _, err = png.Decode(bytes.NewReader(clean)); err != nil {
		t.Fatal(err)
	}
	send("PUT", []byte(`<svg onload="alert(1)"></svg>`), c.csrf, 415)
	send("PUT", make([]byte, logoLimit+1), c.csrf, 413)
	var tooLarge bytes.Buffer
	png.Encode(&tooLarge, image.NewNRGBA(image.Rect(0, 0, 513, 1)))
	send("PUT", tooLarge.Bytes(), c.csrf, 400)
	send("PUT", original.Bytes()[:40], c.csrf, 400)
	if got := c.call("GET", "/api/branding", nil, 200)["logo"]; got != saved {
		t.Fatal("invalid upload changed logo")
	}
	if _, err = c.db.Exec(`CREATE TRIGGER branding_audit_failure BEFORE INSERT ON audit WHEN NEW.action='branding' BEGIN SELECT RAISE(ABORT,'audit unavailable'); END`); err != nil {
		t.Fatal(err)
	}
	send("DELETE", nil, c.csrf, 500)
	if c.call("GET", "/api/branding", nil, 200)["logo"] != saved {
		t.Fatal("missing audit did not roll back removal")
	}
	if _, err = c.db.Exec("DROP TRIGGER branding_audit_failure"); err != nil {
		t.Fatal(err)
	}
	var photo bytes.Buffer
	jpeg.Encode(&photo, img, nil)
	send("PUT", photo.Bytes(), c.csrf, 200)
	send("DELETE", nil, c.csrf, 200)
	if c.call("GET", "/api/branding", nil, 200)["logo"] != "" {
		t.Fatal("logo not removed")
	}
	if _, err = c.db.Exec("UPDATE users SET role='user'"); err != nil {
		t.Fatal(err)
	}
	c.call("GET", "/api/branding", nil, 200)
	send("PUT", raw, c.csrf, 403)
	send("DELETE", nil, c.csrf, 403)
}
