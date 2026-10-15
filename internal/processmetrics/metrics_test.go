package processmetrics

import (
	"runtime"
	"testing"
	"time"
)

func TestMeterWindowAndCounterReset(t *testing.T) {
	var meter Meter
	now := time.Unix(1000, 0)
	if got := meter.observe(now, counters{resident: 123, cpu: time.Second}); got.CPUPercent != nil || got.ResidentBytes != 123 {
		t.Fatal(got)
	}
	got := meter.observe(now.Add(2*time.Second), counters{cpu: 4 * time.Second})
	if got.CPUPercent == nil || *got.CPUPercent != 150 || got.WindowSeconds != 2 {
		t.Fatal(got)
	}
	got = meter.observe(now.Add(3*time.Second), counters{cpu: time.Second})
	if got.CPUPercent != nil {
		t.Fatal("counter regression produced CPU")
	}
	got = meter.observe(now.Add(3*time.Second), counters{cpu: 2 * time.Second})
	if got.CPUPercent != nil {
		t.Fatal("zero window produced CPU")
	}
}

func TestNativeProcessCounters(t *testing.T) {
	if runtime.GOOS != "windows" && runtime.GOOS != "linux" {
		t.Skip("unsupported platform")
	}
	var meter Meter
	got, err := meter.Read()
	if err != nil || got.ResidentBytes == 0 {
		t.Fatal(got, err)
	}
	if runtime.GOOS == "windows" && (got.PrivateBytes == nil || *got.PrivateBytes == 0) {
		t.Fatal("missing private commit")
	}
	if runtime.GOOS == "linux" && got.PrivateBytes != nil {
		t.Fatal("unsupported private metric reported")
	}
	if got.CPUPercent != nil {
		t.Fatal("first CPU sample must be unknown")
	}
	cached, err := meter.Read()
	if err != nil || cached.ResidentBytes != got.ResidentBytes {
		t.Fatal(cached, err)
	}
}
