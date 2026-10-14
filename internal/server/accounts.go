package server

import (
	"database/sql"
	"net/http"
	"strconv"
	"time"
)

func (s *Server) updateAccount(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		problem(w, 400, "id", "Некорректный пользователь")
		return
	}
	var body struct {
		Password        *string `json:"password"`
		Role            *string `json:"role"`
		CurrentPassword string  `json:"current_password"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	if body.Password == nil && body.Role == nil {
		problem(w, 400, "change", "Нет изменений")
		return
	}
	if len(body.CurrentPassword) > 256 || (body.Password != nil && (len(*body.Password) < 12 || len(*body.Password) > 256)) || (body.Role != nil && *body.Role != "admin" && *body.Role != "user") {
		problem(w, 400, "credentials", "Пароль: 12–256 байт; роль: admin или user")
		return
	}
	if !s.enterAuth(w) {
		return
	}
	defer func() { <-s.authGate }()
	tx, err := s.DB.BeginTx(r.Context(), nil)
	if err != nil {
		s.dbError(w, err)
		return
	}
	defer tx.Rollback()
	actor := r.Context().Value(identityKey{}).(User)
	var hash, role string
	if err = tx.QueryRowContext(r.Context(), "SELECT password_hash,role FROM users WHERE id=?", actor.ID).Scan(&hash, &role); err != nil {
		s.dbError(w, err)
		return
	}
	if role != "admin" && (id != actor.ID || body.Role != nil) {
		problem(w, 403, "role", "Изменять роли и других пользователей может только администратор")
		return
	}
	if !checkPassword(body.CurrentPassword, hash) {
		problem(w, 403, "password", "Текущий пароль неверен")
		return
	}
	var target User
	if err = tx.QueryRowContext(r.Context(), "SELECT id,username,role FROM users WHERE id=?", id).Scan(&target.ID, &target.Username, &target.Role); err == sql.ErrNoRows {
		problem(w, 404, "user", "Пользователь не найден")
		return
	} else if err != nil {
		s.dbError(w, err)
		return
	}
	if body.Role != nil && *body.Role != target.Role {
		if target.Role == "admin" {
			var count int
			if err = tx.QueryRowContext(r.Context(), "SELECT COUNT(*) FROM users WHERE role='admin'").Scan(&count); err != nil {
				s.dbError(w, err)
				return
			}
			if count <= 1 {
				problem(w, 409, "last_admin", "Нельзя понизить роль последнего администратора")
				return
			}
		}
		target.Role = *body.Role
		if _, err = tx.ExecContext(r.Context(), "UPDATE users SET role=? WHERE id=?", target.Role, id); err != nil {
			s.dbError(w, err)
			return
		}
	}
	if body.Password != nil {
		if _, err = tx.ExecContext(r.Context(), "UPDATE users SET password_hash=? WHERE id=?", passwordHash(*body.Password), id); err != nil {
			s.dbError(w, err)
			return
		}
	}
	if _, err = tx.ExecContext(r.Context(), "DELETE FROM sessions WHERE user_id=?", id); err != nil {
		s.dbError(w, err)
		return
	}
	if _, err = tx.ExecContext(r.Context(), "UPDATE shares SET revoked=1 WHERE owner_id=?", id); err != nil {
		s.dbError(w, err)
		return
	}
	if _, err = tx.ExecContext(r.Context(), "INSERT INTO audit(created_at,user_id,action,detail) VALUES(?,?,'account_updated',?)", time.Now().Unix(), actor.ID, strconv.FormatInt(id, 10)); err != nil {
		s.dbError(w, err)
		return
	}
	if err = tx.Commit(); err != nil {
		s.dbError(w, err)
		return
	}
	s.CloseStreams()
	jsonResponse(w, 200, target)
}
