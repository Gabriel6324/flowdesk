package service

import (
	"context"
	"database/sql"
	"flowdesk/internal/domain"
	"flowdesk/internal/repository"
	"fmt"
	"strings"
	"unicode/utf8"
)

type TicketCreate struct {
	Title       string `json:"title"`
	Description string `json:"description"`
	Category    string `json:"category"`
	Priority    string `json:"priority"`
}
type TicketPatch struct {
	ExpectedRevision int64   `json:"expected_revision"`
	Title            *string `json:"title"`
	Description      *string `json:"description"`
	Category         *string `json:"category"`
	Priority         *string `json:"priority"`
	Status           *string `json:"status"`
	AssigneeID       *string `json:"assignee_id"`
	ClearAssignee    bool    `json:"clear_assignee"`
}
type VersionBody struct {
	ExpectedRevision int64 `json:"expected_revision"`
}
type CommentBody struct {
	ExpectedRevision int64  `json:"expected_revision"`
	Visibility       string `json:"visibility"`
	Body             string `json:"body"`
}
type TicketFilter struct {
	Status, Category, Priority, Search string
	Page, PageSize                     int
}

func (f TicketFilter) SQL(a domain.Actor) (string, []any, error) {
	parts := []string{"1=1"}
	args := []any{}
	if !domain.Staff(a.User) {
		parts = append(parts, "t.requester_id=?")
		args = append(args, a.ID)
	}
	for _, x := range [][3]string{{"t.status", f.Status, "status"}, {"t.category", f.Category, "category"}, {"t.priority", f.Priority, "priority"}} {
		if x[1] == "" {
			continue
		}
		valid := false
		switch x[2] {
		case "status":
			valid = domain.Enum(x[1], "open", "in_progress", "awaiting_review", "resolved")
		case "category":
			valid = domain.Enum(x[1], "account", "billing", "technical", "other")
		case "priority":
			valid = domain.Enum(x[1], "low", "normal", "high", "urgent")
		}
		if !valid {
			return "", nil, domain.E("VALIDATION_ERROR")
		}
		parts = append(parts, x[0]+"=?")
		args = append(args, x[1])
	}
	if utf8.RuneCountInString(f.Search) > 200 {
		return "", nil, domain.E("VALIDATION_ERROR")
	}
	if f.Search != "" {
		parts = append(parts, "(t.title LIKE ? ESCAPE '\\' OR t.description LIKE ? ESCAPE '\\')")
		q := strings.NewReplacer("\\", "\\\\", "%", "\\%", "_", "\\_").Replace(f.Search)
		args = append(args, "%"+q+"%", "%"+q+"%")
	}
	return " WHERE " + strings.Join(parts, " AND "), args, nil
}
func (s *Service) Tickets(ctx context.Context, a domain.Actor, f TicketFilter) (domain.Page[domain.Ticket], error) {
	out := domain.Page[domain.Ticket]{Items: []domain.Ticket{}}
	if f.Page < 1 {
		f.Page = 1
	}
	if f.PageSize < 1 {
		f.PageSize = 20
	}
	if f.PageSize > 100 || f.Page > 100000 {
		return out, domain.E("VALIDATION_ERROR")
	}
	out.Page = f.Page
	out.PageSize = f.PageSize
	where, args, e := f.SQL(a)
	if e != nil {
		return out, e
	}
	e = s.Store.DB.QueryRowContext(ctx, "SELECT COUNT(*)"+" FROM tickets t"+where, args...).Scan(&out.Total)
	if e != nil {
		return out, DBError(e)
	}
	rows, e := s.Store.DB.QueryContext(ctx, repository.TicketSelect()+where+" ORDER BY t.id DESC LIMIT ? OFFSET ?", append(args, f.PageSize, (f.Page-1)*f.PageSize)...)
	if e != nil {
		return out, DBError(e)
	}
	defer rows.Close()
	for rows.Next() {
		t, e := repository.ScanTicket(rows)
		if e != nil {
			return out, DBError(e)
		}
		if !domain.Staff(a.User) {
			t.ActiveJobID = nil
		}
		out.Items = append(out.Items, t)
	}
	return out, DBError(rows.Err())
}
func (s *Service) Ticket(ctx context.Context, a domain.Actor, id string) (domain.TicketDetail, error) {
	var out domain.TicketDetail
	e := s.Store.Tx(ctx, func(tx *sql.Tx) error {
		t, e := repository.Ticket(ctx, tx, id)
		if e != nil {
			return e
		}
		if e = repository.CheckTicket(a.User, t); e != nil {
			return e
		}
		if !domain.Staff(a.User) {
			t.ActiveJobID = nil
		}
		out.Ticket = t
		out.Comments, e = repository.Comments(ctx, tx, id, domain.Staff(a.User))
		if e != nil {
			return e
		}
		out.Replies, e = repository.Replies(ctx, tx, id)
		return e
	})
	return out, DBError(e)
}
func (s *Service) CreateTicket(ctx context.Context, a domain.Actor, key string, b TicketCreate) (Result, error) {
	if !Text(b.Title, 200) || !Text(b.Description, 6000) || !domain.Enum(b.Category, "account", "billing", "technical", "other") || !domain.Enum(b.Priority, "low", "normal", "high", "urgent") {
		return Result{}, domain.E("VALIDATION_ERROR")
	}
	return s.mutate(ctx, a, "POST /api/tickets", key, b, anyScope, func(ctx context.Context, tx *sql.Tx, a domain.Actor) (any, int, error) {
		now := repository.Now()
		r, e := tx.ExecContext(ctx, "INSERT INTO tickets(title,description,requester_id,category,priority,status,created_at,updated_at) VALUES(?,?,?,?,?,'open',?,?)", b.Title, b.Description, a.ID, b.Category, b.Priority, now, now)
		if e != nil {
			return nil, 0, e
		}
		n, e := r.LastInsertId()
		if e != nil {
			return nil, 0, e
		}
		id := repository.ID(n)
		if e = s.audit(ctx, tx, a, &id, "ticket_created", map[string]any{}); e != nil {
			return nil, 0, e
		}
		t, e := repository.Ticket(ctx, tx, id)
		return t, 201, e
	})
}
func (s *Service) PatchTicket(ctx context.Context, a domain.Actor, id, key string, b TicketPatch) (Result, error) {
	return s.mutate(ctx, a, "PATCH /api/tickets/"+id, key, b, ticketScope(id, false), func(ctx context.Context, tx *sql.Tx, a domain.Actor) (any, int, error) {
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
		if (b.Title != nil || b.Description != nil) && (t.ActiveJobID != nil || !domain.Enum(t.Status, "open", "in_progress") || (!domain.Staff(a.User) && t.Status != "open")) {
			return nil, 0, domain.E("INVALID_TRANSITION")
		}
		content := false
		changed := false
		before := map[string]any{}
		if b.Title != nil {
			if !Text(*b.Title, 200) {
				return nil, 0, domain.E("VALIDATION_ERROR")
			}
			content = content || *b.Title != t.Title
			before["title_changed"] = *b.Title != t.Title
			t.Title = *b.Title
		}
		if b.Description != nil {
			if !Text(*b.Description, 6000) {
				return nil, 0, domain.E("VALIDATION_ERROR")
			}
			content = content || *b.Description != t.Description
			t.Description = *b.Description
		}
		if !domain.Staff(a.User) && (b.Category != nil || b.Priority != nil || b.Status != nil || b.AssigneeID != nil || b.ClearAssignee) {
			return nil, 0, domain.E("FORBIDDEN")
		}
		if b.Category != nil {
			if !domain.Enum(*b.Category, "account", "billing", "technical", "other") {
				return nil, 0, domain.E("VALIDATION_ERROR")
			}
			changed = changed || *b.Category != t.Category
			t.Category = *b.Category
		}
		if b.Priority != nil {
			if !domain.Enum(*b.Priority, "low", "normal", "high", "urgent") {
				return nil, 0, domain.E("VALIDATION_ERROR")
			}
			changed = changed || *b.Priority != t.Priority
			t.Priority = *b.Priority
		}
		if b.Status != nil {
			if *b.Status != t.Status {
				if t.Status != "open" || *b.Status != "in_progress" {
					return nil, 0, domain.E("INVALID_TRANSITION")
				}
				changed = true
				t.Status = *b.Status
			}
		}
		if b.ClearAssignee && b.AssigneeID != nil {
			return nil, 0, domain.E("VALIDATION_ERROR")
		}
		if b.AssigneeID != nil {
			if _, e = repository.ParseID(*b.AssigneeID); e != nil {
				return nil, 0, e
			}
			u, e := repository.User(ctx, tx, *b.AssigneeID)
			if e != nil || u.Disabled || !domain.Staff(u) {
				return nil, 0, domain.E("VALIDATION_ERROR")
			}
			changed = changed || t.AssigneeID == nil || *t.AssigneeID != *b.AssigneeID
			t.AssigneeID = b.AssigneeID
		}
		if b.ClearAssignee {
			changed = changed || t.AssigneeID != nil
			t.AssigneeID = nil
		}
		if content || changed {
			if content && t.Status == "awaiting_review" {
				t.Status = "in_progress"
			}
			cv := t.Content
			if content {
				cv++
			}
			_, e = tx.ExecContext(ctx, "UPDATE tickets SET title=?,description=?,category=?,priority=?,status=?,assignee_id=?,revision=revision+1,content_version=?,updated_at=? WHERE id=? AND revision=?", t.Title, t.Description, t.Category, t.Priority, t.Status, t.AssigneeID, cv, repository.Now(), id, b.ExpectedRevision)
			if e != nil {
				return nil, 0, e
			}
		}
		if e = s.audit(ctx, tx, a, &id, "ticket_updated", map[string]any{"content_changed": content, "fields_changed": changed}); e != nil {
			return nil, 0, e
		}
		t, e = repository.Ticket(ctx, tx, id)
		if !domain.Staff(a.User) {
			t.ActiveJobID = nil
		}
		return t, 200, e
	})
}
func (s *Service) Comment(ctx context.Context, a domain.Actor, id, key string, b CommentBody) (Result, error) {
	return s.mutate(ctx, a, "POST /api/tickets/"+id+"/comments", key, b, ticketScope(id, false), func(ctx context.Context, tx *sql.Tx, a domain.Actor) (any, int, error) {
		if !Text(b.Body, 3000) || !domain.Enum(b.Visibility, "public", "internal") {
			return nil, 0, domain.E("VALIDATION_ERROR")
		}
		if b.Visibility == "internal" && !domain.Staff(a.User) {
			return nil, 0, domain.E("FORBIDDEN")
		}
		t, e := repository.Ticket(ctx, tx, id)
		if e != nil {
			return nil, 0, e
		}
		if e = revision(t, b.ExpectedRevision); e != nil {
			return nil, 0, e
		}
		r, e := tx.ExecContext(ctx, "INSERT INTO comments(ticket_id,cycle_no,author_id,visibility,body,created_at) VALUES(?,?,?,?,?,?)", id, t.Cycle, a.ID, b.Visibility, b.Body, repository.Now())
		if e != nil {
			return nil, 0, e
		}
		cv := t.Conversation
		status := t.Status
		if b.Visibility == "public" {
			cv++
			if status == "awaiting_review" {
				status = "in_progress"
			}
		}
		if _, e = tx.ExecContext(ctx, "UPDATE tickets SET conversation_version=?,status=?,revision=revision+1,updated_at=? WHERE id=?", cv, status, repository.Now(), id); e != nil {
			return nil, 0, e
		}
		n, _ := r.LastInsertId()
		if e = s.audit(ctx, tx, a, &id, "comment_added", map[string]any{"visibility": b.Visibility, "comment_id": repository.ID(n)}); e != nil {
			return nil, 0, e
		}
		return map[string]any{"id": repository.ID(n)}, 201, nil
	})
}
func (s *Service) Reopen(ctx context.Context, a domain.Actor, id, key string, b VersionBody) (Result, error) {
	return s.mutate(ctx, a, "POST /api/tickets/"+id+"/reopen", key, b, ticketScope(id, true), func(ctx context.Context, tx *sql.Tx, a domain.Actor) (any, int, error) {
		t, e := repository.Ticket(ctx, tx, id)
		if e != nil {
			return nil, 0, e
		}
		if e = revision(t, b.ExpectedRevision); e != nil {
			return nil, 0, e
		}
		if t.Status != "resolved" {
			return nil, 0, domain.E("INVALID_TRANSITION")
		}
		if _, e = tx.ExecContext(ctx, "UPDATE tickets SET status='in_progress',cycle_no=cycle_no+1,revision=revision+1,approved_reply_id=NULL,resolved_at=NULL,updated_at=? WHERE id=?", repository.Now(), id); e != nil {
			return nil, 0, e
		}
		if e = s.audit(ctx, tx, a, &id, "ticket_reopened", map[string]any{"cycle_no": t.Cycle + 1}); e != nil {
			return nil, 0, e
		}
		t, e = repository.Ticket(ctx, tx, id)
		return t, 200, e
	})
}
func (s *Service) Dashboard(ctx context.Context, a domain.Actor) (domain.Dashboard, error) {
	var d domain.Dashboard
	where := ""
	args := []any{}
	if !domain.Staff(a.User) {
		where = " WHERE requester_id=?"
		args = append(args, a.ID)
	}
	e := s.Store.DB.QueryRowContext(ctx, "SELECT COUNT(*),COALESCE(SUM(status='open'),0),COALESCE(SUM(status='in_progress'),0),COALESCE(SUM(status='awaiting_review'),0),COALESCE(SUM(status='resolved'),0) FROM tickets"+where, args...).Scan(&d.Total, &d.Open, &d.InProgress, &d.AwaitingReview, &d.Resolved)
	if e != nil {
		return d, DBError(e)
	}
	if !domain.Staff(a.User) {
		return d, nil
	}
	cfg, encrypted, configErr := repository.Settings(ctx, s.Store.DB)
	if configErr != nil {
		return d, DBError(configErr)
	}
	d.AIMode = &cfg.Mode
	d.AIStrategy = &cfg.Strategy
	d.AIConfigVersion = &cfg.Version
	paused := s.StorageBlocked.Load()
	d.StorageBlocked = &paused
	mode := s.Config.WorkspaceMode()
	d.WorkspaceMode = &mode
	d.Setup = &domain.WorkspaceReadiness{ModelConfigured: cfg.Mode == "live" && cfg.BaseURL != "" && cfg.Model != "" && len(encrypted) > 0 && s.Keys.Available() && (s.Config.Profile == "desktop" || s.Config.LiveEnabled)}
	if e = s.Store.DB.QueryRowContext(ctx, "SELECT (SELECT COUNT(*) FROM knowledge_articles WHERE archived=0),(SELECT COUNT(*) FROM users WHERE disabled=0 AND role='requester'),(SELECT COUNT(*) FROM users WHERE disabled=0 AND role IN ('admin','agent'))").Scan(&d.Setup.KnowledgeArticles, &d.Setup.RequesterAccounts, &d.Setup.StaffAccounts); e != nil {
		return d, DBError(e)
	}
	if e = s.Store.DB.QueryRowContext(ctx, "SELECT COUNT(*),COALESCE(SUM(provider_mode='demo'),0),COALESCE(SUM(provider_mode='live'),0) FROM drafts").Scan(&d.Generated, &d.DemoGenerated, &d.LiveGenerated); e != nil {
		return d, DBError(e)
	}
	if e = s.Store.DB.QueryRowContext(ctx, "SELECT COALESCE(SUM(decision='approved'),0),COALESCE(SUM(decision='rejected'),0),COALESCE(SUM(decision='manual'),0) FROM reviews").Scan(&d.Approved, &d.Rejected, &d.Manual); e != nil {
		return d, DBError(e)
	}
	rows, e := s.Store.DB.QueryContext(ctx, "SELECT id,display_name FROM users WHERE disabled=0 AND role IN ('admin','agent') ORDER BY id")
	if e != nil {
		return d, DBError(e)
	}
	defer rows.Close()
	d.Assignees = []domain.Assignee{}
	for rows.Next() {
		var v domain.Assignee
		var id int64
		if e = rows.Scan(&id, &v.DisplayName); e != nil {
			return d, DBError(e)
		}
		v.ID = fmt.Sprint(id)
		d.Assignees = append(d.Assignees, v)
	}
	return d, DBError(rows.Err())
}
func (s *Service) ExportTickets(ctx context.Context, a domain.Actor, f TicketFilter) ([]domain.Ticket, error) {
	out := []domain.Ticket{}
	where, args, e := f.SQL(a)
	if e != nil {
		return nil, e
	}
	e = s.Store.Tx(ctx, func(tx *sql.Tx) error {
		current, e := repository.Session(ctx, tx, a.TokenHash)
		if e != nil {
			return e
		}
		where, args, e = currentFilter(f, current)
		if e != nil {
			return e
		}
		rows, e := tx.QueryContext(ctx, repository.TicketSelect()+where+" ORDER BY t.id DESC LIMIT 2001", args...)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			t, e := repository.ScanTicket(rows)
			if e != nil {
				return e
			}
			if !domain.Staff(current.User) {
				t.ActiveJobID = nil
			}
			out = append(out, t)
		}
		if e = rows.Err(); e != nil {
			return e
		}
		if len(out) > 2000 {
			return domain.E("EXPORT_LIMIT")
		}
		return nil
	})
	return out, DBError(e)
}
func currentFilter(f TicketFilter, a domain.Actor) (string, []any, error) { return f.SQL(a) }
