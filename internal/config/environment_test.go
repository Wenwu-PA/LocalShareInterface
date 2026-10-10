package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEnvironmentFileQuotesAndPrecedence(t *testing.T) {
	key := "LANBRIDGE_ENV_FILE_TEST"
	old, exists := os.LookupEnv(key)
	os.Unsetenv(key)
	t.Cleanup(func() {
		if exists {
			os.Setenv(key, old)
		} else {
			os.Unsetenv(key)
		}
	})
	path := filepath.Join(t.TempDir(), ".env.go")
	for _, value := range []string{"'value with spaces'", "\"value with spaces\""} {
		os.Unsetenv(key)
		if e := os.WriteFile(path, []byte("# comment\n"+key+"="+value+"\n"), 0600); e != nil {
			t.Fatal(e)
		}
		if e := loadEnvironment(path); e != nil {
			t.Fatal(e)
		}
		if os.Getenv(key) != "value with spaces" {
			t.Fatal("quote parsing failed")
		}
	}
	os.Setenv(key, "process wins")
	if e := loadEnvironment(path); e != nil {
		t.Fatal(e)
	}
	if os.Getenv(key) != "process wins" {
		t.Fatal("process environment overwritten")
	}
}
