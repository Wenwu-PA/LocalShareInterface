package store

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

const schema = `
CREATE TABLE users(id INTEGER PRIMARY KEY, username TEXT NOT NULL UNIQUE COLLATE NOCASE, password_hash TEXT NOT NULL, role TEXT NOT NULL CHECK(role IN ('admin','user')), created_at INTEGER NOT NULL);
CREATE TABLE sessions(token_hash TEXT PRIMARY KEY, user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE, csrf_hash TEXT NOT NULL, expires_at INTEGER NOT NULL);
CREATE INDEX session_expiry ON sessions(expires_at);
CREATE TABLE login_attempts(ip TEXT PRIMARY KEY, failures INTEGER NOT NULL, window_start INTEGER NOT NULL);
CREATE TABLE audit(id INTEGER PRIMARY KEY, created_at INTEGER NOT NULL, user_id INTEGER, action TEXT NOT NULL, detail TEXT NOT NULL);
CREATE TABLE settings(key TEXT PRIMARY KEY,value TEXT NOT NULL);
PRAGMA user_version=1;`

func Open(path string) (*sql.DB, error) {
	return OpenBounded(path, 0)
}

func OpenBounded(path string, maxBytes int64) (*sql.DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	dsn, pages, err := databaseDSN(path, maxBytes)
	if err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	fail := func(e error) (*sql.DB, error) { db.Close(); return nil, e }
	if err = checkPageLimit(db, pages); err != nil {
		return fail(err)
	}
	var integrity string
	if err = db.QueryRow("PRAGMA quick_check").Scan(&integrity); err != nil {
		return fail(fmt.Errorf("проверка SQLite: %w", err))
	}
	if integrity != "ok" {
		return fail(errors.New("SQLite повреждена; сохраните файлы БД и восстановите проверенную резервную копию"))
	}
	var version int
	if err = db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		return fail(err)
	}
	if version > 6 {
		return fail(fmt.Errorf("версия БД %d новее поддерживаемой 6", version))
	}
	if version == 0 {
		tx, e := db.Begin()
		if e != nil {
			return fail(e)
		}
		if _, e = tx.Exec(schema); e != nil {
			tx.Rollback()
			return fail(fmt.Errorf("миграция отменена: %w", e))
		}
		if e = tx.Commit(); e != nil {
			return fail(e)
		}
	}
	if version < 2 {
		tx, e := db.Begin()
		if e != nil {
			return fail(e)
		}
		if _, e = tx.Exec(monitorSchema); e != nil {
			tx.Rollback()
			return fail(fmt.Errorf("миграция мониторинга отменена: %w", e))
		}
		if e = tx.Commit(); e != nil {
			return fail(e)
		}
	}
	if version < 3 {
		tx, e := db.Begin()
		if e != nil {
			return fail(e)
		}
		if _, e = tx.Exec(notificationSchema); e != nil {
			tx.Rollback()
			return fail(fmt.Errorf("миграция уведомлений отменена: %w", e))
		}
		if e = tx.Commit(); e != nil {
			return fail(e)
		}
	}
	if version < 4 {
		tx, e := db.Begin()
		if e != nil {
			return fail(e)
		}
		if _, e = tx.Exec(deviceSchema); e != nil {
			tx.Rollback()
			return fail(fmt.Errorf("device migration failed: %w", e))
		}
		if e = tx.Commit(); e != nil {
			return fail(e)
		}
	}
	if version < 5 {
		tx, e := db.Begin()
		if e != nil {
			return fail(e)
		}
		if _, e = tx.Exec(fileSchema); e != nil {
			tx.Rollback()
			return fail(fmt.Errorf("file migration failed: %w", e))
		}
		if e = tx.Commit(); e != nil {
			return fail(e)
		}
	}
	if version < 6 {
		tx, e := db.Begin()
		if e != nil {
			return fail(e)
		}
		if _, e = tx.Exec(shareSchema); e != nil {
			tx.Rollback()
			return fail(fmt.Errorf("share migration failed: %w", e))
		}
		if e = tx.Commit(); e != nil {
			return fail(e)
		}
	}
	return db, nil
}

func Backup(db *sql.DB, directory string, count int) error {
	if err := os.MkdirAll(directory, 0700); err != nil {
		return err
	}
	target := filepath.Join(directory, time.Now().UTC().Format("20060102T150405.000000000")+".sqlite3")
	if _, err := db.Exec("VACUUM INTO '" + strings.ReplaceAll(target, "'", "''") + "'"); err != nil {
		return err
	}
	files, err := filepath.Glob(filepath.Join(directory, "*.sqlite3"))
	if err != nil {
		return err
	}
	if len(files) > count {
		for _, f := range files[:len(files)-count] {
			if err = os.Remove(f); err != nil {
				return err
			}
		}
	}
	return nil
}
