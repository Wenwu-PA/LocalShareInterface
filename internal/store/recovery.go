package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"time"
)

var ErrCorrupt = errors.New("SQLite повреждена")

func IsCorrupt(err error) bool {
	var coded interface{ Code() int }
	return errors.Is(err, ErrCorrupt) || (errors.As(err, &coded) && (coded.Code()&255 == 11 || coded.Code()&255 == 26))
}

type Recovery struct {
	Backup  string `json:"backup"`
	Archive string `json:"archive"`
	Ready   bool   `json:"ready"`
}

func RecoveryStatus(path string) (*Recovery, error) {
	b, err := os.ReadFile(path + ".recovery.json")
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var r Recovery
	if err = json.Unmarshal(b, &r); err != nil {
		return nil, fmt.Errorf("нечитаемый журнал восстановления: %w", err)
	}
	return &r, nil
}

// A standalone backup is checked read-only, before migrations or any writes.
func validateBackup(path string) error {
	dsn, _, err := databaseDSN(path, 0)
	if err != nil {
		return err
	}
	u, err := url.Parse(dsn)
	if err != nil {
		return err
	}
	q := url.Values{"mode": {"ro"}, "immutable": {"1"}}
	u.RawQuery = q.Encode()
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return err
	}
	defer db.Close()
	var result string
	if err = db.QueryRow("PRAGMA integrity_check").Scan(&result); err != nil {
		return err
	}
	if result != "ok" {
		return ErrCorrupt
	}
	var version, users int
	if err = db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		return err
	}
	if version < 1 || version > 6 {
		return fmt.Errorf("неподдерживаемая версия копии %d", version)
	}
	if err = db.QueryRow("SELECT COUNT(*) FROM users WHERE role='admin'").Scan(&users); err != nil {
		return err
	}
	if users == 0 {
		return errors.New("копия не содержит администратора")
	}
	rows, err := db.Query("PRAGMA foreign_key_check")
	if err != nil {
		return err
	}
	defer rows.Close()
	if rows.Next() {
		return errors.New("нарушены внешние ключи копии")
	}
	return rows.Err()
}

func copyBackup(source, target string) error {
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	_, err = io.Copy(out, in)
	if err == nil {
		err = out.Sync()
	}
	return errors.Join(err, out.Close())
}

// RecoverLatest runs only after confirmed corruption, with no open DB handles.
// It never starts the application: a durable marker requires operator review.
func RecoverLatest(path, backups string, maxBytes int64) (*Recovery, error) {
	if pending, err := RecoveryStatus(path); err != nil || pending != nil {
		return nil, errors.New("восстановление уже начато; проверьте журнал и архив")
	}
	files, err := filepath.Glob(filepath.Join(backups, "*.sqlite3"))
	if err != nil {
		return nil, err
	}
	sort.Sort(sort.Reverse(sort.StringSlice(files)))
	var r *Recovery
	var stage string
	for _, source := range files {
		info, e := os.Lstat(source)
		if e != nil || !info.Mode().IsRegular() || (maxBytes > 0 && info.Size() > maxBytes) {
			continue
		}
		archive, e := os.MkdirTemp(filepath.Dir(path), "database-recovery-")
		if e != nil {
			return nil, e
		}
		candidate := filepath.Join(archive, "candidate.sqlite3")
		savedBackup := filepath.Join(archive, "backup.sqlite3")
		e = copyBackup(source, savedBackup)
		if e == nil {
			e = copyBackup(savedBackup, candidate)
		}
		if e == nil {
			e = validateBackup(candidate)
		}
		if e == nil {
			e = prepareRecovered(candidate, maxBytes)
		}
		if e != nil {
			_ = os.RemoveAll(archive)
			continue
		}
		r = &Recovery{Backup: source, Archive: archive}
		stage = candidate
		break
	}
	if r == nil {
		return nil, errors.New("нет пригодной резервной копии; исходная БД сохранена")
	}
	marker := path + ".recovery.json"
	if err = writeRecovery(marker, r); err != nil {
		return nil, err
	}
	// Marker exists before the first rename. An interrupted exchange blocks every
	// normal startup, even when the main database is temporarily absent.
	for _, suffix := range []string{"", "-wal", "-shm"} {
		from := path + suffix
		if _, err = os.Lstat(from); os.IsNotExist(err) {
			continue
		} else if err != nil {
			return nil, err
		}
		if err = os.Rename(from, filepath.Join(r.Archive, "original.sqlite3"+suffix)); err != nil {
			return nil, err
		}
	}
	if err = os.Rename(stage, path); err != nil {
		return nil, err
	}
	r.Ready = true
	if err = writeRecovery(marker+".ready", r); err != nil {
		return nil, err
	}
	if err = os.Rename(marker+".ready", marker); err != nil {
		return nil, err
	}
	return r, nil
}

func writeRecovery(path string, r *Recovery) error {
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	_, err = f.Write(b)
	if err == nil {
		err = f.Sync()
	}
	return errors.Join(err, f.Close())
}

func prepareRecovered(path string, maxBytes int64) error {
	db, err := OpenBounded(path, maxBytes)
	if err != nil {
		return err
	}
	defer db.Close()
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// A historical filesystem journal must never replay against today's files.
	// Original metadata remains in the untouched backup; hidden files stay on disk.
	for _, query := range []string{
		"DELETE FROM sessions", "UPDATE shares SET revoked=1",
		"DELETE FROM file_operations", "DELETE FROM uploads", "DELETE FROM trash",
		"DELETE FROM file_index", "DELETE FROM file_acl", "INSERT INTO file_acl VALUES('.','admin')",
		"UPDATE notification_channels SET config=json_set(config,'$.enabled',json('false'))",
		"UPDATE notification_queue SET status='dropped',last_error='Отменено после восстановления БД' WHERE status='pending'",
		"UPDATE external_heartbeat SET enabled=0",
	} {
		if _, err = tx.Exec(query); err != nil {
			return err
		}
	}
	if _, err = tx.Exec("INSERT INTO audit(created_at,action,detail) VALUES(?,'database_recovered','Sessions and shares revoked; file ACL reset; stale jobs removed')", time.Now().Unix()); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	if _, err = db.Exec("PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
		return err
	}
	if err = db.Close(); err != nil {
		return err
	}
	return validateBackup(path)
}
