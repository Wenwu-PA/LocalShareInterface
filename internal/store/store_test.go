package store

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMigrationBackupReopen(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "db.sqlite3")
	db, e := Open(path)
	if e != nil {
		t.Fatal(e)
	}
	_, e = db.Exec("INSERT INTO settings VALUES('test','preserved')")
	if e != nil {
		t.Fatal(e)
	}
	for i := 0; i < 4; i++ {
		if e = Backup(db, filepath.Join(dir, "backups"), 2); e != nil {
			t.Fatal(e)
		}
	}
	db.Close()
	files, _ := filepath.Glob(filepath.Join(dir, "backups", "*.sqlite3"))
	if len(files) != 2 {
		t.Fatal(files)
	}
	db, e = Open(path)
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	var value string
	if e = db.QueryRow("SELECT value FROM settings WHERE key='test'").Scan(&value); e != nil || value != "preserved" {
		t.Fatal(value, e)
	}
	backup, e := Open(files[1])
	if e != nil {
		t.Fatal(e)
	}
	defer backup.Close()
	if e = backup.QueryRow("SELECT value FROM settings WHERE key='test'").Scan(&value); e != nil {
		t.Fatal(e)
	}
}
func TestRejectCorruptAndFutureDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "db.sqlite3")
	if e := os.WriteFile(path, []byte("corrupt"), 0600); e != nil {
		t.Fatal(e)
	}
	if db, e := Open(path); e == nil {
		db.Close()
		t.Fatal("accepted corruption")
	}
	path = filepath.Join(t.TempDir(), "new.sqlite3")
	db, e := Open(path)
	if e != nil {
		t.Fatal(e)
	}
	_, e = db.Exec("PRAGMA user_version=999")
	if e != nil {
		t.Fatal(e)
	}
	db.Close()
	if db, e = Open(path); e == nil {
		db.Close()
		t.Fatal("accepted future schema")
	}
}
