package main

import (
	"context"
	"crypto/tls"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	_ "time/tzdata"

	"lanbridge/internal/config"
	"lanbridge/internal/monitor"
	"lanbridge/internal/server"
	"lanbridge/internal/store"
	"lanbridge/web"
)

func main() {
	if err := startApplication(); err != nil {
		fmt.Fprintln(os.Stderr, "LANBridge:", err)
		os.Exit(1)
	}
}
func run(parent context.Context, ready func()) (resultErr error) {
	// Keep retained Go heap small on lightweight hosts; an operator can override it.
	if os.Getenv("GOMEMLIMIT") == "" {
		debug.SetMemoryLimit(40 << 20)
	}
	path := flag.String("config", "config.go.yaml", "Путь к YAML новой Go-версии")
	acceptRecovery := flag.Bool("accept-database-recovery", false, "Подтвердить восстановление БД и запустить только на 127.0.0.1 для проверки прав")
	version := flag.Bool("version", false, "Версия")
	flag.Parse()
	if *version {
		fmt.Println("0.2.0-dev.1")
		return nil
	}
	if env := os.Getenv("LANBRIDGE_GO_CONFIG"); env != "" && *path == "config.go.yaml" {
		*path = env
	}
	cfg, err := config.Load(*path)
	if err != nil {
		return err
	}
	if err = os.MkdirAll(cfg.Storage.Root, 0700); err != nil {
		return err
	}
	logFile, err := server.NewLog(filepath.Join(cfg.Storage.Data, "logs", "lanbridge.log"), cfg.Logs.MaxMB, cfg.Logs.Backups)
	if err != nil {
		return err
	}
	defer logFile.Close()
	var level slog.Level
	_ = level.UnmarshalText([]byte(strings.ToUpper(cfg.Logs.Level)))
	logger := slog.New(slog.NewJSONHandler(io.MultiWriter(os.Stderr, logFile), &slog.HandlerOptions{Level: level}))
	defer func() {
		if resultErr != nil {
			logger.Error("application stopped with error", "error", resultErr)
		}
	}()
	dbPath := filepath.Join(cfg.Storage.Data, "lanbridge-go.sqlite3")
	pending, err := store.RecoveryStatus(dbPath)
	if err != nil {
		return err
	}
	if pending != nil {
		if !pending.Ready {
			return fmt.Errorf("восстановление прервано; запуск заблокирован. Проверьте %s и журнал %s.recovery.json", pending.Archive, dbPath)
		}
		if !*acceptRecovery {
			return fmt.Errorf("БД восстановлена из %s; архив: %s. Проверьте права/пароли из старой копии. Для локального запуска добавьте -accept-database-recovery", pending.Backup, pending.Archive)
		}
		if _, err = os.Stat(dbPath); err != nil {
			return fmt.Errorf("восстановленная БД отсутствует: %w", err)
		}
	} else if *acceptRecovery {
		return fmt.Errorf("нет восстановления, ожидающего подтверждения")
	}
	db, err := store.OpenBounded(dbPath, int64(cfg.Database.MaxMB)<<20)
	if err != nil {
		if !store.IsCorrupt(err) || pending != nil {
			return err
		}
		recovery, recoveryErr := store.RecoverLatest(dbPath, filepath.Join(cfg.Storage.Data, "backups"), int64(cfg.Database.MaxMB)<<20)
		if recoveryErr != nil {
			return fmt.Errorf("%w; восстановление: %v", err, recoveryErr)
		}
		return fmt.Errorf("повреждённая БД сохранена в %s; восстановлена копия %s. Сессии и ссылки отозваны, доступ к файлам ограничен администратором. Проверьте прежние пароли/роли; для локального запуска добавьте -accept-database-recovery", recovery.Archive, recovery.Backup)
	}
	if pending != nil {
		if err = os.Remove(dbPath + ".recovery.json"); err != nil {
			db.Close()
			return err
		}
		cfg.Server.Host = "127.0.0.1"
		logger.Warn("database recovery accepted; listening on loopback only for access review", "backup", pending.Backup, "archive", pending.Archive)
	}
	defer db.Close()
	if err = store.Backup(db, filepath.Join(cfg.Storage.Data, "backups"), cfg.Database.Backups); err != nil {
		return fmt.Errorf("начальная резервная копия: %w", err)
	}
	cert, key := cfg.Server.Cert, cfg.Server.Key
	if cert == "" {
		cert, key, err = server.EnsureTLS(filepath.Join(cfg.Storage.Data, "tls"))
		if err != nil {
			return err
		}
	}
	app := server.New(db, cfg, logger)
	defer app.External.Close()
	if err = app.InitFiles(); err != nil {
		return err
	}
	defer app.Files.Close()
	srv := &http.Server{Addr: net.JoinHostPort(cfg.Server.Host, strconv.Itoa(cfg.Server.Port)), Handler: app.Handler(web.Assets()), ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 * 1024}
	ctx, cancel := signal.NotifyContext(parent, os.Interrupt, syscall.SIGTERM)
	defer cancel()
	certificate, err := tls.LoadX509KeyPair(cert, key)
	if err != nil {
		return err
	}
	srv.TLSConfig = &tls.Config{Certificates: []tls.Certificate{certificate}, MinVersion: tls.VersionTLS12}
	listener, err := net.Listen("tcp", srv.Addr)
	if err != nil {
		return err
	}
	defer listener.Close()
	if err = app.Monitors.Start(ctx); err != nil {
		return err
	}
	app.Notifications.Start(ctx)
	app.Discovery.Start(ctx)
	app.Files.Start(ctx)
	app.CheckResources(ctx)
	var tasks sync.WaitGroup
	tasks.Add(1)
	go func() {
		defer tasks.Done()
		defer func() {
			if recover() != nil {
				logger.Error("maintenance stopped unexpectedly")
				cancel()
			}
		}()
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		nextBackup := time.Now().Add(time.Duration(cfg.Database.BackupHours) * time.Hour)
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				app.CheckResources(ctx)
				if e := monitor.Compact(ctx, db, time.Now()); e != nil && ctx.Err() == nil {
					logger.Error("monitor compaction failed", "error", e)
				}
				for _, query := range []string{"DELETE FROM sessions WHERE expires_at<unixepoch()", "DELETE FROM login_attempts WHERE window_start<unixepoch()-900", "DELETE FROM audit WHERE created_at<unixepoch()-31536000", "PRAGMA wal_checkpoint(PASSIVE)"} {
					if _, e := db.ExecContext(ctx, query); e != nil && ctx.Err() == nil {
						logger.Error("maintenance failed", "error", e)
					}
				}
				if time.Now().After(nextBackup) {
					if e := store.Backup(db, filepath.Join(cfg.Storage.Data, "backups"), cfg.Database.Backups); e != nil {
						logger.Error("backup failed", "error", e)
					}
					nextBackup = time.Now().Add(time.Duration(cfg.Database.BackupHours) * time.Hour)
				}
			}
		}
	}()
	result := make(chan error, 1)
	go func() { result <- srv.ServeTLS(listener, "", "") }()
	ready()
	logger.Info("starting HTTPS server", "address", "https://"+srv.Addr, "database", filepath.Join(cfg.Storage.Data, "lanbridge-go.sqlite3"))
	select {
	case err = <-result:
		cancel()
	case <-ctx.Done():
	}
	app.CloseStreams()
	if e := app.External.Close(); e != nil {
		logger.Error("external gateway stopped", "error", e)
	}
	shutdown, stop := context.WithTimeout(context.Background(), 10*time.Second)
	defer stop()
	if e := srv.Shutdown(shutdown); e != nil {
		logger.Error("graceful shutdown timeout", "error", e)
		_ = srv.Close()
	}
	cancel()
	tasks.Wait()
	app.Monitors.Close()
	app.Notifications.Close()
	app.Discovery.Close()
	if _, e := db.Exec("PRAGMA wal_checkpoint(TRUNCATE)"); e != nil {
		return e
	}
	if err != nil && err != http.ErrServerClosed {
		return err
	}
	logger.Info("server stopped cleanly")
	return nil
}
