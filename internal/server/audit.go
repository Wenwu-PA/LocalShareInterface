package server

import (
	"context"
	"database/sql"
	"net/http"
	"regexp"
	"strconv"
	"time"
)

var auditAction = regexp.MustCompile(`^[a-zA-Z0-9_.-]{1,64}$`)

type auditEntry struct {
	ID       int64  `json:"id"`
	At       int64  `json:"at"`
	UserID   *int64 `json:"user_id"`
	Username string `json:"username"`
	Action   string `json:"action"`
	Detail   string `json:"detail"`
}

func (s *Server) getAudit(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	q := r.URL.Query()
	limit := int64(50)
	before, user := int64(0), int64(0)
	for key, dest := range map[string]*int64{"limit": &limit, "before": &before, "user_id": &user} {
		if raw := q.Get(key); raw != "" {
			value, err := strconv.ParseInt(raw, 10, 64)
			if err != nil || value < 1 || (key == "limit" && value > 100) {
				problem(w, 400, "filter", "Некорректный фильтр журнала; размер страницы 1–100")
				return
			}
			*dest = value
		}
	}
	action := q.Get("action")
	if action != "" && !auditAction.MatchString(action) {
		problem(w, 400, "action", "Некорректный тип события")
		return
	}
	query := `SELECT a.id,a.created_at,a.user_id,COALESCE(u.username,''),a.action,a.detail FROM audit a LEFT JOIN users u ON u.id=a.user_id WHERE 1=1`
	args := []any{}
	if before > 0 {
		query += " AND a.id<?"
		args = append(args, before)
	}
	if user > 0 {
		query += " AND a.user_id=?"
		args = append(args, user)
	}
	if action != "" {
		query += " AND a.action=?"
		args = append(args, action)
	}
	query += " ORDER BY a.id DESC LIMIT ?"
	args = append(args, limit+1)
	rows, err := s.DB.QueryContext(ctx, query, args...)
	if err != nil {
		s.dbError(w, err)
		return
	}
	defer rows.Close()
	items := []auditEntry{}
	for rows.Next() {
		var item auditEntry
		var id sql.NullInt64
		if err = rows.Scan(&item.ID, &item.At, &id, &item.Username, &item.Action, &item.Detail); err != nil {
			s.dbError(w, err)
			return
		}
		if id.Valid {
			value := id.Int64
			item.UserID = &value
		}
		items = append(items, item)
	}
	if err = rows.Err(); err != nil {
		s.dbError(w, err)
		return
	}
	more := len(items) > int(limit)
	var next int64
	if more {
		items = items[:limit]
		next = items[len(items)-1].ID
	}
	jsonResponse(w, 200, map[string]any{"items": items, "has_more": more, "next_before": next})
}
