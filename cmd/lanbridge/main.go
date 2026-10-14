package main

import (
	"context"
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
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "LANBridge:", err)
		os.Exit(1)
	}
}
func run() error {
	// Keep retained Go heap small on lightweight hosts; an operator can override it.
	if os.Getenv("GOMEMLIMIT") == "" {
		debug.SetMemoryLimit(40 << 20)
	}
	path := flag.String("config", "config.go.yaml", "Путь к YAML новой Go-версии")
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
	db, err := store.OpenBounded(filepath.Join(cfg.Storage.Data, "lanbridge-go.sqlite3"), int64(cfg.Database.MaxMB)<<20)
	if err != nil {
		return err
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
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err = app.Monitors.Start(ctx); err != nil {
		return err
	}
	app.Notifications.Start(ctx)
	app.Discovery.Start(ctx)
	app.Files.Start(ctx)
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
	go func() { result <- srv.ListenAndServeTLS(cert, key) }()
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
