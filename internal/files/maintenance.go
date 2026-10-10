package files

import (
	"context"
	"errors"
	"os"
	"time"
)

// Maintenance retries publication and expires only database-owned temporary data.
func (s *Service) Maintenance(ctx context.Context) error {
	settings, e := s.Settings(ctx)
	if e != nil {
		return e
	}
	cutoff := time.Now().Add(-time.Duration(settings.PartialHours) * time.Hour).Unix()
	rows, e := s.DB.QueryContext(ctx, "SELECT id FROM uploads WHERE state='committing' OR state='cancelled' OR (state='pending' AND updated<?)", cutoff)
	if e != nil {
		return e
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if e = rows.Scan(&id); e != nil {
			rows.Close()
			return e
		}
		ids = append(ids, id)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	for _, id := range ids {
		lock := s.uploadLock(id)
		lock.Lock()
		u, err := s.Upload(ctx, Actor{Admin: true}, id)
		if err != nil {
			lock.Unlock()
			return err
		}
		if u.State == "committing" {
			lock.Unlock()
			if _, err = s.Finish(ctx, Actor{ID: u.Owner, Admin: true}, id); err != nil {
				s.Log.Warn("upload recovery pending", "id", id, "error", err)
			}
			continue
		}
		if u.State == "cancelled" || u.State == "pending" && u.Updated < cutoff {
			_, err = s.DB.ExecContext(ctx, "UPDATE uploads SET state='cancelled' WHERE id=?", id)
			if err == nil {
				err = s.Root.Remove(".lanbridge/uploads/" + id + ".part")
				if errors.Is(err, os.ErrNotExist) {
					err = nil
				}
			}
			if err == nil {
				_, err = s.DB.ExecContext(ctx, "DELETE FROM uploads WHERE id=?", id)
			}
		}
		lock.Unlock()
		if err != nil {
			return err
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	type item struct {
		id            string
		size, created int64
	}
	rows, e = s.DB.QueryContext(ctx, "SELECT id,size,created FROM trash WHERE state='ready' ORDER BY created,id")
	if e != nil {
		return e
	}
	items := []item{}
	var total int64
	for rows.Next() {
		var x item
		if e = rows.Scan(&x.id, &x.size, &x.created); e != nil {
			rows.Close()
			return e
		}
		items = append(items, x)
		total += x.size
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	expires := time.Now().Add(-time.Duration(settings.TrashDays) * 24 * time.Hour).Unix()
	for _, x := range items {
		if x.created >= expires && (settings.TrashLimit == 0 || total <= settings.TrashLimit) {
			continue
		}
		if _, e = s.DB.ExecContext(ctx, "UPDATE trash SET state='purging' WHERE id=?", x.id); e != nil {
			return e
		}
		if e = s.purgeTrash(ctx, x.id); e != nil {
			return e
		}
		total -= x.size
	}
	return nil
}
func (s *Service) purgeTrash(ctx context.Context, id string) error {
	if e := s.Root.RemoveAll(".lanbridge/trash/" + id); e != nil {
		return e
	}
	_, e := s.DB.ExecContext(ctx, "DELETE FROM trash WHERE id=?", id)
	return e
}
