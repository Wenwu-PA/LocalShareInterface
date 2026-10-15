package processmetrics

import (
	"errors"
	"golang.org/x/sys/unix"
	"os"
	"strconv"
	"strings"
	"time"
)

func readCounters() (counters, error) {
	var usage unix.Rusage
	if err := unix.Getrusage(unix.RUSAGE_SELF, &usage); err != nil {
		return counters{}, err
	}
	data, err := os.ReadFile("/proc/self/statm")
	if err != nil {
		return counters{}, err
	}
	fields := strings.Fields(string(data))
	if len(fields) < 2 {
		return counters{}, errors.New("invalid /proc/self/statm")
	}
	pages, err := strconv.ParseUint(fields[1], 10, 64)
	if err != nil {
		return counters{}, err
	}
	seconds := usage.Utime.Sec + usage.Stime.Sec
	micros := usage.Utime.Usec + usage.Stime.Usec
	return counters{resident: pages * uint64(os.Getpagesize()), cpu: time.Duration(seconds)*time.Second + time.Duration(micros)*time.Microsecond}, nil
}
