package service

import (
	"context"
	"database/sql"
	"flowdesk/internal/domain"
	"flowdesk/internal/repository"
	"sort"
)

type DraftAction struct {
	ExpectedRevision      int64    `json:"expected_revision"`
	DraftID               string   `json:"draft_id"`
	ApplyCategory         bool     `json:"apply_category"`
	ApplyPriority         bool     `json:"apply_priority"`
	ReasonCode            string   `json:"reason_code"`
	ReplyText             string   `json:"reply_text"`
	ExpectedReviewContext string   `json:"expected_review_context"`
	Acknowledgements      []string `json:"acknowledgements"`
}
type ManualBody struct {
	ExpectedRevision int64  `json:"expected_revision"`
	ReplyText        string `json:"reply_text"`
}

func ReviewContext(ctx context.Context, q repository.Executor, d domain.Draft) (domain.ReviewContext, error) {
	v := domain.ReviewContext{Heads: []domain.SourceHead{}, Required: []string{}}
	if len(d.Sources) == 0 {
		v.Required = append(v.Required, "no_evidence")
	}
	if d.ContextOmitted {
		v.Required = append(v.Required, "context_omitted")
	}
	changed := false
	for _, s := range d.Sources {
		a, e := repository.Article(ctx, q, s.ArticleID)
		if e != nil {
			return v, e
		}
		v.Heads = append(v.Heads, domain.SourceHead{ArticleID: a.ID, Version: a.Version, Archived: a.Archived})
		changed = changed || a.Version != s.Version || a.Archived
	}
	sort.Slice(v.Heads, func(i, j int) bool {
		a, _ := repository.ParseID(v.Heads[i].ArticleID)
		b, _ := repository.ParseID(v.Heads[j].ArticleID)
		return a < b
	})
	if changed {
		v.Required = append(v.Required, "sources_changed")
	}
	sort.Strings(v.Required)
	v.Token = repository.Sum(repository.JSON(struct {
		Contract int                 `json:"contract"`
		DraftID  string              `json:"draft_id"`
		Version  domain.Version      `json:"version"`
		Heads    []domain.SourceHead `json:"heads"`
		Required []string            `json:"required"`
	}{1, d.ID, d.Version, v.Heads, v.Required}))
	return v, nil
}
func (s *Service) Drafts(ctx context.Context, a domain.Actor, id string) ([]domain.Draft, error) {
	if e := Staff(a); e != nil {
		return nil, e
	}
	out := []domain.Draft{}
	e := s.Store.Tx(ctx, func(tx *sql.Tx) error {
		ticket, e := repository.Ticket(ctx, tx, id)
		if e != nil {
			return e
		}
		out, e = ticketDrafts(ctx, tx, ticket)
		return e
	})
	return out, DBError(e)
}

func reviewable(ctx context.Context, q repository.Executor, t domain.Ticket, b DraftAction) (domain.Draft, error) {
	var d domain.Draft
	if e := revision(t, b.ExpectedRevision); e != nil {
		return d, e
	}
	if t.Status == "resolved" {
		return d, domain.E("INVALID_TRANSITION")
	}
	if t.ActiveJobID != nil {
		return d, domain.E("ACTIVE_JOB_EXISTS")
	}
	d, e := repository.Draft(ctx, q, b.DraftID)
	if e != nil {
		return d, e
	}
	if d.TicketID != t.ID {
		return d, domain.E("NOT_FOUND")
	}
	if !d.Version.Equal(t.Version) {
		return d, domain.E("DRAFT_STALE")
	}
	if d.ReviewState != "pending" {
		return d, domain.E("DRAFT_REVIEWED")
	}
	return d, nil
}
func (s *Service) DraftAction(ctx context.Context, a domain.Actor, id, key, action string, b DraftAction) (Result, error) {
	if _, e := repository.ParseID(b.DraftID); e != nil {
		return Result{}, e
	}
	return s.mutate(ctx, a, "POST /api/tickets/"+id+"/"+action, key, b, ticketScope(id, true), func(ctx context.Context, tx *sql.Tx, a domain.Actor) (any, int, error) {
		t, e := repository.Ticket(ctx, tx, id)
		if e != nil {
			return nil, 0, e
		}
		d, e := reviewable(ctx, tx, t, b)
		if e != nil {
			return nil, 0, e
		}
		switch action {
		case "apply-suggestion":
			if !b.ApplyCategory && !b.ApplyPriority {
				return nil, 0, domain.E("VALIDATION_ERROR")
			}
			before := map[string]any{"category": t.Category, "priority": t.Priority}
			changed := false
			if b.ApplyCategory {
				changed = changed || t.Category != d.Category
				t.Category = d.Category
			}
			if b.ApplyPriority {
				changed = changed || t.Priority != d.Priority
				t.Priority = d.Priority
			}
			if changed {
				if _, e = tx.ExecContext(ctx, "UPDATE tickets SET category=?,priority=?,revision=revision+1,updated_at=? WHERE id=?", t.Category, t.Priority, repository.Now(), id); e != nil {
					return nil, 0, e
				}
			}
			if e = s.audit(ctx, tx, a, &id, "suggestion_applied", map[string]any{"draft_id": d.ID, "before": before, "after": map[string]any{"category": t.Category, "priority": t.Priority}, "changed": changed}); e != nil {
				return nil, 0, e
			}
			t, e = repository.Ticket(ctx, tx, id)
			return map[string]any{"ticket": t, "changed": changed}, 200, e
		case "reject-draft":
			if !domain.Enum(b.ReasonCode, "incorrect", "unsupported", "irrelevant", "incomplete", "other") {
				return nil, 0, domain.E("VALIDATION_ERROR")
			}
			if _, e = tx.ExecContext(ctx, "INSERT INTO reviews(ticket_id,cycle_no,draft_id,actor_id,decision,reason_code,ack_json,review_context_json,content_version,conversation_version,created_at) VALUES(?,?,?,?,'rejected',?,'[]','{}',?,?,?)", id, t.Cycle, d.ID, a.ID, b.ReasonCode, t.Content, t.Conversation, repository.Now()); e != nil {
				return nil, 0, e
			}
			var remaining int
			if e = tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM drafts d LEFT JOIN reviews r ON r.draft_id=d.id WHERE d.ticket_id=? AND d.content_version=? AND d.conversation_version=? AND d.cycle_no=? AND r.id IS NULL", id, t.Content, t.Conversation, t.Cycle).Scan(&remaining); e != nil {
				return nil, 0, e
			}
			status := "in_progress"
			if remaining > 0 {
				status = "awaiting_review"
			}
			if _, e = tx.ExecContext(ctx, "UPDATE tickets SET status=?,revision=revision+1,updated_at=? WHERE id=?", status, repository.Now(), id); e != nil {
				return nil, 0, e
			}
			if e = s.audit(ctx, tx, a, &id, "draft_rejected", map[string]any{"draft_id": d.ID, "reason_code": b.ReasonCode}); e != nil {
				return nil, 0, e
			}
		case "approve":
			if !Text(b.ReplyText, 6000) {
				return nil, 0, domain.E("VALIDATION_ERROR")
			}
			rc, e := ReviewContext(ctx, tx, d)
			if e != nil {
				return nil, 0, e
			}
			if b.ExpectedReviewContext != rc.Token {
				return nil, 0, domain.E("REVIEW_CONTEXT_CHANGED")
			}
			ack := map[string]bool{}
			for _, x := range b.Acknowledgements {
				if !domain.Enum(x, "sources_changed", "no_evidence", "context_omitted") || ack[x] {
					return nil, 0, domain.E("VALIDATION_ERROR")
				}
				ack[x] = true
			}
			for _, x := range rc.Required {
				if !ack[x] {
					return nil, 0, domain.E("REVIEW_ACK_REQUIRED")
				}
			}
			r, e := tx.ExecContext(ctx, "INSERT INTO reviews(ticket_id,cycle_no,draft_id,actor_id,decision,reply_text,ack_json,review_context_json,content_version,conversation_version,original_reply_hash,text_changed,created_at) VALUES(?,?,?,?,'approved',?,?,?,?,?,?,?,?)", id, t.Cycle, d.ID, a.ID, b.ReplyText, repository.JSON(b.Acknowledgements), repository.JSON(rc), t.Content, t.Conversation, repository.Sum(d.Reply), b.ReplyText != d.Reply, repository.Now())
			if e != nil {
				return nil, 0, e
			}
			rid, _ := r.LastInsertId()
			if e = resolve(ctx, tx, id, rid); e != nil {
				return nil, 0, e
			}
			if e = s.audit(ctx, tx, a, &id, "reply_approved", map[string]any{"draft_id": d.ID, "review_id": repository.ID(rid), "text_changed": b.ReplyText != d.Reply}); e != nil {
				return nil, 0, e
			}
		default:
			return nil, 0, domain.E("NOT_FOUND")
		}
		t, e = repository.Ticket(ctx, tx, id)
		return t, 200, e
	})
}
func resolve(ctx context.Context, q repository.Executor, id string, rid int64) error {
	now := repository.Now()
	_, e := q.ExecContext(ctx, "UPDATE tickets SET status='resolved',approved_reply_id=?,resolved_at=?,revision=revision+1,updated_at=? WHERE id=?", rid, now, now, id)
	return e
}
func (s *Service) Reply(ctx context.Context, a domain.Actor, id, key string, b ManualBody) (Result, error) {
	return s.mutate(ctx, a, "POST /api/tickets/"+id+"/reply", key, b, ticketScope(id, true), func(ctx context.Context, tx *sql.Tx, a domain.Actor) (any, int, error) {
		if !Text(b.ReplyText, 6000) {
			return nil, 0, domain.E("VALIDATION_ERROR")
		}
		t, e := repository.Ticket(ctx, tx, id)
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
		r, e := tx.ExecContext(ctx, "INSERT INTO reviews(ticket_id,cycle_no,actor_id,decision,reply_text,ack_json,review_context_json,content_version,conversation_version,created_at) VALUES(?,?,?,'manual',?,'[]','{}',?,?,?)", id, t.Cycle, a.ID, b.ReplyText, t.Content, t.Conversation, repository.Now())
		if e != nil {
			return nil, 0, e
		}
		rid, _ := r.LastInsertId()
		if e = resolve(ctx, tx, id, rid); e != nil {
			return nil, 0, e
		}
		if e = s.audit(ctx, tx, a, &id, "manual_reply", map[string]any{"review_id": repository.ID(rid)}); e != nil {
			return nil, 0, e
		}
		t, e = repository.Ticket(ctx, tx, id)
		return t, 200, e
	})
}
