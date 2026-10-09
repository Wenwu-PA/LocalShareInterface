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
