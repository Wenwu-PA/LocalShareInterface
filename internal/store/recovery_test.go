package store

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestRecoveryPreservesOriginalAndInvalidatesStaleState(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "live.sqlite3")
	backups := filepath.Join(dir, "backups")
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{
		"INSERT INTO users VALUES(1,'admin','hash','admin',1)",
		"INSERT INTO sessions VALUES('token',1,'csrf',9999999999)",
		"INSERT INTO shares(id,token_hash,owner_id,path,created,expires) VALUES('id','token',1,'file',1,9999999999)",
		"INSERT INTO file_operations VALUES('job','source','target',1,'move',1,'{}')",
		"INSERT INTO uploads(id,path,owner_id,size,sha256,conflict,created,updated) VALUES('id','file',1,1,'hash','replace',1,1)",
		"INSERT INTO trash(id,path,owner_id,size,created,state) VALUES('id','file',1,1,1,'purging')",
		`INSERT INTO notification_channels(config) VALUES('{"enabled":true}')`,
		"INSERT INTO notification_queue(channel_id,event_id,kind,payload,next_at,created_at) VALUES(1,1,'down','{}',1,1)",
	} {
		if _, err = db.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	if err = Backup(db, backups, 3); err != nil {
		t.Fatal(err)
	}
	db.Close()
	original := []byte("corrupted original SQLite database")
	if err = os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}
	_, err = OpenBounded(path, 16<<20)
	if !IsCorrupt(err) {
		t.Fatal("not classified", err)
	}
	// A damaged newer copy must not hide the previous valid backup.
	if err = os.WriteFile(filepath.Join(backups, "99999999.sqlite3"), []byte("invalid"), 0600); err != nil {
		t.Fatal(err)
	}
	r, err := RecoverLatest(path, backups, 16<<20)
	if err != nil {
		t.Fatal(err)
	}
	if !r.Ready {
		t.Fatal(r)
	}
	saved, err := os.ReadFile(filepath.Join(r.Archive, "original.sqlite3"))
	if err != nil || !bytes.Equal(saved, original) {
		t.Fatal(err, string(saved))
	}
	if err = validateBackup(filepath.Join(r.Archive, "backup.sqlite3")); err != nil {
		t.Fatal(err)
	}
	pending, err := RecoveryStatus(path)
	if err != nil || pending == nil || !pending.Ready {
		t.Fatal(pending, err)
	}
	if _, err = RecoverLatest(path, backups, 16<<20); err == nil {
		t.Fatal("repeated recovery accepted")
	}
	db, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, table := range []string{"sessions", "file_operations", "uploads", "trash", "file_index"} {
		var count int
		if err = db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&count); err != nil || count != 0 {
			t.Fatal(table, count, err)
		}
	}
	var revoked int
	if err = db.QueryRow("SELECT revoked FROM shares").Scan(&revoked); err != nil || revoked != 1 {
		t.Fatal(revoked, err)
	}
	var access string
	if err = db.QueryRow("SELECT access FROM file_acl WHERE path='.'").Scan(&access); err != nil || access != "admin" {
		t.Fatal(access, err)
	}
	var enabled bool
	if err = db.QueryRow("SELECT json_extract(config,'$.enabled') FROM notification_channels").Scan(&enabled); err != nil || enabled {
		t.Fatal(enabled, err)
	}
}

func TestRecoveryRejectsMissingInvalidAndFutureBackups(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "broken.sqlite3")
	backups := filepath.Join(dir, "backups")
	original := []byte("broken")
	if err := os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(backups, 0700); err != nil {
		t.Fatal(err)
	}
	future := filepath.Join(backups, "future.sqlite3")
	db, err := Open(future)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("PRAGMA user_version=99"); err != nil {
		t.Fatal(err)
	}
	db.Close()
	if _, err = RecoverLatest(path, backups, 16<<20); err == nil {
		t.Fatal("accepted invalid backup")
	}
	got, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(got, original) {
		t.Fatal(err)
	}
	if r, err := RecoveryStatus(path); err != nil || r != nil {
		t.Fatal(r, err)
	}
	// An interrupted exchange must be detectable even when the main DB is absent.
	if err = writeRecovery(path+".recovery.json", &Recovery{Archive: dir}); err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if r, err := RecoveryStatus(path); err != nil || r == nil || r.Ready {
		t.Fatal(r, err)
	}
}

func TestCorruptionClassification(t *testing.T) {
	if IsCorrupt(os.ErrPermission) || IsCorrupt(nil) || !IsCorrupt(ErrCorrupt) {
		t.Fatal("classification")
	}
}
