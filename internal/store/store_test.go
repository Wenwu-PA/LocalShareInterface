package store

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
)

func TestUpgradeFileDatabaseToShares(t *testing.T) {
	name := filepath.Join(t.TempDir(), "previous.sqlite3")
	db, err := sql.Open("sqlite", name)
	if err != nil {
		t.Fatal(err)
	}
	for _, migration := range []string{schema, monitorSchema, notificationSchema, deviceSchema, fileSchema} {
		if _, err = db.Exec(migration); err != nil {
			db.Close()
			t.Fatal(err)
		}
	}
	if _, err = db.Exec("INSERT INTO transfers(user_id,path,direction,size,started,status) VALUES(1,'existing.txt','download',42,1,'complete')"); err != nil {
		t.Fatal(err)
	}
	db.Close()
	db, err = Open(name)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var version int
	var shareID string
	var size int
	if err = db.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != 6 {
		t.Fatal(version, err)
	}
	if err = db.QueryRow("SELECT size,share_id FROM transfers WHERE path='existing.txt'").Scan(&size, &shareID); err != nil || size != 42 || shareID != "" {
		t.Fatal(size, shareID, err)
	}
}

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
