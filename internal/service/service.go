package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"flowdesk/internal/domain"
	"flowdesk/internal/platform"
	"flowdesk/internal/repository"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"
)

type Service struct {
	Store          *repository.Store
	Config         platform.Config
	Keys           *platform.Keystore
	Auth           *Authenticator
	StorageBlocked atomic.Bool
	Stopping       atomic.Bool
	Wake           func()
	CancelJob      func(string)
	CancelActor    func(string)
	RetrySave      func(context.Context, domain.Actor, string) (domain.Job, error)
	testMu         sync.Mutex
	testActive     bool
	testLast       time.Time
	testCancel     context.CancelFunc
	testActor      string
}

func New(s *repository.Store, c platform.Config, k *platform.Keystore) *Service {
	return &Service{Store: s, Config: c, Keys: k, Auth: NewAuthenticator(s)}
}

type Result struct {
	Data     json.RawMessage
	Status   int
	Replayed bool
}

func Text(s string, max int) bool {
	return utf8.ValidString(s) && utf8.RuneCountInString(s) <= max && strings.TrimSpace(s) != "" && !strings.ContainsRune(s, 0)
}
func Staff(a domain.Actor) error {
	if !domain.Staff(a.User) {
		return domain.E("FORBIDDEN")
	}
	return nil
}
func Admin(a domain.Actor) error {
	if a.Role != "admin" {
		return domain.E("FORBIDDEN")
	}
	return nil
}
func DBError(e error) error {
	if e == nil {
		return nil
	}
	var d *domain.Error
	if errors.As(e, &d) {
		return e
	}
	if strings.Contains(strings.ToLower(e.Error()), "locked") || strings.Contains(strings.ToLower(e.Error()), "busy") {
		return domain.E("STORAGE_BUSY")
	}
	return domain.E("STORAGE_WRITE_FAILED")
}
func (s *Service) Actor(ctx context.Context, token string) (domain.Actor, error) {
	if token == "" {
		return domain.Actor{}, domain.E("AUTH_REQUIRED")
	}
	return repository.Session(ctx, s.Store.DB, token)
}
func (s *Service) mutate(ctx context.Context, a domain.Actor, route, key string, body any, scope func(context.Context, repository.Executor, domain.Actor) error, fn func(context.Context, *sql.Tx, domain.Actor) (any, int, error)) (Result, error) {
	var out Result
	if s.Stopping.Load() {
		return out, domain.E("STORAGE_UNHEALTHY")
	}
	if len(key) > 128 || strings.ContainsAny(key, "\r\n\t ") {
		return out, domain.E("VALIDATION_ERROR")
	}
	hash := repository.Sum(repository.JSON(body))
	kh := repository.Sum(key)
	load := func(c context.Context, q repository.Executor, actor domain.Actor) (bool, error) {
		if key == "" {
			return false, nil
		}
		var rh, rs string
		var status int
		e := q.QueryRowContext(c, "SELECT request_hash,status,response_json FROM idempotency WHERE actor_id=? AND route=? AND key_hash=? AND created_at>?", actor.ID, route, kh, time.Now().UTC().Add(-24*time.Hour).Format(time.RFC3339Nano)).Scan(&rh, &status, &rs)
		if errors.Is(e, sql.ErrNoRows) {
			return false, nil
		}
		if e != nil {
			return false, e
		}
		if rh != hash {
			return false, domain.E("IDEMPOTENCY_CONFLICT")
		}
		out = Result{Data: json.RawMessage(rs), Status: status, Replayed: true}
		return true, nil
	}
	e := s.Store.Tx(ctx, func(tx *sql.Tx) error {
		actor, e := repository.Session(ctx, tx, a.TokenHash)
		if e != nil {
			return e
		}
		if e = scope(ctx, tx, actor); e != nil {
			return e
		}
		found, e := load(ctx, tx, actor)
		if e != nil || found {
			return e
		}
		v, status, e := fn(ctx, tx, actor)
		if e != nil {
			return e
		}
		b, e := json.Marshal(v)
		if e != nil {
			return e
		}
		out = Result{Data: b, Status: status}
		if key != "" {
			if _, e = tx.ExecContext(ctx, "DELETE FROM idempotency WHERE rowid IN (SELECT rowid FROM idempotency WHERE created_at<? LIMIT 100)", time.Now().UTC().Add(-24*time.Hour).Format(time.RFC3339Nano)); e != nil {
				return e
			}
			var count int
			if e = tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM idempotency WHERE actor_id=?", actor.ID).Scan(&count); e != nil {
				return e
			}
			if count >= 500 {
				return domain.E("IDEMPOTENCY_LIMIT")
			}
			_, e = tx.ExecContext(ctx, "INSERT INTO idempotency VALUES(?,?,?,?,?,?,?) ON CONFLICT(actor_id,route,key_hash) DO UPDATE SET request_hash=excluded.request_hash,status=excluded.status,response_json=excluded.response_json,created_at=excluded.created_at", actor.ID, route, kh, hash, status, string(b), repository.Now())
			return e
		}
		return nil
	})
	if e == nil {
		return out, nil
	}
	var ce *repository.CommitError
	if errors.As(e, &ce) {
		check, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		actor, ae := repository.Session(check, s.Store.DB, a.TokenHash)
		if ae == nil {
			ae = scope(check, s.Store.DB, actor)
		}
		if ae == nil {
			found, le := load(check, s.Store.DB, actor)
			if le == nil && found {
				return out, nil
			}
		}
		s.StorageBlocked.Store(true)
		return Result{}, domain.E("STORAGE_OUTCOME_UNKNOWN")
	}
	var de *domain.Error
	if !errors.As(e, &de) {
		s.StorageBlocked.Store(true)
	}
	return Result{}, DBError(e)
}
func anyScope(_ context.Context, _ repository.Executor, _ domain.Actor) error   { return nil }
func staffScope(_ context.Context, _ repository.Executor, a domain.Actor) error { return Staff(a) }
func adminScope(_ context.Context, _ repository.Executor, a domain.Actor) error { return Admin(a) }
func ticketScope(id string, staff bool) func(context.Context, repository.Executor, domain.Actor) error {
	return func(ctx context.Context, q repository.Executor, a domain.Actor) error {
		if staff {
			if e := Staff(a); e != nil {
				return e
			}
		}
		t, e := repository.Ticket(ctx, q, id)
		if e != nil {
			return e
		}
		return repository.CheckTicket(a.User, t)
	}
}
func revision(t domain.Ticket, v int64) error {
	if v != t.Revision {
		return domain.E("VERSION_CONFLICT")
	}
	return nil
}
func (s *Service) audit(ctx context.Context, q repository.Executor, a domain.Actor, ticket *string, action string, details any) error {
	return repository.Audit(ctx, q, ticket, &a.ID, action, details)
}
