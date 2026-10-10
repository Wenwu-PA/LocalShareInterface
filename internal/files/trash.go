package files

import (
	"context"
	"errors"
	"io/fs"
	"os"
)

func (s *Service) finishTrash(ctx context.Context, id, name string) error {
	tx, e := s.DB.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if _, e = tx.ExecContext(ctx, "UPDATE trash SET state='ready' WHERE id=?", id); e != nil {
		return e
	}
	if _, e = tx.ExecContext(ctx, "DELETE FROM file_index WHERE path=? OR substr(path,1,length(?)+1)=?||'/'", name, name, name); e != nil {
		return e
	}
	// Retain old ACLs conservatively; the trash snapshot follows restored names.
	return tx.Commit()
}
func (s *Service) finishRestore(ctx context.Context, id, name, acl string, owner int64) error {
	if e := s.walk(ctx, Actor{ID: owner, Admin: true}, name, false, func(current string, _ fs.FileInfo) error { return s.record(ctx, current, owner) }); e != nil {
		return e
	}
	tx, e := s.DB.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if e = applyACL(ctx, tx, name, acl); e != nil {
		return e
	}
	if _, e = tx.ExecContext(ctx, "DELETE FROM trash WHERE id=?", id); e != nil {
		return e
	}
	return tx.Commit()
}
func (s *Service) recoverTrash(ctx context.Context) error {
	type item struct {
		id, name, state, acl string
		owner                int64
	}
	rows, e := s.DB.QueryContext(ctx, "SELECT id,path,state,acl,owner_id FROM trash WHERE state!='ready'")
	if e != nil {
		return e
	}
	pending := []item{}
	for rows.Next() {
		var x item
		if e = rows.Scan(&x.id, &x.name, &x.state, &x.acl, &x.owner); e != nil {
			rows.Close()
			return e
		}
		pending = append(pending, x)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	for _, x := range pending {
		if x.state == "purging" {
			if e = s.purgeTrash(ctx, x.id); e != nil {
				return e
			}
			continue
		}
		_, hidden := s.Root.Lstat(".lanbridge/trash/" + x.id)
		_, visible := s.Root.Lstat(x.name)
		switch {
		case x.state == "moving" && hidden == nil:
			e = s.finishTrash(ctx, x.id, x.name)
		case x.state == "moving" && errors.Is(hidden, os.ErrNotExist) && visible == nil:
			_, e = s.DB.ExecContext(ctx, "DELETE FROM trash WHERE id=?", x.id)
		case x.state == "restoring" && hidden == nil:
			_, e = s.DB.ExecContext(ctx, "UPDATE trash SET state='ready' WHERE id=?", x.id)
		case x.state == "restoring" && errors.Is(hidden, os.ErrNotExist) && visible == nil:
			e = s.finishRestore(ctx, x.id, x.name, x.acl, x.owner)
		default:
			return errors.New("неоднозначное состояние корзины: требуется проверка администратором")
		}
		if e != nil {
			return e
		}
	}
	return nil
}
