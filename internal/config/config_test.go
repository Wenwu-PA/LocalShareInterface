package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultsAndEnvironment(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	t.Setenv("LANBRIDGE_GO_PORT", "9000")
	c, e := Load(path)
	if e != nil || c.Server.Port != 9000 || !filepath.IsAbs(c.Storage.Data) {
		t.Fatal(c, e)
	}
	t.Setenv("LANBRIDGE_GO_PORT", "bad")
	if _, e = Load(path); e == nil {
		t.Fatal("accepted port")
	}
}
func TestRejectUnknownConfiguration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	_ = os.WriteFile(path, []byte("server:\n  typo: value\n"), 0600)
	if _, e := Load(path); e == nil {
		t.Fatal("accepted unknown option")
	}
}

func TestConnectorExecutablePath(t *testing.T) {
	dir := t.TempDir()
	name := filepath.Join(dir, "config.yaml")
	if e := os.WriteFile(name, []byte("external:\n  cloudflared: ./tools/cloudflared.exe\n"), 0600); e != nil {
		t.Fatal(e)
	}
	c, e := Load(name)
	if e != nil || c.External.Cloudflared != filepath.Join(dir, "tools", "cloudflared.exe") {
		t.Fatal(c.External.Cloudflared, e)
	}
	t.Setenv("LANBRIDGE_GO_CLOUDFLARED", "")
	if _, e = Load(name); e == nil {
		t.Fatal("accepted empty executable")
	}
}

func TestRejectDatabaseSizeLimit(t *testing.T) {
	for _, value := range []string{"0", "15", "16385"} {
		name := filepath.Join(t.TempDir(), "config.yaml")
		os.WriteFile(name, []byte("database:\n  max_mb: "+value+"\n"), 0600)
		if _, e := Load(name); e == nil {
			t.Fatal(value)
		}
	}
}
