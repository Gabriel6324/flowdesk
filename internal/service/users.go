package service

import (
	"context"
	"database/sql"
	"errors"
	"flowdesk/internal/auth"
	"flowdesk/internal/domain"
	"flowdesk/internal/repository"
	"strings"
)

type UserCreate struct {
	Email       string `json:"email"`
	DisplayName string `json:"display_name"`
	Role        string `json:"role"`
	Password    string `json:"password"`
}
type UserPatch struct {
	ExpectedRevision int64  `json:"expected_revision"`
	DisplayName      string `json:"display_name"`
	Role             string `json:"role"`
	Disabled         bool   `json:"disabled"`
}

func (s *Service) Users(ctx context.Context, a domain.Actor) ([]domain.User, error) {
	if e := Admin(a); e != nil {
		return nil, e
	}
	rows, e := s.Store.DB.QueryContext(ctx, "SELECT id,email,display_name,role,disabled,revision,authorization_version,credentials_version FROM users ORDER BY id LIMIT 501")
	if e != nil {
		return nil, DBError(e)
	}
	defer rows.Close()
	out := []domain.User{}
	for rows.Next() {
		u, e := repository.ScanUser(rows)
		if e != nil {
			return nil, DBError(e)
		}
		out = append(out, u)
	}
	if len(out) > 500 {
		return nil, domain.E("EXPORT_LIMIT")
	}
	return out, DBError(rows.Err())
}
func (s *Service) CreateUser(ctx context.Context, a domain.Actor, key string, b UserCreate) (Result, error) {
	if e := Admin(a); e != nil {
		return Result{}, e
	}
	email, e := auth.Email(b.Email)
	if e != nil || (s.Config.Workspace && domain.Enum(email, "admin@flowdesk.demo", "agent@flowdesk.demo", "customer@flowdesk.demo")) || !Text(b.DisplayName, 100) || !auth.PasswordValid(b.Password) || !domain.Enum(b.Role, "admin", "agent", "requester") {
		return Result{}, domain.E("VALIDATION_ERROR")
	}
	release, e := s.Auth.Acquire(ctx)
	if e != nil {
		return Result{}, e
	}
	defer release()
	hash, salt, e := auth.Hash(b.Password)
	if e != nil {
		return Result{}, e
	}
	return s.mutate(ctx, a, "POST /api/users", key, b, adminScope, func(ctx context.Context, tx *sql.Tx, a domain.Actor) (any, int, error) {
		var count int
		if e := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM users").Scan(&count); e != nil {
			return nil, 0, e
		}
		if count >= 500 {
			return nil, 0, domain.E("USER_LIMIT")
		}
		r, e := tx.ExecContext(ctx, "INSERT INTO users(email,display_name,role,password_hash,password_salt,password_iterations,created_at) VALUES(?,?,?,?,?,?,?)", email, b.DisplayName, b.Role, hash, salt, auth.Iterations, repository.Now())
		if e != nil {
			if strings.Contains(e.Error(), "UNIQUE") {
				return nil, 0, domain.E("EMAIL_EXISTS")
			}
			return nil, 0, e
		}
		id, _ := r.LastInsertId()
		u, e := repository.User(ctx, tx, repository.ID(id))
		if e != nil {
			return nil, 0, e
		}
		e = s.audit(ctx, tx, a, nil, "user_created", map[string]any{"user_id": u.ID, "role": u.Role})
		return u, 201, e
	})
}
func (s *Service) PatchUser(ctx context.Context, a domain.Actor, id, key string, b UserPatch) (Result, error) {
	scope := func(ctx context.Context, q repository.Executor, a domain.Actor) error {
		if e := Admin(a); e != nil {
			return e
		}
		_, e := repository.User(ctx, q, id)
		if errors.Is(e, sql.ErrNoRows) {
			return domain.E("NOT_FOUND")
		}
		return e
	}
	out, e := s.mutate(ctx, a, "PATCH /api/users/"+id, key, b, scope, func(ctx context.Context, tx *sql.Tx, a domain.Actor) (any, int, error) {
		if !Text(b.DisplayName, 100) || !domain.Enum(b.Role, "admin", "agent", "requester") {
			return nil, 0, domain.E("VALIDATION_ERROR")
		}
		u, e := repository.User(ctx, tx, id)
		if e != nil {
			return nil, 0, e
		}
		if u.Revision != b.ExpectedRevision {
			return nil, 0, domain.E("VERSION_CONFLICT")
		}
		if id == a.ID && b.Disabled {
			return nil, 0, domain.E("SELF_DISABLE_FORBIDDEN")
		}
		authChanged := u.Role != b.Role || u.Disabled != b.Disabled
		if u.Role == "admin" && !u.Disabled && (b.Role != "admin" || b.Disabled) {
			var n int
			if e = tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM users WHERE role='admin' AND disabled=0").Scan(&n); e != nil {
				return nil, 0, e
			}
			if n <= 1 {
				return nil, 0, domain.E("LAST_ADMIN")
			}
		}
		if _, e = tx.ExecContext(ctx, "UPDATE users SET display_name=?,role=?,disabled=?,revision=revision+1,authorization_version=authorization_version+? WHERE id=?", b.DisplayName, b.Role, b.Disabled, boolInt(authChanged), id); e != nil {
			return nil, 0, e
		}
		if authChanged {
			if _, e = tx.ExecContext(ctx, "DELETE FROM sessions WHERE user_id=?", id); e != nil {
				return nil, 0, e
			}
			if _, e = tx.ExecContext(ctx, "UPDATE jobs SET status='failed',error_code='ACTOR_PERMISSION_CHANGED',error_stage='authorization',encrypted_secret=NULL,finished_at=? WHERE actor_id=? AND status IN ('queued','running')", repository.Now(), id); e != nil {
				return nil, 0, e
			}
			if e = repository.ClearTerminatedActive(ctx, tx); e != nil {
				return nil, 0, e
			}
		}
		if e = s.audit(ctx, tx, a, nil, "user_updated", map[string]any{"user_id": id, "role": b.Role, "disabled": b.Disabled, "authorization_changed": authChanged}); e != nil {
			return nil, 0, e
		}
		u, e = repository.User(ctx, tx, id)
		return u, 200, e
	})
	if e == nil && s.CancelActor != nil {
		s.CancelActor(id)
	}
	return out, e
}
func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}
