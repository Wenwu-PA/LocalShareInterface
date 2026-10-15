package server

import (
	"context"
	"net/http"
	"os"
	"runtime"
	"time"

	"lanbridge/internal/files"
	"lanbridge/internal/processmetrics"
)

type resourceAlert struct {
	Code    string `json:"code"`
	Level   string `json:"level"`
	Message string `json:"message"`
}
type resourceSnapshot struct {
	Process          *processmetrics.Sample `json:"process"`
	At               int64                  `json:"at"`
	DatabaseBytes    int64                  `json:"database_bytes"`
	DatabaseLimit    int64                  `json:"database_limit"`
	ReusableBytes    int64                  `json:"reusable_bytes"`
	WALBytes         int64                  `json:"wal_bytes"`
	StorageAvailable uint64                 `json:"storage_available"`
	DataAvailable    uint64                 `json:"data_available"`
	HeapBytes        uint64                 `json:"heap_bytes"`
	Goroutines       int                    `json:"goroutines"`
	Alerts           []resourceAlert        `json:"alerts"`
}

func diskLevel(free, critical uint64) string {
	if free <= critical {
		return "critical"
	}
	warning := critical
	if critical <= ^uint64(0)/3 {
		warning = critical * 3
	}
	if free <= max(warning, uint64(1<<30)) {
		return "warning"
	}
	return "ok"
}

func databaseLevel(allocated, reusable, limit int64) string {
	// Free pages can still be reused without growing the physical DB file.
	used := max(0, allocated-reusable)
	if limit <= 0 {
		return "unknown"
	}
	if used >= limit-limit/50 {
		return "critical"
	}
	if used >= limit-limit/10 {
		return "warning"
	}
	return "ok"
}

func (s *Server) resourceStatus(ctx context.Context) (resourceSnapshot, error) {
	r := resourceSnapshot{At: time.Now().Unix(), DatabaseLimit: int64(s.Config.Database.MaxMB) << 20, Alerts: []resourceAlert{}}
	var pages, size, free int64
	for query, value := range map[string]*int64{"PRAGMA page_count": &pages, "PRAGMA page_size": &size, "PRAGMA freelist_count": &free} {
		if err := s.DB.QueryRowContext(ctx, query).Scan(value); err != nil {
			return r, err
		}
	}
	r.DatabaseBytes = pages * size
	r.ReusableBytes = free * size
	add := func(code, level, message string) {
		if level != "ok" {
			r.Alerts = append(r.Alerts, resourceAlert{code, level, message})
		}
	}
	add("database", databaseLevel(r.DatabaseBytes, r.ReusableBytes, r.DatabaseLimit), "БД приближается к лимиту. Освободите старую историю или увеличьте database.max_mb после проверки места на диске.")
	if info, err := os.Stat(s.Config.Storage.Data + string(os.PathSeparator) + "lanbridge-go.sqlite3-wal"); err == nil {
		r.WALBytes = info.Size()
	}
	var err error
	r.DataAvailable, _, err = files.DiskSpace(s.Config.Storage.Data)
	if err != nil {
		add("data_disk", "unknown", "Не удалось проверить свободное место на диске БД.")
	} else {
		add("data_disk", diskLevel(r.DataAvailable, uint64(r.DatabaseBytes)+(64<<20)), "Мало места на диске БД: требуется запас для WAL и резервной копии.")
	}
	if s.Files == nil {
		add("storage_disk", "unknown", "Файловое хранилище недоступно.")
	} else {
		settings, e := s.Files.Settings(ctx)
		if e != nil {
			return r, e
		}
		r.StorageAvailable, _, err = s.Files.Space()
		if err != nil {
			add("storage_disk", "unknown", "Не удалось проверить свободное место хранилища.")
		} else {
			add("storage_disk", diskLevel(r.StorageAvailable, uint64(max(settings.CriticalFree, 0))), "Мало места в хранилище. Освободите место; загрузки остановятся при достижении резерва.")
		}
	}
	process, processErr := s.processMeter.Read()
	if processErr != nil {
		add("process", "unknown", "Не удалось измерить CPU и память процесса.")
	} else {
		r.Process = &process
	}
	var memory runtime.MemStats
	runtime.ReadMemStats(&memory)
	r.HeapBytes = memory.HeapAlloc
	r.Goroutines = runtime.NumGoroutine()
	return r, nil
}

func (s *Server) getResources(w http.ResponseWriter, r *http.Request) {
	status, err := s.resourceStatus(r.Context())
	if err != nil {
		s.dbError(w, err)
		return
	}
	jsonResponse(w, 200, status)
}

// Log only severity transitions, including recovery, so a prolonged outage does
// not flood the rotating log. Sampling does not depend on an open browser.
func (s *Server) CheckResources(ctx context.Context) {
	status, err := s.resourceStatus(ctx)
	current := map[string]string{}
	for _, alert := range status.Alerts {
		current[alert.Code] = alert.Level
	}

	s.resourceMu.Lock()
	defer s.resourceMu.Unlock()
	if err != nil {
		if ctx.Err() != nil {
			return
		}
		if s.resourceLevels == nil {
			s.resourceLevels = map[string]string{}
		}
		if s.resourceLevels["resource_probe"] != "unknown" {
			s.Log.Warn("resource warning", "resource", "resource_probe", "level", "unknown")
		}
		s.resourceLevels["resource_probe"] = "unknown"
		return
	}
	if deliveryErr := s.queueResourceTransitions(ctx, status); deliveryErr != nil {
		if ctx.Err() == nil && !s.resourceDeliveryFailed {
			s.Log.Error("resource notification state write failed", "error", deliveryErr)
		}
		s.resourceDeliveryFailed = true
	} else if s.resourceDeliveryFailed {
		s.Log.Info("resource notification state writes restored")
		s.resourceDeliveryFailed = false
	}
	for code, level := range current {
		if s.resourceLevels[code] != level {
			s.Log.Warn("resource warning", "resource", code, "level", level)
		}
	}
	for code := range s.resourceLevels {
		if current[code] == "" {
			s.Log.Info("resource warning cleared", "resource", code)
		}
	}
	s.resourceLevels = current
}
