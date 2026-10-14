package config

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	External struct {
		Cloudflared string `yaml:"cloudflared"`
	} `yaml:"external"`
	Server struct {
		Host string `yaml:"host"`
		Port int    `yaml:"port"`
		Cert string `yaml:"cert"`
		Key  string `yaml:"key"`
	} `yaml:"server"`
	Storage struct {
		Data string `yaml:"data"`
		Root string `yaml:"root"`
	} `yaml:"storage"`
	Security struct {
		SessionHours  int `yaml:"session_hours"`
		LoginAttempts int `yaml:"login_attempts"`
	} `yaml:"security"`
	Logs struct {
		Level   string `yaml:"level"`
		MaxMB   int    `yaml:"max_mb"`
		Backups int    `yaml:"backups"`
	} `yaml:"logs"`
	Database struct {
		MaxMB       int `yaml:"max_mb"`
		Backups     int `yaml:"backups"`
		BackupHours int `yaml:"backup_hours"`
	} `yaml:"database"`
}

func Default() Config {
	var c Config
	c.External.Cloudflared = "cloudflared"
	c.Server.Host = "127.0.0.1"
	c.Server.Port = 8766
	c.Storage.Data = "runtime"
	c.Storage.Root = "runtime/files"
	c.Security.SessionHours = 168
	c.Security.LoginAttempts = 8
	c.Logs.Level = "info"
	c.Logs.MaxMB = 5
	c.Logs.Backups = 3
	c.Database.Backups = 3
	c.Database.MaxMB = 512
	c.Database.BackupHours = 24
	return c
}

func Load(path string) (Config, error) {
	c := Default()
	if err := loadEnvironment(filepath.Join(filepath.Dir(path), ".env.go")); err != nil {
		return c, err
	}
	b, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return c, err
	}
	if err == nil {
		dec := yaml.NewDecoder(strings.NewReader(string(b)))
		dec.KnownFields(true)
		if err = dec.Decode(&c); err != nil {
			return c, fmt.Errorf("конфигурация: %w", err)
		}
	}
	for key, dst := range map[string]*string{"LANBRIDGE_GO_CLOUDFLARED": &c.External.Cloudflared, "LANBRIDGE_GO_HOST": &c.Server.Host, "LANBRIDGE_GO_DATA": &c.Storage.Data, "LANBRIDGE_GO_ROOT": &c.Storage.Root, "LANBRIDGE_GO_CERT": &c.Server.Cert, "LANBRIDGE_GO_KEY": &c.Server.Key, "LANBRIDGE_GO_LOG_LEVEL": &c.Logs.Level} {
		if v, ok := os.LookupEnv(key); ok {
			*dst = v
		}
	}
	if value, ok := os.LookupEnv("LANBRIDGE_GO_PORT"); ok {
		c.Server.Port, err = strconv.Atoi(value)
		if err != nil {
			return c, errors.New("LANBRIDGE_GO_PORT должен быть числом")
		}
	}
	if c.Server.Port < 1 || c.Server.Port > 65535 {
		return c, errors.New("порт должен быть от 1 до 65535")
	}
	if c.Storage.Data == "" || c.Storage.Root == "" {
		return c, errors.New("каталоги хранения не могут быть пустыми")
	}
	if (c.Server.Cert == "") != (c.Server.Key == "") {
		return c, errors.New("сертификат и ключ задаются вместе")
	}
	if c.Security.SessionHours < 1 || c.Security.SessionHours > 720 || c.Security.LoginAttempts < 1 || c.Security.LoginAttempts > 100 {
		return c, errors.New("недопустимые параметры сессии или входа")
	}
	if c.Logs.MaxMB < 1 || c.Logs.MaxMB > 100 || c.Logs.Backups < 1 || c.Logs.Backups > 10 {
		return c, errors.New("недопустимые параметры ротации логов")
	}
	if c.Database.Backups < 1 || c.Database.Backups > 10 || c.Database.BackupHours < 1 || c.Database.BackupHours > 168 {
		return c, errors.New("недопустимые параметры резервных копий")
	}
	if c.Database.MaxMB < 16 || c.Database.MaxMB > 16384 {
		return c, errors.New("database.max_mb должен быть от 16 до 16384 МиБ")
	}
	switch c.Logs.Level {
	case "debug", "info", "warn", "error":
	default:
		return c, errors.New("неизвестный уровень журнала")
	}
	base, err := filepath.Abs(filepath.Dir(path))
	if err != nil {
		return c, err
	}
	for _, p := range []*string{&c.Storage.Data, &c.Storage.Root, &c.Server.Cert, &c.Server.Key} {
		if *p != "" && !filepath.IsAbs(*p) {
			*p = filepath.Join(base, *p)
		}
	}
	if strings.TrimSpace(c.External.Cloudflared) == "" {
		return c, errors.New("external.cloudflared не может быть пустым")
	}
	if strings.ContainsAny(c.External.Cloudflared, "/\\") && !filepath.IsAbs(c.External.Cloudflared) {
		c.External.Cloudflared = filepath.Join(base, c.External.Cloudflared)
	}
	return c, nil
}
func loadEnvironment(path string) error {
	file, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 4096), 65536)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		key = strings.TrimSpace(key)
		if !ok || !strings.HasPrefix(key, "LANBRIDGE_") {
			return errors.New(".env.go: ожидается LANBRIDGE_NAME=value")
		}
		value = strings.TrimSpace(value)
		if len(value) > 1 && ((value[0] == '"' && value[len(value)-1] == '"') || (value[0] == '\'' && value[len(value)-1] == '\'')) {
			value = value[1 : len(value)-1]
		}
		if _, exists := os.LookupEnv(key); !exists {
			if err = os.Setenv(key, value); err != nil {
				return errors.New(".env.go: некорректное имя переменной")
			}
		}
	}
	return scanner.Err()
}
func (c Config) SessionDuration() time.Duration {
	return time.Duration(c.Security.SessionHours) * time.Hour
}
