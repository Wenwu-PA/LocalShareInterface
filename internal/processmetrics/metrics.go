package processmetrics

import (
	"sync"
	"time"
)

type Sample struct {
	ResidentBytes uint64   `json:"resident_bytes"`
	PrivateBytes  *uint64  `json:"private_bytes"`
	CPUPercent    *float64 `json:"cpu_percent"`
	WindowSeconds float64  `json:"window_seconds"`
}
type counters struct {
	resident uint64
	private  *uint64
	cpu      time.Duration
}

// Meter measures this process only. One fully occupied logical core is 100%.
// Calls share a one-second minimum sampling window and never spawn subprocesses.
type Meter struct {
	mu     sync.Mutex
	last   time.Time
	cpu    time.Duration
	sample Sample
}

func (m *Meter) Read() (Sample, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	if !m.last.IsZero() && now.Sub(m.last) < time.Second {
		return m.sample, nil
	}
	raw, err := readCounters()
	if err != nil {
		return Sample{}, err
	}
	return m.observe(now, raw), nil
}

func (m *Meter) observe(now time.Time, raw counters) Sample {
	next := Sample{ResidentBytes: raw.resident, PrivateBytes: raw.private}
	if !m.last.IsZero() && now.After(m.last) && raw.cpu >= m.cpu {
		next.WindowSeconds = now.Sub(m.last).Seconds()
		percent := 100 * (raw.cpu - m.cpu).Seconds() / next.WindowSeconds
		next.CPUPercent = &percent
	}
	m.last = now
	m.cpu = raw.cpu
	m.sample = next
	return next
}
