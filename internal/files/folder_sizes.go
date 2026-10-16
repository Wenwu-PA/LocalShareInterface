package files

import (
	"context"
	"errors"
	"sync"
	"time"
)

type FolderSize struct {
	State      string `json:"state"`
	Bytes      int64  `json:"bytes"`
	Files      int64  `json:"files"`
	Calculated int64  `json:"calculated"`
}
type sizeKey struct {
	path  string
	admin bool
}
type sizeJob struct {
	key     sizeKey
	actor   Actor
	result  FolderSize
	expires time.Time
}
type folderSizer struct {
	mu     sync.Mutex
	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{}
	queue  chan *sizeJob
	cache  map[sizeKey]*sizeJob
}

func (s *Service) startFolderSizer() {
	ctx, cancel := context.WithCancel(context.Background())
	z := &folderSizer{ctx: ctx, cancel: cancel, done: make(chan struct{}), queue: make(chan *sizeJob, 64), cache: make(map[sizeKey]*sizeJob)}
	s.sizes = z
	go func() {
		defer close(z.done)
		for {
			select {
			case <-ctx.Done():
				return
			case job := <-z.queue:
				s.calculateFolderSize(job)
			}
		}
	}()
}

// Called before mutations while the service lock is held. No pre-change ACL
// result may be returned while a mutation is committing.
func (s *Service) invalidateFolderSizes() {
	if s.sizes == nil {
		return
	}
	s.sizes.mu.Lock()
	clear(s.sizes.cache)
	s.sizes.mu.Unlock()
}
func (s *Service) FolderSize(ctx context.Context, a Actor, raw string) (FolderSize, error) {
	name, err := s.check(ctx, a, raw, false)
	if err != nil {
		return FolderSize{}, err
	}
	info, err := s.Root.Stat(name)
	if err != nil {
		return FolderSize{}, err
	}
	if !info.IsDir() {
		return FolderSize{}, ErrAccess
	}
	z := s.sizes
	z.mu.Lock()
	defer z.mu.Unlock()
	if z.ctx.Err() != nil {
		return FolderSize{}, z.ctx.Err()
	}
	key := sizeKey{name, a.Admin}
	if job := z.cache[key]; job != nil && time.Now().Before(job.expires) {
		return job.result, nil
	}
	if len(z.cache) >= 128 {
		var oldest *sizeJob
		for _, job := range z.cache {
			if oldest == nil || job.expires.Before(oldest.expires) {
				oldest = job
			}
		}
		delete(z.cache, oldest.key)
	}
	job := &sizeJob{key: key, actor: a, result: FolderSize{State: "pending"}, expires: time.Now().Add(time.Minute)}
	select {
	case z.queue <- job:
		z.cache[key] = job
		return job.result, nil
	default:
		return FolderSize{State: "busy"}, nil
	}
}
func (s *Service) calculateFolderSize(job *sizeJob) {
	z := s.sizes
	z.mu.Lock()
	current := z.cache[job.key] == job
	z.mu.Unlock()
	if !current {
		return
	}
	result := FolderSize{State: "error"}
	defer func() {
		if value := recover(); value != nil {
			s.Log.Error("folder size worker failed")
		}
		z.mu.Lock()
		defer z.mu.Unlock()
		if z.cache[job.key] == job {
			job.result = result
			job.expires = time.Now().Add(30 * time.Second)
		}
	}()
	ctx, cancel := context.WithTimeout(z.ctx, 3*time.Second)
	defer cancel()
	// Waiting for a long copy must not trap shutdown or an unbounded queue.
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for !s.mu.TryLock() {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
	defer s.mu.Unlock()
	if _, err := s.check(ctx, job.actor, job.key.path, false); err != nil {
		return
	}
	query := `SELECT COALESCE(SUM(size),0),COUNT(*) FROM file_index WHERE is_dir=0 AND (?='.' OR substr(path,1,length(?)+1)=?||'/')`
	if !job.actor.Admin {
		query += " AND " + visibleIndexPredicate
	}
	if err := s.DB.QueryRowContext(ctx, query, job.key.path, job.key.path, job.key.path).Scan(&result.Bytes, &result.Files); err != nil {
		if !errors.Is(err, context.Canceled) {
			s.Log.Warn("folder size calculation failed", "error", err)
		}
		return
	}
	result.State = "ready"
	result.Calculated = time.Now().Unix()
}
