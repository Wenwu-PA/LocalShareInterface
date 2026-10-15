//go:build !windows && !linux

package processmetrics

import "errors"

func readCounters() (counters, error) {
	return counters{}, errors.New("process metrics require Windows or Linux")
}
