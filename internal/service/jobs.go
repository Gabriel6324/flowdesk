package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"flowdesk/internal/domain"
	"flowdesk/internal/repository"
	"sort"
	"unicode/utf8"
)

type JobBody struct {
	ExpectedRevision      int64  `json:"expected_revision"`
	Strategy              string `json:"strategy"`
	ExpectedConfigVersion *int64 `json:"expected_config_version,omitempty"`
}

func frozenInput(ctx context.Context, q repository.Executor, t domain.Ticket) (domain.Input, error) {
	in := domain.Input{TicketID: t.ID, Title: t.Title, Description: t.Description, Version: t.Version, Comments: []domain.Comment{}}
	comments, e := repository.Comments(ctx, q, t.ID, false)
	if e != nil {
		return in, e
	}
	eligible := []domain.Comment{}
	for _, c := range comments {
		if c.Cycle == t.Cycle {
			eligible = append(eligible, c)
		}
	}
	size := 0
	for i := len(eligible) - 1; i >= 0; i-- {
		c := eligible[i]
		n := utf8.RuneCountInString(c.Body)
		if len(in.Comments) == 10 || size+n > 3000 {
			in.ContextOmitted = true
			break
		}
		size += n
		in.Comments = append(in.Comments, c)
	}
	sort.Slice(in.Comments, func(i, j int) bool {
		x, _ := repository.ParseID(in.Comments[i].ID)
		y, _ := repository.ParseID(in.Comments[j].ID)
		return x < y
	})
	return in, nil
}
func (s *Service) enqueue(ctx context.Context, a domain.Actor, ticket, key, route string, b JobBody, parent *string) (Result, error) {
	out, e := s.mutate(ctx, a, route, key, b, ticketScope(ticket, true), func(ctx context.Context, tx *sql.Tx, a domain.Actor) (any, int, error) {
		if s.StorageBlocked.Load() {
			return nil, 0, domain.E("STORAGE_UNHEALTHY")
		}
		if parent != nil {
			j, e := repository.Job(ctx, tx, *parent)
			if e != nil {
				return nil, 0, e
			}
			if !domain.Enum(j.Status, "failed", "canceled", "stale") {
				return nil, 0, domain.E("INVALID_TRANSITION")
			}
		}
		t, e := repository.Ticket(ctx, tx, ticket)
		if e != nil {
			return nil, 0, e
		}
		if e = revision(t, b.ExpectedRevision); e != nil {
			return nil, 0, e
		}
		if t.Status == "resolved" {
			return nil, 0, domain.E("INVALID_TRANSITION")
		}
		if t.ActiveJobID != nil {
			return nil, 0, domain.E("ACTIVE_JOB_EXISTS")
		}
		var count int
		if e = tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM jobs WHERE status IN ('queued','running')").Scan(&count); e != nil {
			return nil, 0, e
		}
		if count >= 50 {
			return nil, 0, domain.E("QUEUE_FULL")
		}
		cfg, encrypted, e := repository.Settings(ctx, tx)
		if e != nil {
			return nil, 0, e
		}
		if b.ExpectedConfigVersion != nil && *b.ExpectedConfigVersion != cfg.Version {
			return nil, 0, domain.WithStage(domain.E("CONFIG_VERSION_CONFLICT"), "configuration")
		}
		if b.Strategy != "" {
			if !domain.Enum(b.Strategy, "workflow", "agent") {
				return nil, 0, domain.E("VALIDATION_ERROR")
			}
			cfg.Strategy = b.Strategy
		}
		if cfg.Mode == "live" {
			if b.ExpectedConfigVersion == nil {
				return nil, 0, domain.WithStage(domain.E("LIVE_CONSENT_REQUIRED"), "configuration")
			}
			if s.Config.Profile == "server" && !s.Config.LiveEnabled {
				return nil, 0, domain.E("LIVE_DISABLED")
			}
			if !s.Keys.Available() {
				return nil, 0, domain.E("KEYSTORE_UNAVAILABLE")
			}
			if len(encrypted) == 0 || cfg.Model == "" || cfg.BaseURL == "" {
				return nil, 0, domain.E("CONFIG_MISSING")
			}
			plain, e := s.Keys.Decrypt(encrypted)
			if e != nil {
				return nil, 0, domain.E("KEYSTORE_UNAVAILABLE")
			}
			encrypted, e = s.Keys.Encrypt(plain)
			if e != nil {
				return nil, 0, e
			}
		} else {
			encrypted = nil
		}
		in, e := frozenInput(ctx, tx, t)
		if e != nil {
			return nil, 0, e
		}
		r, e := tx.ExecContext(ctx, "INSERT INTO jobs(ticket_id,actor_id,actor_authorization_version,parent_job_id,input_json,config_json,encrypted_secret,status,request_id,created_at) VALUES(?,?,?,?,?,?,?,'queued',?,?)", ticket, a.ID, a.AuthorizationVersion, parent, repository.JSON(in), repository.JSON(cfg), encrypted, requestID(ctx), repository.Now())
		if e != nil {
			return nil, 0, e
		}
		n, _ := r.LastInsertId()
		id := repository.ID(n)
		if _, e = tx.ExecContext(ctx, "UPDATE tickets SET active_job_id=?,status='in_progress',revision=revision+1,updated_at=? WHERE id=?", id, repository.Now(), ticket); e != nil {
			return nil, 0, e
		}
		if e = s.audit(ctx, tx, a, &ticket, "job_queued", map[string]any{"job_id": id, "mode": cfg.Mode, "strategy": cfg.Strategy}); e != nil {
			return nil, 0, e
		}
		j, e := repository.Job(ctx, tx, id)
		return j.Job, 202, e
	})
	if e == nil && s.Wake != nil {
		s.Wake()
	}
	return out, e
}

type ctxKey string

func WithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, ctxKey("request_id"), id)
}
func requestID(ctx context.Context) string {
	v, _ := ctx.Value(ctxKey("request_id")).(string)
	return v
}
func (s *Service) Enqueue(ctx context.Context, a domain.Actor, ticket, key string, b JobBody) (Result, error) {
	return s.enqueue(ctx, a, ticket, key, "POST /api/tickets/"+ticket+"/jobs", b, nil)
}
func (s *Service) RetryJob(ctx context.Context, a domain.Actor, id, key string, b JobBody) (Result, error) {
	if e := Staff(a); e != nil {
		return Result{}, e
	}
	j, e := repository.Job(ctx, s.Store.DB, id)
	if e != nil {
		return Result{}, e
	}
	return s.enqueue(ctx, a, j.TicketID, key, "POST /api/jobs/"+id+"/retry", b, &id)
}
func (s *Service) Cancel(ctx context.Context, a domain.Actor, id, key string) (Result, error) {
	scope := func(ctx context.Context, q repository.Executor, a domain.Actor) error {
		if e := Staff(a); e != nil {
			return e
		}
		_, e := repository.Job(ctx, q, id)
		return e
	}
	out, e := s.mutate(ctx, a, "POST /api/jobs/"+id+"/cancel", key, struct{}{}, scope, func(ctx context.Context, tx *sql.Tx, a domain.Actor) (any, int, error) {
		j, e := repository.Job(ctx, tx, id)
		if e != nil {
			return nil, 0, e
		}
		if domain.Enum(j.Status, "queued", "running") {
			if _, e = tx.ExecContext(ctx, "UPDATE jobs SET status='canceled',encrypted_secret=NULL,finished_at=? WHERE id=? AND status IN ('queued','running')", repository.Now(), id); e != nil {
				return nil, 0, e
			}
			if e = ClearActive(ctx, tx, j.TicketID, id); e != nil {
				return nil, 0, e
			}
			if e = s.audit(ctx, tx, a, &j.TicketID, "job_canceled", map[string]any{"job_id": id}); e != nil {
				return nil, 0, e
			}
		}
		j, e = repository.Job(ctx, tx, id)
		return j.Job, 200, e
	})
	if e == nil && s.CancelJob != nil {
		s.CancelJob(id)
	}
	return out, e
}
func ClearActive(ctx context.Context, q repository.Executor, ticket, job string) error {
	t, e := repository.Ticket(ctx, q, ticket)
	if e != nil {
		return e
	}
	if t.ActiveJobID == nil || *t.ActiveJobID != job {
		return nil
	}
	status := t.Status
	if status != "resolved" {
		status = "in_progress"
		var count int
		if e = q.QueryRowContext(ctx, "SELECT COUNT(*) FROM drafts d LEFT JOIN reviews r ON r.draft_id=d.id WHERE d.ticket_id=? AND d.content_version=? AND d.conversation_version=? AND d.cycle_no=? AND r.id IS NULL", ticket, t.Content, t.Conversation, t.Cycle).Scan(&count); e != nil {
			return e
		}
		if count > 0 {
			status = "awaiting_review"
		}
	}
	_, e = q.ExecContext(ctx, "UPDATE tickets SET active_job_id=NULL,status=?,revision=revision+1,updated_at=? WHERE id=? AND active_job_id=?", status, repository.Now(), ticket, job)
	return e
}
func (s *Service) Job(ctx context.Context, a domain.Actor, id string) (domain.Job, error) {
	if e := Staff(a); e != nil {
		return domain.Job{}, e
	}
	j, e := repository.Job(ctx, s.Store.DB, id)
	if e != nil {
		return j.Job, e
	}
	e = repository.JobDetails(ctx, s.Store.DB, &j.Job)
	return j.Job, DBError(e)
}
func (s *Service) Jobs(ctx context.Context, a domain.Actor, page, size int, ticket string) (domain.Page[domain.Job], error) {
	out := domain.Page[domain.Job]{Items: []domain.Job{}, Page: page, PageSize: size}
	if e := Staff(a); e != nil {
		return out, e
	}
	if page < 1 || page > 100000 || size < 1 || size > 100 {
		return out, domain.E("VALIDATION_ERROR")
	}
	where := ""
	args := []any{}
	if ticket != "" {
		if _, e := repository.ParseID(ticket); e != nil {
			return out, e
		}
		where = " WHERE ticket_id=?"
		args = append(args, ticket)
	}
	if e := s.Store.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM jobs"+where, args...).Scan(&out.Total); e != nil {
		return out, DBError(e)
	}
	rows, e := s.Store.DB.QueryContext(ctx, "SELECT id FROM jobs"+where+" ORDER BY id DESC LIMIT ? OFFSET ?", append(args, size, (page-1)*size)...)
	if e != nil {
		return out, DBError(e)
	}
	ids := []string{}
	for rows.Next() {
		var id int64
		if e = rows.Scan(&id); e != nil {
			rows.Close()
			return out, DBError(e)
		}
		ids = append(ids, repository.ID(id))
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return out, DBError(e)
	}
	for _, id := range ids {
		v, e := s.Job(ctx, a, id)
		if e != nil {
			return out, e
		}
		out.Items = append(out.Items, v)
	}
	return out, nil
}
func (s *Service) Audit(ctx context.Context, a domain.Actor, ticket string) ([]domain.Audit, error) {
	if e := Staff(a); e != nil {
		return nil, e
	}
	if _, e := repository.Ticket(ctx, s.Store.DB, ticket); e != nil {
		return nil, e
	}
	rows, e := s.Store.DB.QueryContext(ctx, "SELECT a.id,COALESCE(u.display_name,'系统'),a.action,a.details_json,a.created_at FROM audit_events a LEFT JOIN users u ON u.id=a.actor_id WHERE a.ticket_id=? ORDER BY a.id DESC LIMIT 200", ticket)
	if e != nil {
		return nil, DBError(e)
	}
	defer rows.Close()
	out := []domain.Audit{}
	for rows.Next() {
		var v domain.Audit
		var id int64
		var details string
		if e = rows.Scan(&id, &v.ActorName, &v.Action, &details, &v.CreatedAt); e != nil {
			return nil, DBError(e)
		}
		v.ID = repository.ID(id)
		if e = json.Unmarshal([]byte(details), &v.Details); e != nil {
			return nil, DBError(e)
		}
		out = append(out, v)
	}
	return out, DBError(rows.Err())
}
