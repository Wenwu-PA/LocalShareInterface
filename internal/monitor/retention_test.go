package monitor

import (
	"context"
	"lanbridge/internal/store"
	"path/filepath"
	"testing"
	"time"
)

func TestHistoryRetention(t *testing.T) {
	for _, tc := range []struct {
		name, settings string
		want           int
		fail           bool
	}{{"legacy", `{}`, 2, false}, {"thirty days", `{"monitor_history_days":30}`, 1, false}, {"invalid", `{"monitor_history_days":29}`, 3, true}, {"malformed", `{`, 3, true}} {
		t.Run(tc.name, func(t *testing.T) {
			db, err := store.Open(filepath.Join(t.TempDir(), "retention.sqlite3"))
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			if _, err = db.Exec("INSERT INTO monitors(id,config) VALUES(1,'{}')"); err != nil {
				t.Fatal(err)
			}
			if _, err = db.Exec("INSERT INTO settings(key,value) VALUES('appearance',?)", tc.settings); err != nil {
				t.Fatal(err)
			}
			now := time.Date(2026, 10, 19, 0, 0, 0, 0, time.UTC)
			for _, age := range []int{30, 40, 400} {
				if _, err = db.Exec("INSERT INTO aggregates(monitor_id,bucket,resolution,total,up,latency_sum) VALUES(1,?,86400,1,1,10)", now.Add(-time.Duration(age)*24*time.Hour).Unix()); err != nil {
					t.Fatal(err)
				}
			}
			for repeat := 0; repeat < 2; repeat++ {
				err = Compact(context.Background(), db, now)
				if (err != nil) != tc.fail {
					t.Fatalf("compact error=%v", err)
				}
				var count int
				if err = db.QueryRow("SELECT COUNT(*) FROM aggregates").Scan(&count); err != nil || count != tc.want {
					t.Fatal(count, err)
				}
			}
		})
	}
}

// Exercise the real compactor with historical timestamps; no production retention override.
func TestCompactionStagesAndRawBoundary(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "stages.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	now := time.Date(2026, 10, 23, 0, 0, 0, 0, time.UTC)
	exec := func(q string, args ...any) {
		t.Helper()
		if _, e := db.Exec(q, args...); e != nil {
			t.Fatal(e)
		}
	}
	exec("INSERT INTO monitors(id,config) VALUES(1,'{}')")
	exec("INSERT INTO samples VALUES(1,?,1,10),(1,?,1,10)", now.Add(-48*time.Hour).Unix()-1, now.Add(-48*time.Hour).Unix())
	old := now.Add(-72 * time.Hour).Unix()
	exec("INSERT INTO aggregates VALUES(1,?,60,2,1,10),(1,?,60,3,2,30)", old, old+60)
	exec("INSERT INTO aggregates VALUES(1,?,3600,7,6,70)", now.Add(-40*24*time.Hour).Unix())
	exec("INSERT INTO aggregates VALUES(1,?,86400,11,10,100)", now.Add(-400*24*time.Hour).Unix())
	for i := 0; i < 2; i++ {
		if e := Compact(context.Background(), db, now); e != nil {
			t.Fatal(e)
		}
		for _, test := range []struct {
			resolution, total, up int
			latency               float64
		}{{3600, 5, 3, 40}, {86400, 7, 6, 70}} {
			var total, up int
			var latency float64
			if e := db.QueryRow("SELECT SUM(total),SUM(up),SUM(latency_sum) FROM aggregates WHERE resolution=?", test.resolution).Scan(&total, &up, &latency); e != nil {
				t.Fatal(e)
			}
			if total != test.total || up != test.up || latency != test.latency {
				t.Fatal(test, total, up, latency)
			}
		}
		var count int
		var at int64
		if e := db.QueryRow("SELECT COUNT(*),MIN(at) FROM samples").Scan(&count, &at); e != nil {
			t.Fatal(e)
		}
		if count != 1 || at != now.Add(-48*time.Hour).Unix() {
			t.Fatal(count, at)
		}
		if e := db.QueryRow("SELECT COUNT(*) FROM aggregates WHERE resolution=60").Scan(&count); e != nil || count != 0 {
			t.Fatal(count, e)
		}
	}
}

func TestCompactionStoragePlateau(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "plateau.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for id := 1; id <= 50; id++ {
		if _, err = db.Exec("INSERT INTO monitors(id,config) VALUES(?,'{}')", id); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = db.Exec(`INSERT INTO settings(key,value) VALUES('appearance','{"monitor_history_days":30}')`); err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	var baseline, maxPages, minPages int
	for hour := 0; hour < 75*24; hour++ {
		now := start.Add(time.Duration(hour) * time.Hour)
		tx, e := db.Begin()
		if e != nil {
			t.Fatal(e)
		}
		if _, e = tx.Exec("INSERT INTO samples SELECT id,?,1,10 FROM monitors", now.Unix()); e != nil {
			tx.Rollback()
			t.Fatal(e)
		}
		if _, e = tx.Exec("INSERT INTO aggregates SELECT id,?,60,1,1,10 FROM monitors", now.Unix()); e != nil {
			tx.Rollback()
			t.Fatal(e)
		}
		if e = tx.Commit(); e != nil {
			t.Fatal(e)
		}
		if e = Compact(context.Background(), db, now); e != nil {
			t.Fatal(e)
		}
		if hour%24 != 23 {
			continue
		}
		if _, e = db.Exec("PRAGMA wal_checkpoint(TRUNCATE)"); e != nil {
			t.Fatal(e)
		}
		var pages, free, size, samples, aggregates int
		for q, dest := range map[string]*int{"PRAGMA page_count": &pages, "PRAGMA freelist_count": &free, "PRAGMA page_size": &size, "SELECT COUNT(*) FROM samples": &samples, "SELECT COUNT(*) FROM aggregates": &aggregates} {
			if e = db.QueryRow(q).Scan(dest); e != nil {
				t.Fatal(e)
			}
		}
		if hour >= 45*24 {
			if baseline == 0 {
				baseline = pages
				minPages = pages
			}
			if pages > maxPages {
				maxPages = pages
			}
			if pages < minPages {
				minPages = pages
			}
			if samples != 50*49 || aggregates != 50*721 {
				t.Fatalf("counts not stable day=%d samples=%d aggregates=%d", hour/24+1, samples, aggregates)
			}
		}
		if hour/24+1 == 46 || hour/24+1 == 75 {
			t.Logf("day=%d pages=%d bytes=%d freelist=%d samples=%d aggregates=%d", hour/24+1, pages, pages*size, free, samples, aggregates)
		}
	}
	if maxPages > baseline+baseline/10 {
		t.Fatalf("no plateau: baseline=%d max=%d", baseline, maxPages)
	}
	t.Logf("75 simulated days, 50 monitors, hourly samples, last 30 days pages=%d..%d; no VACUUM", minPages, maxPages)
	var integrity string
	if err = db.QueryRow("PRAGMA integrity_check").Scan(&integrity); err != nil || integrity != "ok" {
		t.Fatal(integrity, err)
	}
}
