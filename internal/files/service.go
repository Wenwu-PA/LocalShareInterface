package files

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path"
	"strings"
	"sync"
	"time"
)

type Actor struct {
	ID    int64
	Admin bool
}
type Service struct {
	Root        *os.Root
	DB          *sql.DB
	Log         *slog.Logger
	mu          sync.Mutex
	indexMu     sync.Mutex
	cancel      context.CancelFunc
	wg          sync.WaitGroup
	uploadLocks [64]sync.Mutex
}
type Entry struct {
	Path      string `json:"path"`
	Name      string `json:"name"`
	Directory bool   `json:"directory"`
	Size      int64  `json:"size"`
	Modified  int64  `json:"modified"`
	Access    string `json:"access"`
}
type Settings struct {
	GlobalLimit  int64 `json:"global_limit"`
	UserLimit    int64 `json:"user_limit"`
	CriticalFree int64 `json:"critical_free"`
	TrashDays    int   `json:"trash_days"`
	TrashLimit   int64 `json:"trash_limit"`
	PartialHours int   `json:"partial_hours"`
}

func New(root string, db *sql.DB, log *slog.Logger) (*Service, error) {
	if e := os.MkdirAll(root, 0700); e != nil {
		return nil, e
	}
	r, e := os.OpenRoot(root)
	if e != nil {
		return nil, e
	}
	for _, dir := range []string{".lanbridge", ".lanbridge/uploads", ".lanbridge/trash"} {
		if e = r.MkdirAll(dir, 0700); e != nil {
			r.Close()
			return nil, e
		}
		info, err := r.Lstat(dir)
		if err != nil || !info.IsDir() || info.Mode()&fs.ModeSymlink != 0 {
			r.Close()
			return nil, ErrAccess
		}
	}
	return &Service{Root: r, DB: db, Log: log}, nil
}
func ID() string {
	b := make([]byte, 16)
	if _, e := rand.Read(b); e != nil {
		panic(e)
	}
	return hex.EncodeToString(b)
}
func (s *Service) Space() (uint64, uint64, error) { return freeSpace(s.Root.Name()) }
func (s *Service) Close() {
	if s.cancel != nil {
		s.cancel()
	}
	s.wg.Wait()
	s.Root.Close()
}
func (s *Service) Access(ctx context.Context, a Actor, name string, write bool) error {
	if a.Admin {
		return nil
	}
	var pending int
	if e := s.DB.QueryRowContext(ctx, "SELECT (SELECT COUNT(*) FROM trash WHERE state='restoring' AND (path=? OR substr(?,1,length(path)+1)=path||'/')) + COUNT(*) FROM file_operations WHERE target=? OR substr(?,1,length(target)+1)=target||'/' OR (kind='move' AND (source=? OR substr(?,1,length(source)+1)=source||'/'))", name, name, name, name, name, name).Scan(&pending); e != nil {
		return e
	}
	if pending > 0 {
		return ErrAccess
	}
	var access string
	e := s.DB.QueryRowContext(ctx, "SELECT access FROM file_acl WHERE path='.' OR path=? OR substr(?,1,length(path)+1)=path||'/' ORDER BY length(path) DESC LIMIT 1", name, name).Scan(&access)
	if e != nil {
		return e
	}
	if access == "admin" || write && access == "read" {
		return ErrAccess
	}
	return nil
}
func (s *Service) check(ctx context.Context, a Actor, raw string, write bool) (string, error) {
	name, e := Clean(raw)
	if e != nil {
		return "", e
	}
	if e = s.Access(ctx, a, name, write); e != nil {
		return "", e
	}
	for current := name; current != "."; current = path.Dir(current) {
		info, err := s.Root.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return "", err
		}
		if info.Mode()&fs.ModeSymlink != 0 {
			return "", ErrAccess
		}
		// Windows accepts alternate casing and short aliases; ACL keys use the
		// exact indexed spelling, so aliases must not bypass a protected ancestor.
		exact, err := s.exactSpelling(current)
		if err != nil {
			return "", err
		}
		if exact != path.Base(current) {
			if current == name && write && exact != "" {
				return s.check(ctx, a, path.Join(path.Dir(name), exact), write)
			}
			return "", ErrAccess
		}
	}
	return name, nil
}
func (s *Service) exactSpelling(name string) (string, error) {
	f, e := s.Root.Open(path.Dir(name))
	if e != nil {
		return "", e
	}
	defer f.Close()
	alias := ""
	for {
		entries, e := f.ReadDir(256)
		for _, entry := range entries {
			if strings.EqualFold(entry.Name(), path.Base(name)) {
				alias = entry.Name()
			}
			if entry.Name() == path.Base(name) {
				return entry.Name(), nil
			}
		}
		if e == io.EOF {
			return alias, nil
		}
		if e != nil {
			return "", e
		}
	}
}
func (s *Service) SetAccess(ctx context.Context, a Actor, raw, access string) error {
	if !a.Admin {
		return ErrAccess
	}
	if access != "admin" && access != "users" && access != "read" {
		return errors.New("доступ: admin, users или read")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	name, e := s.check(ctx, a, raw, true)
	if e != nil {
		return e
	}
	if _, e = s.Root.Lstat(name); e != nil {
		return e
	}
	_, e = s.DB.ExecContext(ctx, "INSERT INTO file_acl(path,access) VALUES(?,?) ON CONFLICT(path) DO UPDATE SET access=excluded.access", name, access)
	return e
}
func (s *Service) Settings(ctx context.Context) (Settings, error) {
	var c Settings
	e := s.DB.QueryRowContext(ctx, "SELECT global_limit,user_limit,critical_free,trash_days,trash_limit,partial_hours FROM file_settings WHERE id=1").Scan(&c.GlobalLimit, &c.UserLimit, &c.CriticalFree, &c.TrashDays, &c.TrashLimit, &c.PartialHours)
	return c, e
}
func (s *Service) Start(parent context.Context) {
	ctx, cancel := context.WithCancel(parent)
	s.cancel = cancel
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		run := func() {
			defer func() {
				if recover() != nil {
					s.Log.Error("file maintenance panic; retry scheduled")
				}
			}()
			if e := s.Recover(ctx); e != nil && ctx.Err() == nil {
				s.Log.Error("file recovery failed", "error", e)
				return
			}
			if e := s.Index(ctx); e != nil && ctx.Err() == nil {
				s.Log.Error("file index failed", "error", e)
			}
			if e := s.Maintenance(ctx); e != nil && ctx.Err() == nil {
				s.Log.Error("file maintenance failed", "error", e)
			}
		}
		run()
		tick := time.NewTicker(30 * time.Second)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
				run()
			}
		}
	}()
}
func (s *Service) Index(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.indexMu.Lock()
	defer s.indexMu.Unlock()
	generation := time.Now().UnixNano()
	batch := []Entry{}
	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		tx, e := s.DB.BeginTx(ctx, nil)
		if e != nil {
			return e
		}
		defer tx.Rollback()
		for _, entry := range batch {
			if _, e = tx.ExecContext(ctx, `INSERT INTO file_index(path,name,name_fold,parent,is_dir,size,modified,seen) VALUES(?,?,?,?,?,?,?,?) ON CONFLICT(path) DO UPDATE SET name=excluded.name,name_fold=excluded.name_fold,parent=excluded.parent,is_dir=excluded.is_dir,size=excluded.size,modified=excluded.modified,seen=excluded.seen WHERE file_index.seen<=excluded.seen`, entry.Path, entry.Name, strings.ToLower(entry.Name), path.Dir(entry.Path), entry.Directory, entry.Size, entry.Modified, generation); e != nil {
				return e
			}
		}
		if e = tx.Commit(); e != nil {
			return e
		}
		batch = batch[:0]
		return nil
	}
	e := fs.WalkDir(s.Root.FS(), ".", func(name string, entry fs.DirEntry, walkErr error) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if walkErr != nil {
			return walkErr
		}
		if name == "." {
			return nil
		}
		if strings.EqualFold(entry.Name(), ".lanbridge") {
			if entry.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if entry.Type()&fs.ModeSymlink != 0 {
			return nil
		}
		clean, e := Clean(name)
		if e != nil || clean != name {
			if entry.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		info, e := entry.Info()
		if e != nil {
			return e
		}
		if !info.IsDir() && !info.Mode().IsRegular() {
			return nil
		}
		size := info.Size()
		if info.IsDir() {
			size = 0
		}
		batch = append(batch, Entry{Path: name, Name: entry.Name(), Directory: info.IsDir(), Size: size, Modified: info.ModTime().UnixNano()})
		if len(batch) >= 128 {
			return flush()
		}
		return nil
	})
	if e != nil {
		return e
	}
	if e = flush(); e != nil {
		return e
	}
	_, e = s.DB.ExecContext(ctx, "DELETE FROM file_index WHERE seen<?", generation)
	return e
}
func (s *Service) record(ctx context.Context, name string, owner int64) error {
	info, e := s.Root.Stat(name)
	if e != nil {
		return e
	}
	size := info.Size()
	if info.IsDir() {
		size = 0
	}
	_, e = s.DB.ExecContext(ctx, `INSERT INTO file_index(path,name,name_fold,parent,is_dir,size,modified,owner_id,seen) VALUES(?,?,?,?,?,?,?,?,?) ON CONFLICT(path) DO UPDATE SET size=excluded.size,modified=excluded.modified,owner_id=CASE WHEN ?>0 THEN excluded.owner_id ELSE file_index.owner_id END,seen=excluded.seen`, name, path.Base(name), strings.ToLower(path.Base(name)), path.Dir(name), info.IsDir(), size, info.ModTime().UnixNano(), owner, time.Now().UnixNano(), owner)
	return e
}
func (s *Service) List(ctx context.Context, a Actor, raw, q, sort, order string, offset, limit int) ([]Entry, error) {
	return s.list(ctx, a, raw, q, sort, order, offset, limit, false)
}
func (s *Service) Folders(ctx context.Context, a Actor, raw string, offset, limit int) ([]Entry, error) {
	return s.list(ctx, a, raw, "", "name", "asc", offset, limit, true)
}
func (s *Service) list(ctx context.Context, a Actor, raw, q, sort, order string, offset, limit int, foldersOnly bool) ([]Entry, error) {
	name, e := s.check(ctx, a, raw, false)
	if e != nil {
		return nil, e
	}
	if limit < 1 || limit > 500 {
		limit = 100
	}
	if offset < 0 {
		offset = 0
	}
	columns := map[string]string{"name": "name_fold", "size": "size", "modified": "modified"}
	column := columns[sort]
	if column == "" {
		column = columns["name"]
	}
	direction := "ASC"
	if order == "desc" {
		direction = "DESC"
	}
	query := "SELECT path,name,is_dir,size,modified FROM file_index WHERE parent=?"
	args := []any{name}
	if q != "" {
		query = "SELECT path,name,is_dir,size,modified FROM file_index WHERE instr(name_fold,?)>0"
		args = []any{strings.ToLower(q)}
	}
	if foldersOnly {
		query += " AND is_dir=1"
	}
	// Apply authorization before pagination so hidden entries cannot consume a page.
	if !a.Admin {
		query += ` AND (SELECT access FROM file_acl WHERE file_acl.path='.' OR file_acl.path=file_index.path OR substr(file_index.path,1,length(file_acl.path)+1)=file_acl.path||'/' ORDER BY length(file_acl.path) DESC LIMIT 1)!='admin'
 AND NOT EXISTS(SELECT 1 FROM trash t WHERE t.state='restoring' AND (t.path=file_index.path OR substr(file_index.path,1,length(t.path)+1)=t.path||'/'))
 AND NOT EXISTS(SELECT 1 FROM file_operations o WHERE o.target=file_index.path OR substr(file_index.path,1,length(o.target)+1)=o.target||'/' OR (o.kind='move' AND (o.source=file_index.path OR substr(file_index.path,1,length(o.source)+1)=o.source||'/')))`
	}
	query += " ORDER BY is_dir DESC," + column + " " + direction + ",path LIMIT ? OFFSET ?"
	args = append(args, limit, offset)
	rows, e := s.DB.QueryContext(ctx, query, args...)
	if e != nil {
		return nil, e
	}
	entries := []Entry{}
	for rows.Next() {
		var entry Entry
		if e = rows.Scan(&entry.Path, &entry.Name, &entry.Directory, &entry.Size, &entry.Modified); e != nil {
			rows.Close()
			return nil, e
		}
		entries = append(entries, entry)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return nil, e
	}
	visible := []Entry{}
	for _, entry := range entries {
		if e = s.Access(ctx, a, entry.Path, false); errors.Is(e, ErrAccess) {
			continue
		} else if e != nil {
			return nil, e
		}
		entry.Access = "users"
		if e = s.DB.QueryRowContext(ctx, "SELECT access FROM file_acl WHERE path='.' OR path=? OR substr(?,1,length(path)+1)=path||'/' ORDER BY length(path) DESC LIMIT 1", entry.Path, entry.Path).Scan(&entry.Access); e != nil {
			return nil, e
		}
		visible = append(visible, entry)
	}
	return visible, nil
}
func (s *Service) Mkdir(ctx context.Context, a Actor, raw string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	name, e := s.check(ctx, a, raw, true)
	if e != nil {
		return e
	}
	if name == "." {
		return ErrConflict
	}
	if e = s.Root.MkdirAll(name, 0700); e != nil {
		return e
	}
	for current := name; current != "."; current = path.Dir(current) {
		if e = s.record(ctx, current, a.ID); e != nil {
			return e
		}
	}
	return nil
}
func (s *Service) Open(ctx context.Context, a Actor, raw string) (*os.File, error) {
	name, e := s.check(ctx, a, raw, false)
	if e != nil {
		return nil, e
	}
	info, e := s.Root.Lstat(name)
	if e != nil {
		return nil, e
	}
	if !info.Mode().IsRegular() {
		return nil, ErrAccess
	}
	return s.Root.Open(name)
}
