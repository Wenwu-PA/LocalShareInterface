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
