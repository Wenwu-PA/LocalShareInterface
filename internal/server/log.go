package server

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

type RotatingLog struct {
	mu      sync.Mutex
	path    string
	limit   int64
	backups int
	file    *os.File
	size    int64
}

func NewLog(path string, maxMB, backups int) (*RotatingLog, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	l := &RotatingLog{path: path, limit: int64(maxMB) * 1024 * 1024, backups: backups}
	err := l.open()
	return l, err
}
func (l *RotatingLog) open() error {
	f, e := os.OpenFile(l.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if e != nil {
		return e
	}
	s, e := f.Stat()
	if e != nil {
		f.Close()
		return e
	}
	l.file = f
	l.size = s.Size()
	return nil
}
func (l *RotatingLog) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.size+int64(len(p)) > l.limit {
		if err := l.file.Close(); err != nil {
			return 0, err
		}
		oldest := fmt.Sprintf("%s.%d", l.path, l.backups)
		if err := os.Remove(oldest); err != nil && !os.IsNotExist(err) {
			return 0, err
		}
		for i := l.backups - 1; i >= 1; i-- {
			if err := os.Rename(fmt.Sprintf("%s.%d", l.path, i), fmt.Sprintf("%s.%d", l.path, i+1)); err != nil && !os.IsNotExist(err) {
				return 0, err
			}
		}
		if err := os.Rename(l.path, l.path+".1"); err != nil {
			return 0, err
		}
		if err := l.open(); err != nil {
			return 0, err
		}
	}
	n, e := l.file.Write(p)
	l.size += int64(n)
	return n, e
}
func (l *RotatingLog) Close() error { l.mu.Lock(); defer l.mu.Unlock(); return l.file.Close() }
