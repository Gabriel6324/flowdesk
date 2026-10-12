package service

import (
	"container/list"
	"context"
	"database/sql"
	"errors"
	"flowdesk/internal/auth"
	"flowdesk/internal/domain"
	"flowdesk/internal/repository"
	"sync"
	"time"
)

type limitEntry struct {
	key   string
	start time.Time
	count int
}
type Authenticator struct {
	store       *repository.Store
	active      chan struct{}
	waiting     chan struct{}
	mu          sync.Mutex
	entries     map[string]*list.Element
	lru         *list.List
	dummy       []byte
	AfterVerify func() // Deterministic concurrency seam for tests; never exposed by HTTP or flags.
}

func NewAuthenticator(s *repository.Store) *Authenticator {
	d, _ := auth.Derive("unavailable-password", make([]byte, 16), auth.Iterations)
	return &Authenticator{store: s, active: make(chan struct{}, 2), waiting: make(chan struct{}, 8), entries: map[string]*list.Element{}, lru: list.New(), dummy: d}
}
func (a *Authenticator) Allow(key string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	now := time.Now()
	el := a.entries[key]
	if el == nil {
		if len(a.entries) >= 4096 {
			old := a.lru.Back()
			delete(a.entries, old.Value.(*limitEntry).key)
			a.lru.Remove(old)
		}
		el = a.lru.PushFront(&limitEntry{key: key, start: now})
		a.entries[key] = el
	}
	a.lru.MoveToFront(el)
	v := el.Value.(*limitEntry)
	if now.Sub(v.start) > time.Minute {
		v.start = now
		v.count = 0
	}
	v.count++
	return v.count <= 10
}
func (a *Authenticator) Acquire(ctx context.Context) (func(), error) {
	select {
	case a.active <- struct{}{}:
		return func() { <-a.active }, nil
	default:
	}
	select {
	case a.waiting <- struct{}{}:
	default:
		return nil, domain.E("LOGIN_RATE_LIMITED")
	}
	defer func() { <-a.waiting }()
	timer := time.NewTimer(250 * time.Millisecond)
	defer timer.Stop()
	select {
	case a.active <- struct{}{}:
		return func() { <-a.active }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-timer.C:
		return nil, domain.E("LOGIN_RATE_LIMITED")
	}
}
func (s *Service) Login(ctx context.Context, email, password, ip string) (domain.Me, string, error) {
	var me domain.Me
	normalized, e := auth.Email(email)
	if e != nil || len(password) > 512 {
		normalized = "invalid@invalid.invalid"
	}
	if !s.Auth.Allow("ip:"+ip) || !s.Auth.Allow("email:"+normalized) {
		return me, "", domain.E("LOGIN_RATE_LIMITED")
	}
	release, e := s.Auth.Acquire(ctx)
	if e != nil {
		return me, "", e
	}
	defer release()
	v, e := repository.Credentials(ctx, s.Store.DB, normalized)
	known := e == nil
	if e != nil && !errors.Is(e, sql.ErrNoRows) {
		return me, "", DBError(e)
	}
	if !known {
		v.Hash = s.Auth.dummy
		v.Salt = make([]byte, 16)
		v.Iterations = auth.Iterations
	}
	valid := auth.Verify(password, v.Hash, v.Salt, v.Iterations)
	if !known || !valid || v.User.Disabled {
		return me, "", domain.E("LOGIN_FAILED")
	}
	if s.Auth.AfterVerify != nil {
		s.Auth.AfterVerify()
	}
	token := auth.RandomHex(32)
	hash := auth.TokenHash(token)
	e = s.Store.Tx(ctx, func(tx *sql.Tx) error {
		current, e := repository.User(ctx, tx, v.User.ID)
		if e != nil {
			return e
		}
		if current.Disabled || current.CredentialsVersion != v.User.CredentialsVersion || current.AuthorizationVersion != v.User.AuthorizationVersion {
			return domain.E("LOGIN_FAILED")
		}
		if _, e = tx.ExecContext(ctx, "DELETE FROM sessions WHERE expires_at<=?", repository.Now()); e != nil {
			return e
		}
		_, e = tx.ExecContext(ctx, "INSERT INTO sessions VALUES(?,?,?,?,?)", hash, current.ID, current.CredentialsVersion, current.AuthorizationVersion, time.Now().UTC().Add(12*time.Hour).Format(time.RFC3339Nano))
		return e
	})
	if e != nil {
		return me, "", DBError(e)
	}
	me = domain.Me{User: v.User, WorkspaceID: s.Store.WorkspaceID, Profile: s.Config.Profile}
	return me, token, nil
}
func (s *Service) Logout(ctx context.Context, a domain.Actor) error {
	_, e := s.Store.DB.ExecContext(ctx, "DELETE FROM sessions WHERE token_hash=?", a.TokenHash)
	return DBError(e)
}

type PasswordBody struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
}

func (s *Service) ChangePassword(ctx context.Context, a domain.Actor, b PasswordBody) error {
	if !auth.PasswordValid(b.NewPassword) {
		return domain.E("VALIDATION_ERROR")
	}
	release, e := s.Auth.Acquire(ctx)
	if e != nil {
		return e
	}
	defer release()
	v, e := repository.Credentials(ctx, s.Store.DB, a.Email)
	if e != nil {
		return DBError(e)
	}
	if !auth.Verify(b.CurrentPassword, v.Hash, v.Salt, v.Iterations) {
		return domain.E("LOGIN_FAILED")
	}
	hash, salt, e := auth.Hash(b.NewPassword)
	if e != nil {
		return e
	}
	e = s.Store.Tx(ctx, func(tx *sql.Tx) error {
		u, e := repository.Session(ctx, tx, a.TokenHash)
		if e != nil {
			return e
		}
		if u.CredentialsVersion != v.User.CredentialsVersion {
			return domain.E("VERSION_CONFLICT")
		}
		if _, e = tx.ExecContext(ctx, "UPDATE users SET password_hash=?,password_salt=?,password_iterations=?,credentials_version=credentials_version+1,revision=revision+1 WHERE id=?", hash, salt, auth.Iterations, u.ID); e != nil {
			return e
		}
		_, e = tx.ExecContext(ctx, "DELETE FROM sessions WHERE user_id=?", u.ID)
		return e
	})
	return DBError(e)
}
