package server

import (
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestFullDatabaseReturnsActionableError(t *testing.T) {
	c := newClient(t)
	var pages int
	if e := c.db.QueryRow("PRAGMA page_count").Scan(&pages); e != nil {
		t.Fatal(e)
	}
	if _, e := c.db.Exec(fmt.Sprintf("PRAGMA max_page_count=%d", pages+8)); e != nil {
		t.Fatal(e)
	}
	_, e := c.db.Exec("INSERT INTO settings(key,value) VALUES('full-test',zeroblob(1048576))")
	if e == nil {
		t.Fatal("expected database full")
	}
	for _, handler := range []func(*httptest.ResponseRecorder){func(w *httptest.ResponseRecorder) { c.server.dbError(w, e) }, func(w *httptest.ResponseRecorder) { c.server.fileError(w, e) }} {
		w := httptest.NewRecorder()
		handler(w)
		if w.Code != 507 || !strings.Contains(w.Body.String(), "database_full") {
			t.Fatal(w.Code, w.Body.String())
		}
	}
}
