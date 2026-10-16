package files

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func awaitFolderSize(t *testing.T, s *Service, a Actor, path string) FolderSize {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		result, err := s.FolderSize(context.Background(), a, path)
		if err != nil {
			t.Fatal(err)
		}
		if result.State == "ready" {
			return result
		}
		if result.State == "error" {
			t.Fatal("size calculation failed")
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("size not ready")
	return FolderSize{}
}
func TestFolderSizeUsesVisibleIndexAndInvalidatesOnChanges(t *testing.T) {
	s, admin := testService(t)
	ctx := context.Background()
	user := Actor{ID: 2}
	if err := s.Mkdir(ctx, admin, "a/private"); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{"a/visible": "abc", "a/private/secret": "12345"} {
		f, err := s.Root.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		_, err = f.WriteString(content)
		f.Close()
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Index(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.SetAccess(ctx, admin, "a/private", "admin"); err != nil {
		t.Fatal(err)
	}
	all := awaitFolderSize(t, s, admin, "a")
	visible := awaitFolderSize(t, s, user, "a")
	if all.Bytes != 8 || all.Files != 2 || visible.Bytes != 3 || visible.Files != 1 {
		t.Fatal(all, visible)
	}
	cached, err := s.FolderSize(ctx, user, "a")
	if err != nil || cached != visible {
		t.Fatal(cached, err)
	}
	if _, err = s.FolderSize(ctx, user, "a/private"); err != ErrAccess {
		t.Fatal(err)
	}
	if err = s.SetAccess(ctx, admin, "a/private", "users"); err != nil {
		t.Fatal(err)
	}
	if got := awaitFolderSize(t, s, user, "a"); got.Bytes != 8 {
		t.Fatal(got)
	}
	if _, err = s.Move(ctx, admin, "a/private", "other", "skip"); err != nil {
		t.Fatal(err)
	}
	if got := awaitFolderSize(t, s, user, "a"); got.Bytes != 3 {
		t.Fatal(got)
	}
	if got := awaitFolderSize(t, s, user, "."); got.Bytes != 8 {
		t.Fatal(got)
	}
}
func TestFolderSizeQueueAndCacheStayBounded(t *testing.T) {
	s, admin := testService(t)
	ctx := context.Background()
	for i := 0; i < 150; i++ {
		if err := s.Root.Mkdir(fmt.Sprint(i), 0700); err != nil {
			t.Fatal(err)
		}
	}
	s.mu.Lock()
	busy := 0
	for i := 0; i < 150; i++ {
		result, err := s.FolderSize(ctx, admin, fmt.Sprint(i))
		if err != nil {
			s.mu.Unlock()
			t.Fatal(err)
		}
		if result.State == "busy" {
			busy++
		}
	}
	s.sizes.mu.Lock()
	cached, queued := len(s.sizes.cache), len(s.sizes.queue)
	s.sizes.mu.Unlock()
	s.mu.Unlock()
	if cached > 128 || queued > 64 || busy == 0 {
		t.Fatal(cached, queued, busy)
	}
}
func TestFolderSizeShutdownWhileStorageBusy(t *testing.T) {
	s, admin := testService(t)
	s.mu.Lock()
	if _, err := s.FolderSize(context.Background(), admin, "."); err != nil {
		s.mu.Unlock()
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { s.sizes.cancel(); <-s.sizes.done; close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		s.mu.Unlock()
		t.Fatal("worker did not stop")
	}
	s.mu.Unlock()
}

func TestFolderSizeEvictsOldestCachedResult(t *testing.T) {
	s, admin := testService(t)
	s.sizes.mu.Lock()
	for i := 0; i < 128; i++ {
		key := sizeKey{fmt.Sprint(i), true}
		s.sizes.cache[key] = &sizeJob{key: key, result: FolderSize{State: "ready"}, expires: time.Now().Add(time.Minute + time.Duration(i)*time.Second)}
	}
	s.sizes.mu.Unlock()
	if _, err := s.FolderSize(context.Background(), admin, "."); err != nil {
		t.Fatal(err)
	}
	s.sizes.mu.Lock()
	defer s.sizes.mu.Unlock()
	if len(s.sizes.cache) != 128 || s.sizes.cache[sizeKey{"0", true}] != nil || s.sizes.cache[sizeKey{".", true}] == nil {
		t.Fatal("cache eviction failed")
	}
}
