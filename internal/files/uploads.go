package files

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"os"
	"path"
	"strings"
	"sync"
	"time"
)

const ChunkLimit = 1024 * 1024

type Upload struct {
	ID       string `json:"id"`
	Path     string `json:"path"`
	Owner    int64  `json:"owner"`
	Size     int64  `json:"size"`
	Offset   int64  `json:"offset"`
	SHA256   string `json:"sha256"`
	State    string `json:"state"`
	Conflict string `json:"conflict"`
	Created  int64  `json:"created"`
	Updated  int64  `json:"updated"`
	Error    string `json:"error"`
}

func (s *Service) uploadLock(id string) *sync.Mutex {
	h := fnv.New32a()
	h.Write([]byte(id))
	return &s.uploadLocks[h.Sum32()%64]
}
func (s *Service) Upload(ctx context.Context, a Actor, id string) (Upload, error) {
	var u Upload
	e := s.DB.QueryRowContext(ctx, "SELECT id,path,owner_id,size,offset,sha256,state,conflict,created,updated,error FROM uploads WHERE id=?", id).Scan(&u.ID, &u.Path, &u.Owner, &u.Size, &u.Offset, &u.SHA256, &u.State, &u.Conflict, &u.Created, &u.Updated, &u.Error)
	if e != nil {
		return u, e
	}
	if !a.Admin && a.ID != u.Owner {
		return Upload{}, ErrAccess
	}
	return u, nil
}
func (s *Service) quota(ctx context.Context, a Actor, size int64, diskAlreadyUsed ...bool) error {
	c, e := s.Settings(ctx)
	if e != nil {
		return e
	}
	var used, userUsed, reserved, userReserved, remaining int64
	for _, q := range []struct {
		sql  string
		args []any
		dst  *int64
	}{{"SELECT COALESCE(SUM(size),0) FROM file_index", nil, &used}, {"SELECT COALESCE(SUM(size),0) FROM file_index WHERE owner_id=?", []any{a.ID}, &userUsed}, {"SELECT COALESCE(SUM(size),0) FROM uploads WHERE state IN ('pending','committing')", nil, &reserved}, {"SELECT COALESCE(SUM(size),0) FROM uploads WHERE owner_id=? AND state IN ('pending','committing')", []any{a.ID}, &userReserved}, {"SELECT COALESCE(SUM(size-offset),0) FROM uploads WHERE state='pending'", nil, &remaining}} {
		if e = s.DB.QueryRowContext(ctx, q.sql, q.args...).Scan(q.dst); e != nil {
			return e
		}
	}
	if c.GlobalLimit > 0 && size > c.GlobalLimit-used-reserved || c.UserLimit > 0 && size > c.UserLimit-userUsed-userReserved {
		return ErrQuota
	}
	available, _, e := freeSpace(s.Root.Name())
	if e != nil {
		return e
	}
	physical := size
	if len(diskAlreadyUsed) > 0 && diskAlreadyUsed[0] {
		physical = 0
	}
	if available < uint64(physical+remaining+c.CriticalFree) {
		return ErrQuota
	}
	return nil
}
func (s *Service) destination(name, conflict string) (string, error) {
	// Check sibling case folding even on a case-sensitive Linux filesystem.
	parent, e := s.Root.Open(path.Dir(name))
	if e != nil {
		return "", e
	}
	defer parent.Close()
	base := path.Base(name)
	existing := ""
	for {
		entries, e := parent.ReadDir(256)
		for _, entry := range entries {
			if strings.EqualFold(entry.Name(), base) {
				existing = entry.Name()
				break
			}
		}
		if existing != "" || e == io.EOF {
			break
		}
		if e != nil {
			return "", e
		}
	}
	if existing == "" {
		return name, nil
	}
	if conflict == "replace" {
		return path.Join(path.Dir(name), existing), nil
	}
	if conflict != "both" {
		return "", ErrConflict
	}
	ext := path.Ext(base)
	stem := strings.TrimSuffix(base, ext)
	for i := 1; i <= 10000; i++ {
		candidate := path.Join(path.Dir(name), fmt.Sprintf("%s (%d)%s", stem, i, ext))
		if _, e := Clean(candidate); e != nil {
			return "", e
		}
		if _, e = s.destination(candidate, "skip"); e == nil {
			return candidate, nil
		} else if !errors.Is(e, ErrConflict) {
			return "", e
		}
	}
	return "", ErrConflict
}
func (s *Service) Begin(ctx context.Context, a Actor, raw string, size int64, hash, conflict string) (Upload, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.invalidateFolderSizes()
	var empty Upload
	name, e := s.check(ctx, a, raw, true)
	if e != nil {
		return empty, e
	}
	if name == "." || size < 0 || size > 1<<40 {
		return empty, errors.New("размер файла: 0–1 ТиБ")
	}
	decoded, e := hex.DecodeString(hash)
	if e != nil || len(decoded) != 32 {
		return empty, errors.New("нужен SHA-256 файла")
	}
	hash = strings.ToLower(hash)
	if conflict != "replace" && conflict != "both" && conflict != "skip" {
		return empty, errors.New("конфликт: replace, both или skip")
	}
	var count int
	if e = s.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM uploads WHERE owner_id=? AND state IN ('pending','committing')", a.ID).Scan(&count); e != nil {
		return empty, e
	}
	if count >= 1000 {
		return empty, errors.New("не более 1000 незавершённых загрузок")
	}
	if e = s.quota(ctx, a, size); e != nil {
		return empty, e
	}
	if e = s.Root.MkdirAll(path.Dir(name), 0700); e != nil {
		return empty, e
	}
	name, e = s.destination(name, conflict)
	if e != nil {
		return empty, e
	}
	if e = s.Access(ctx, a, name, true); e != nil {
		return empty, e
	}
	now := time.Now().Unix()
	u := Upload{ID: ID(), Path: name, Owner: a.ID, Size: size, SHA256: hash, State: "pending", Conflict: conflict, Created: now, Updated: now}
	temporary := ".lanbridge/uploads/" + u.ID + ".part"
	f, e := s.Root.OpenFile(temporary, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if e != nil {
		return empty, e
	}
	f.Close()
	if _, e = s.DB.ExecContext(ctx, "INSERT INTO uploads(id,path,owner_id,size,sha256,conflict,created,updated) VALUES(?,?,?,?,?,?,?,?)", u.ID, u.Path, u.Owner, u.Size, u.SHA256, u.Conflict, now, now); e != nil {
		s.Root.Remove(temporary)
		return empty, e
	}
	return u, nil
}
func (s *Service) Chunk(ctx context.Context, a Actor, id string, offset, length int64, body io.Reader) (Upload, error) {
	lock := s.uploadLock(id)
	lock.Lock()
	defer lock.Unlock()
	u, e := s.Upload(ctx, a, id)
	if e != nil {
		return u, e
	}
	if u.State != "pending" || offset != u.Offset {
		return u, errors.New("позиция загрузки изменилась; обновите состояние")
	}
	if length < 1 || length > ChunkLimit || length > u.Size-u.Offset {
		return u, errors.New("кусок: 1–1048576 байт, в пределах размера файла")
	}
	if e = s.Access(ctx, a, u.Path, true); e != nil {
		return u, e
	}
	c, e := s.Settings(ctx)
	if e != nil {
		return u, e
	}
	available, _, e := freeSpace(s.Root.Name())
	if e != nil {
		return u, e
	}
	if available < uint64(c.CriticalFree+length) {
		return u, ErrQuota
	}
	f, e := s.Root.OpenFile(".lanbridge/uploads/"+u.ID+".part", os.O_RDWR, 0600)
	if e != nil {
		return u, e
	}
	defer f.Close()
	if e = f.Truncate(u.Offset); e != nil {
		return u, e
	}
	if _, e = f.Seek(u.Offset, io.SeekStart); e != nil {
		return u, e
	}
	copied, e := copyContext(ctx, f, io.LimitReader(body, length), make([]byte, 64*1024))
	if e == nil && copied != length {
		e = io.ErrUnexpectedEOF
	}
	if e == nil {
		e = f.Sync()
	}
	if e != nil {
		_ = f.Truncate(u.Offset)
		return u, e
	}
	next := u.Offset + length
	if _, e = s.DB.ExecContext(ctx, "UPDATE uploads SET offset=?,updated=? WHERE id=? AND offset=?", next, time.Now().Unix(), u.ID, u.Offset); e != nil {
		return u, e
	}
	u.Offset = next
	return u, nil
}
func copyContext(ctx context.Context, dst io.Writer, src io.Reader, buffer []byte) (int64, error) {
	var total int64
	for {
		if e := ctx.Err(); e != nil {
			return total, e
		}
		n, e := src.Read(buffer)
		if n > 0 {
			written, err := dst.Write(buffer[:n])
			total += int64(written)
			if err != nil {
				return total, err
			}
			if written != n {
				return total, io.ErrShortWrite
			}
		}
		if e == io.EOF {
			return total, nil
		}
		if e != nil {
			return total, e
		}
	}
}
func (s *Service) Finish(ctx context.Context, a Actor, id string) (Upload, error) {
	lock := s.uploadLock(id)
	lock.Lock()
	defer lock.Unlock()
	u, e := s.Upload(ctx, a, id)
	if e != nil {
		return u, e
	}
	if u.State == "complete" {
		return u, nil
	}
	if u.Offset != u.Size || (u.State != "pending" && u.State != "committing") {
		return u, errors.New("файл ещё не загружен")
	}
	if e = s.Access(ctx, a, u.Path, true); e != nil {
		return u, e
	}
	temporary := ".lanbridge/uploads/" + u.ID + ".part"
	f, e := s.Root.Open(temporary)
	if errors.Is(e, os.ErrNotExist) && u.State == "committing" {
		f, e = s.Root.Open(u.Path)
	}
	if e != nil {
		return u, e
	}
	hash := sha256.New()
	copied, e := copyContext(ctx, hash, f, make([]byte, 64*1024))
	f.Close()
	if e != nil {
		return u, e
	}
	if copied != u.Size || hex.EncodeToString(hash.Sum(nil)) != u.SHA256 {
		return u, errors.New("SHA-256 не совпадает; итоговый файл не опубликован")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.invalidateFolderSizes()
	if u.State == "pending" {
		u.Path, e = s.destination(u.Path, u.Conflict)
		if e != nil {
			return u, e
		}
		if e = s.Access(ctx, a, u.Path, true); e != nil {
			return u, e
		}
		if info, err := s.Root.Lstat(u.Path); err == nil && !info.Mode().IsRegular() {
			return u, ErrConflict
		} else if err != nil && !errors.Is(err, os.ErrNotExist) {
			return u, err
		}
		if _, e = s.DB.ExecContext(ctx, "UPDATE uploads SET path=?,state='committing',updated=? WHERE id=?", u.Path, time.Now().Unix(), u.ID); e != nil {
			return u, e
		}
		u.State = "committing"
	}
	if _, e = s.Root.Lstat(temporary); e == nil {
		if u.Conflict == "replace" {
			e = s.Root.Rename(temporary, u.Path)
		} else {
			e = s.Root.Link(temporary, u.Path)
			if errors.Is(e, os.ErrExist) {
				left, le := s.Root.Lstat(temporary)
				right, re := s.Root.Lstat(u.Path)
				if le == nil && re == nil && os.SameFile(left, right) {
					e = nil
				} else {
					_, _ = s.DB.ExecContext(ctx, "UPDATE uploads SET state='pending' WHERE id=?", u.ID)
					return u, ErrConflict
				}
			}
			if e == nil {
				e = s.Root.Remove(temporary)
			}
		}
		if e != nil {
			return u, e
		}
	} else if !errors.Is(e, os.ErrNotExist) {
		return u, e
	}
	for current := u.Path; current != "."; current = path.Dir(current) {
		if e = s.record(ctx, current, u.Owner); e != nil {
			return u, e
		}
	}
	tx, e := s.DB.BeginTx(ctx, nil)
	if e != nil {
		return u, e
	}
	defer tx.Rollback()
	if _, e = tx.ExecContext(ctx, "UPDATE uploads SET state='complete',updated=? WHERE id=?", time.Now().Unix(), u.ID); e != nil {
		return u, e
	}
	if _, e = tx.ExecContext(ctx, "INSERT INTO transfers(user_id,path,direction,size,started,finished,status) VALUES(?,?,'upload',?,?,?,'complete')", u.Owner, u.Path, u.Size, u.Created, time.Now().Unix()); e != nil {
		return u, e
	}
	if e = tx.Commit(); e != nil {
		return u, e
	}
	u.State = "complete"
	return u, nil
}
func (s *Service) CancelUpload(ctx context.Context, a Actor, id string) error {
	lock := s.uploadLock(id)
	lock.Lock()
	defer lock.Unlock()
	u, e := s.Upload(ctx, a, id)
	if e != nil {
		return e
	}
	if u.State == "complete" || u.State == "committing" {
		return errors.New("публикация уже началась")
	}
	if _, e = s.DB.ExecContext(ctx, "UPDATE uploads SET state='cancelled',updated=? WHERE id=?", time.Now().Unix(), u.ID); e != nil {
		return e
	}
	e = s.Root.Remove(".lanbridge/uploads/" + u.ID + ".part")
	if errors.Is(e, os.ErrNotExist) {
		return nil
	}
	return e
}
