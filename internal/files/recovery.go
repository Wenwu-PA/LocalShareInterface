package files

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path"
	"sort"
	"strings"
	"time"
)

type operation struct {
	ID, Source, Target, Kind, ACL string
	Owner                         int64
}

func (s *Service) planOperation(ctx context.Context, source, target, kind string, owner int64, origin ...string) (operation, error) {
	o := operation{ID: ID(), Source: source, Target: target, Kind: kind, Owner: owner}
	original := source
	if len(origin) > 0 {
		original = origin[0]
	}
	var e error
	o.ACL, e = s.snapshotACL(ctx, original)
	if e != nil {
		return o, e
	}
	_, e = s.DB.ExecContext(ctx, "INSERT INTO file_operations VALUES(?,?,?,?,?,?,?)", o.ID, o.Source, o.Target, o.Owner, o.Kind, time.Now().Unix(), o.ACL)
	return o, e
}
func (s *Service) finishOperation(ctx context.Context, o operation) error {
	if o.Kind == "copy" {
		if e := s.walk(ctx, Actor{ID: o.Owner, Admin: true}, o.Target, false, func(name string, _ fs.FileInfo) error { return s.record(ctx, name, o.Owner) }); e != nil {
			return e
		}
	}
	tx, e := s.DB.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if e = applyACL(ctx, tx, o.Target, o.ACL); e != nil {
		return e
	}
	if o.Kind == "move" {
		if _, e = tx.ExecContext(ctx, "DELETE FROM file_acl WHERE path=? OR substr(path,1,length(?)+1)=?||'/'", o.Source, o.Source, o.Source); e != nil {
			return e
		}
		if _, e = tx.ExecContext(ctx, "DELETE FROM file_index WHERE path=? OR substr(path,1,length(?)+1)=?||'/'", o.Target, o.Target, o.Target); e != nil {
			return e
		}
		if _, e = tx.ExecContext(ctx, "UPDATE file_index SET path=?||substr(path,length(?)+1),parent=CASE WHEN path=? THEN ? ELSE ?||substr(parent,length(?)+1) END,name=CASE WHEN path=? THEN ? ELSE name END,name_fold=CASE WHEN path=? THEN ? ELSE name_fold END,seen=? WHERE path=? OR substr(path,1,length(?)+1)=?||'/'", o.Target, o.Source, o.Source, path.Dir(o.Target), o.Target, o.Source, o.Source, path.Base(o.Target), o.Source, strings.ToLower(path.Base(o.Target)), time.Now().UnixNano(), o.Source, o.Source, o.Source); e != nil {
			return e
		}
	}
	if _, e = tx.ExecContext(ctx, "DELETE FROM file_operations WHERE id=?", o.ID); e != nil {
		return e
	}
	return tx.Commit()
}
func (s *Service) Recover(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	rows, e := s.DB.QueryContext(ctx, "SELECT id,source,target,owner_id,kind,acl FROM file_operations ORDER BY created")
	if e != nil {
		return e
	}
	pending := []operation{}
	for rows.Next() {
		var o operation
		if e = rows.Scan(&o.ID, &o.Source, &o.Target, &o.Owner, &o.Kind, &o.ACL); e != nil {
			rows.Close()
			return e
		}
		pending = append(pending, o)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	for _, o := range pending {
		_, fromErr := s.Root.Lstat(o.Source)
		_, toErr := s.Root.Lstat(o.Target)
		if errors.Is(fromErr, os.ErrNotExist) && toErr == nil {
			if e = s.finishOperation(ctx, o); e != nil {
				return e
			}
		} else if fromErr == nil && errors.Is(toErr, os.ErrNotExist) {
			if o.Kind == "copy" {
				if e = s.Root.RemoveAll(o.Source); e != nil {
					return e
				}
			}
			if _, e = s.DB.ExecContext(ctx, "DELETE FROM file_operations WHERE id=?", o.ID); e != nil {
				return e
			}
		} else {
			return errors.New("неоднозначная незавершённая операция: требуется проверка файлов администратором")
		}
	}
	return s.recoverTrash(ctx)
}

// Store the effective root permission and explicit descendants before moving data.
func (s *Service) snapshotACL(ctx context.Context, name string) (string, error) {
	var root string
	if e := s.DB.QueryRowContext(ctx, "SELECT access FROM file_acl WHERE path='.' OR path=? OR substr(?,1,length(path)+1)=path||'/' ORDER BY length(path) DESC LIMIT 1", name, name).Scan(&root); e != nil {
		return "", e
	}
	values := map[string]string{"": root}
	rows, e := s.DB.QueryContext(ctx, "SELECT path,access FROM file_acl WHERE substr(path,1,length(?)+1)=?||'/'", name, name)
	if e != nil {
		return "", e
	}
	defer rows.Close()
	for rows.Next() {
		var p, a string
		if e = rows.Scan(&p, &a); e != nil {
			return "", e
		}
		values[strings.TrimPrefix(p, name)] = a
	}
	if e = rows.Err(); e != nil {
		return "", e
	}
	data, e := json.Marshal(values)
	return string(data), e
}
func applyACL(ctx context.Context, tx *sql.Tx, target, data string) error {
	values := map[string]string{}
	if e := json.Unmarshal([]byte(data), &values); e != nil {
		return e
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool { return len(keys[i]) < len(keys[j]) })
	for _, suffix := range keys {
		access := values[suffix]
		name := target + suffix
		// A destination's inherited restriction must never be weakened by the copy.
		var inherited string
		if e := tx.QueryRowContext(ctx, "SELECT access FROM file_acl WHERE path='.' OR path=? OR substr(?,1,length(path)+1)=path||'/' ORDER BY length(path) DESC LIMIT 1", name, name).Scan(&inherited); e != nil {
			return e
		}
		if inherited == "admin" || access == "admin" {
			access = "admin"
		} else if inherited == "read" || access == "read" {
			access = "read"
		}
		if _, e := tx.ExecContext(ctx, "INSERT INTO file_acl VALUES(?,?) ON CONFLICT(path) DO UPDATE SET access=excluded.access", name, access); e != nil {
			return e
		}
	}
	return nil
}
