package files

import (
	"archive/zip"
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path"
	"strings"
	"time"
)

func (s *Service) walk(ctx context.Context, a Actor, name string, write bool, visit func(string, fs.FileInfo) error) error {
	count := 0
	return fs.WalkDir(s.Root.FS(), name, func(current string, entry fs.DirEntry, e error) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if e != nil {
			return e
		}
		if strings.EqualFold(entry.Name(), ".lanbridge") {
			if entry.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if _, e = Clean(current); e != nil {
			return e
		}
		count++
		if count > 100000 {
			return errors.New("операция ограничена 100000 объектами")
		}
		if e = s.Access(ctx, a, current, write); e != nil {
			return e
		}
		info, e := entry.Info()
		if e != nil {
			return e
		}
		if !info.IsDir() && !info.Mode().IsRegular() {
			return ErrAccess
		}
		return visit(current, info)
	})
}
func (s *Service) trashLocked(ctx context.Context, a Actor, name string) (string, error) {
	if name == "." {
		return "", ErrAccess
	}
	var size int64
	if e := s.walk(ctx, a, name, true, func(_ string, info fs.FileInfo) error {
		if !info.IsDir() {
			size += info.Size()
		}
		return nil
	}); e != nil {
		return "", e
	}
	acl, e := s.snapshotACL(ctx, name)
	if e != nil {
		return "", e
	}
	id := ID()
	if _, e := s.DB.ExecContext(ctx, "INSERT INTO trash(id,path,owner_id,size,created,acl) VALUES(?,?,?,?,?,?)", id, name, a.ID, size, time.Now().Unix(), acl); e != nil {
		return "", e
	}
	if e := s.Root.Rename(name, ".lanbridge/trash/"+id); e != nil {
		_, _ = s.DB.ExecContext(ctx, "DELETE FROM trash WHERE id=?", id)
		return "", e
	}
	return id, s.finishTrash(ctx, id, name)
}
func (s *Service) Trash(ctx context.Context, a Actor, raw string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.invalidateFolderSizes()
	name, e := s.check(ctx, a, raw, true)
	if e != nil {
		return "", e
	}
	return s.trashLocked(ctx, a, name)
}
func (s *Service) Move(ctx context.Context, a Actor, source, target, conflict string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.invalidateFolderSizes()
	from, e := s.check(ctx, a, source, true)
	if e != nil {
		return "", e
	}
	to, e := s.check(ctx, a, target, true)
	if e != nil {
		return "", e
	}
	if from == "." || to == "." || descendant(to, from) || descendant(from, to) || strings.EqualFold(from, to) {
		return "", ErrAccess
	}
	if e = s.walk(ctx, a, from, true, func(string, fs.FileInfo) error { return nil }); e != nil {
		return "", e
	}
	to, e = s.destination(to, conflict)
	if e != nil {
		return "", e
	}
	if e = s.Access(ctx, a, to, true); e != nil {
		return "", e
	}
	if _, e = s.Root.Lstat(to); e == nil {
		if conflict != "replace" {
			return "", ErrConflict
		}
		if _, e = s.trashLocked(ctx, a, to); e != nil {
			return "", e
		}
	} else if !errors.Is(e, os.ErrNotExist) {
		return "", e
	}
	o, e := s.planOperation(ctx, from, to, "move", a.ID)
	if e != nil {
		return "", e
	}
	if e = s.Root.Rename(from, to); e != nil {
		return "", e
	}
	return to, s.finishOperation(ctx, o)
}
func (s *Service) Copy(ctx context.Context, a Actor, source, target, conflict string) (string, error) {
	from, e := s.check(ctx, a, source, false)
	if e != nil {
		return "", e
	}
	to, e := s.check(ctx, a, target, true)
	if e != nil {
		return "", e
	}
	if from == "." || to == "." || descendant(to, from) || descendant(from, to) || strings.EqualFold(from, to) {
		return "", ErrAccess
	}
	var total int64
	if e = s.walk(ctx, a, from, false, func(_ string, info fs.FileInfo) error {
		if !info.IsDir() {
			total += info.Size()
		}
		return nil
	}); e != nil {
		return "", e
	}
	// Serialize mutations while copying; cancellation leaves only private staging data.
	s.mu.Lock()
	defer s.mu.Unlock()
	s.invalidateFolderSizes()
	if e = s.quota(ctx, a, total); e != nil {
		return "", e
	}
	to, e = s.destination(to, conflict)
	if e != nil {
		return "", e
	}
	temporary := ".lanbridge/uploads/" + ID() + ".copy"
	planned := false
	defer func() {
		if !planned {
			_ = s.Root.RemoveAll(temporary)
		}
	}()
	buffer := make([]byte, 64*1024)
	e = s.walk(ctx, a, from, false, func(name string, info fs.FileInfo) error {
		relative := strings.TrimPrefix(name, from)
		destination := temporary + relative
		if info.IsDir() {
			return s.Root.MkdirAll(destination, 0700)
		}
		in, e := s.Root.Open(name)
		if e != nil {
			return e
		}
		defer in.Close()
		out, e := s.Root.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if e != nil {
			return e
		}
		n, e := copyContext(ctx, out, in, buffer)
		if e == nil && n != info.Size() {
			e = errors.New("исходный файл изменился во время копирования")
		}
		if e == nil {
			e = out.Sync()
		}
		closeErr := out.Close()
		if e != nil {
			return e
		}
		return closeErr
	})
	if e != nil {
		return "", e
	}
	if e = s.Access(ctx, a, to, true); e != nil {
		return "", e
	}
	if _, e = s.Root.Lstat(to); e == nil {
		if conflict != "replace" {
			return "", ErrConflict
		}
		if _, e = s.trashLocked(ctx, a, to); e != nil {
			return "", e
		}
	} else if !errors.Is(e, os.ErrNotExist) {
		return "", e
	}
	o, e := s.planOperation(ctx, temporary, to, "copy", a.ID, from)
	if e != nil {
		return "", e
	}
	planned = true
	if e = s.Root.Rename(temporary, to); e != nil {
		return "", e
	}
	return to, s.finishOperation(ctx, o)
}
func (s *Service) Restore(ctx context.Context, a Actor, id, conflict string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.invalidateFolderSizes()
	var name, state, acl string
	var owner, size int64
	if e := s.DB.QueryRowContext(ctx, "SELECT path,owner_id,state,size,acl FROM trash WHERE id=?", id).Scan(&name, &owner, &state, &size, &acl); e != nil {
		return "", e
	}
	if !a.Admin && owner != a.ID {
		return "", ErrAccess
	}
	if state != "ready" {
		return "", errors.New("операция с корзиной ещё не завершена")
	}
	name, e := s.check(ctx, a, name, true)
	if e != nil {
		return "", e
	}
	if e = s.quota(ctx, a, size, true); e != nil {
		return "", e
	}
	if e = s.Root.MkdirAll(path.Dir(name), 0700); e != nil {
		return "", e
	}
	name, e = s.destination(name, conflict)
	if e != nil {
		return "", e
	}
	if e = s.Access(ctx, a, name, true); e != nil {
		return "", e
	}
	if _, e = s.Root.Lstat(name); e == nil {
		if conflict != "replace" {
			return "", ErrConflict
		}
		if _, e = s.trashLocked(ctx, a, name); e != nil {
			return "", e
		}
	} else if !errors.Is(e, os.ErrNotExist) {
		return "", e
	}
	if _, e = s.DB.ExecContext(ctx, "UPDATE trash SET path=?,state='restoring' WHERE id=?", name, id); e != nil {
		return "", e
	}
	if e = s.Root.Rename(".lanbridge/trash/"+id, name); e != nil {
		return "", e
	}
	return name, s.finishRestore(ctx, id, name, acl, owner)
}
func (s *Service) Zip(ctx context.Context, a Actor, paths []string, destination io.Writer) error {
	if len(paths) == 0 || len(paths) > 1000 {
		return errors.New("выберите 1–1000 объектов")
	}
	selected := []string{}
	for _, raw := range paths {
		name, e := s.check(ctx, a, raw, false)
		if e != nil {
			return e
		}
		covered := false
		for _, previous := range selected {
			if descendant(name, previous) {
				covered = true
				break
			}
		}
		if !covered {
			filtered := selected[:0]
			for _, previous := range selected {
				if !descendant(previous, name) {
					filtered = append(filtered, previous)
				}
			}
			selected = append(filtered, name)
		}
	}
	for _, name := range selected {
		if e := s.walk(ctx, a, name, false, func(string, fs.FileInfo) error { return nil }); e != nil {
			return e
		}
	}
	archive := zip.NewWriter(destination)
	buffer := make([]byte, 64*1024)
	for _, name := range selected {
		e := s.walk(ctx, a, name, false, func(current string, info fs.FileInfo) error {
			if current == "." {
				return nil
			}
			header, e := zip.FileInfoHeader(info)
			if e != nil {
				return e
			}
			header.Name = current
			if info.IsDir() {
				header.Name += "/"
			}
			header.Method = zip.Store
			out, e := archive.CreateHeader(header)
			if e != nil || info.IsDir() {
				return e
			}
			in, e := s.Root.Open(current)
			if e != nil {
				return e
			}
			defer in.Close()
			_, e = copyContext(ctx, out, in, buffer)
			return e
		})
		if e != nil {
			return e
		}
	}
	return archive.Close()
}
