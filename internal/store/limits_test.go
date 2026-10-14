package store

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"path/filepath"
	"testing"
)

func TestDatabaseSizeLimitAndReconnect(t *testing.T) {
	name := filepath.Join(t.TempDir(), "данные #1.sqlite3")
	db, e := OpenBounded(name, 2<<20)
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	if _, e = db.Exec("CREATE TABLE fill_test(data BLOB)"); e != nil {
		t.Fatal(e)
	}
	full := false
	for i := 0; i < 10; i++ {
		if _, e = db.Exec("INSERT INTO fill_test VALUES(zeroblob(524288))"); e != nil {
			var code interface{ Code() int }
			if !errors.As(e, &code) || code.Code()&255 != 13 {
				t.Fatal(e)
			}
			full = true
			break
		}
	}
	if !full {
		t.Fatal("database grew past limit")
	}
	var integrity string
	if e = db.QueryRow("PRAGMA quick_check").Scan(&integrity); e != nil || integrity != "ok" {
		t.Fatal(integrity, e)
	}
	// Force database/sql to discard its connection and check that DSN reapplies limits.
	conn, e := db.Conn(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	conn.Raw(func(any) error { return driver.ErrBadConn })
	conn.Close()
	var max, foreign int
	if e = db.QueryRow("PRAGMA max_page_count").Scan(&max); e != nil || max != 512 {
		t.Fatal(max, e)
	}
	if e = db.QueryRow("PRAGMA foreign_keys").Scan(&foreign); e != nil || foreign != 1 {
		t.Fatal(foreign, e)
	}
	if _, e = db.Exec("DELETE FROM fill_test"); e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec("INSERT INTO fill_test VALUES(zeroblob(524288))"); e != nil {
		t.Fatal(e)
	}
}
func TestDatabaseLimitExistingPageSizes(t *testing.T) {
	name := filepath.Join(t.TempDir(), "large-pages.sqlite3")
	db, e := sql.Open("sqlite", name)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec("PRAGMA page_size=8192; CREATE TABLE saved(value TEXT); INSERT INTO saved VALUES('preserved')"); e != nil {
		t.Fatal(e)
	}
	db.Close()
	db, e = OpenBounded(name, 2<<20)
	if e != nil {
		t.Fatal(e)
	}
	var max int
	if e = db.QueryRow("PRAGMA max_page_count").Scan(&max); e != nil || max != 256 {
		t.Fatal(max, e)
	}
	if _, e = db.Exec("INSERT INTO saved VALUES(zeroblob(1048576))"); e != nil {
		t.Fatal(e)
	}
	db.Close()
	if over, e := OpenBounded(name, 512<<10); e == nil {
		over.Close()
		t.Fatal("accepted oversized existing database")
	}
	db, e = OpenBounded(name, 2<<20)
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	var value string
	if e = db.QueryRow("SELECT value FROM saved LIMIT 1").Scan(&value); e != nil || value != "preserved" {
		t.Fatal(value, e)
	}
}
