package store

import (
	"database/sql"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// Each connection receives these pragmas through the driver DSN, so a replaced
// database/sql connection cannot silently lose its size limit or foreign keys.
func databaseDSN(name string, maxBytes int64) (string, int64, error) {
	if maxBytes < 0 {
		return "", 0, errors.New("лимит SQLite не может быть отрицательным")
	}
	pageSize := int64(4096)
	f, e := os.Open(name)
	if e == nil {
		var header [18]byte
		_, readErr := io.ReadFull(f, header[:])
		f.Close()
		if readErr == nil && string(header[:16]) == "SQLite format 3\x00" {
			size := int64(binary.BigEndian.Uint16(header[16:]))
			if size == 1 {
				size = 65536
			}
			if size >= 512 && size <= 65536 && size&(size-1) == 0 {
				pageSize = size
			}
		}
	} else if !os.IsNotExist(e) {
		return "", 0, e
	}
	absolute, e := filepath.Abs(name)
	if e != nil {
		return "", 0, e
	}
	slash := filepath.ToSlash(absolute)
	if !strings.HasPrefix(slash, "/") {
		slash = "/" + slash
	}
	u := url.URL{Scheme: "file", Path: slash}
	q := url.Values{}
	for _, pragma := range []string{"busy_timeout(5000)", "foreign_keys(ON)", "journal_mode(WAL)", "synchronous(NORMAL)", "wal_autocheckpoint(1000)", "journal_size_limit(16777216)"} {
		q.Add("_pragma", pragma)
	}
	var pages int64
	if maxBytes > 0 {
		pages = maxBytes / pageSize
		if pages < 1 {
			return "", 0, errors.New("лимит SQLite меньше одной страницы")
		}
		q.Add("_pragma", fmt.Sprintf("max_page_count(%d)", pages))
	}
	u.RawQuery = q.Encode()
	return u.String(), pages, nil
}

func checkPageLimit(db *sql.DB, pages int64) error {
	if pages == 0 {
		return nil
	}
	var count, limit int64
	if e := db.QueryRow("PRAGMA page_count").Scan(&count); e != nil {
		return e
	}
	if e := db.QueryRow("PRAGMA max_page_count").Scan(&limit); e != nil {
		return e
	}
	if count > pages || limit > pages {
		return errors.New("SQLite уже превышает database.max_mb: увеличьте лимит или освободите место в БД; данные не удалены")
	}
	return nil
}

func IsFull(err error) bool {
	var code interface{ Code() int }
	return errors.As(err, &code) && code.Code()&255 == 13
}
