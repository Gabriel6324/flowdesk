package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"flowdesk/internal/domain"
	"fmt"
)

const userCols = "id,email,display_name,role,disabled,revision,authorization_version,credentials_version"

type Scanner interface{ Scan(...any) error }

func ScanUser(r Scanner) (u domain.User, e error) {
	var id int64
	e = r.Scan(&id, &u.Email, &u.DisplayName, &u.Role, &u.Disabled, &u.Revision, &u.AuthorizationVersion, &u.CredentialsVersion)
	u.ID = ID(id)
	return
}
func User(ctx context.Context, q Executor, id string) (domain.User, error) {
	return ScanUser(q.QueryRowContext(ctx, "SELECT "+userCols+" FROM users WHERE id=?", id))
}

type Credential struct {
	User       domain.User
	Hash, Salt []byte
	Iterations int
}

func Credentials(ctx context.Context, q Executor, email string) (v Credential, e error) {
	var id int64
	e = q.QueryRowContext(ctx, "SELECT "+userCols+",password_hash,password_salt,password_iterations FROM users WHERE email=?", email).Scan(&id, &v.User.Email, &v.User.DisplayName, &v.User.Role, &v.User.Disabled, &v.User.Revision, &v.User.AuthorizationVersion, &v.User.CredentialsVersion, &v.Hash, &v.Salt, &v.Iterations)
	v.User.ID = ID(id)
	return
}
func Session(ctx context.Context, q Executor, hash string) (domain.Actor, error) {
	var a domain.Actor
	var id, cv, av int64
	var expiry string
	e := q.QueryRowContext(ctx, "SELECT user_id,credentials_version,authorization_version,expires_at FROM sessions WHERE token_hash=?", hash).Scan(&id, &cv, &av, &expiry)
	if e != nil {
		return a, domain.E("AUTH_REQUIRED")
	}
	u, e := User(ctx, q, ID(id))
	if e != nil {
		return a, e
	}
	if u.Disabled || u.CredentialsVersion != cv || u.AuthorizationVersion != av || expiry <= Now() {
		return a, domain.E("SESSION_EXPIRED")
	}
	a = domain.Actor{User: u, TokenHash: hash}
	return a, nil
}

const ticketCols = "t.id,t.title,t.description,t.requester_id,r.display_name,t.assignee_id,a.display_name,t.category,t.priority,t.status,t.revision,t.content_version,t.conversation_version,t.cycle_no,t.active_job_id,t.approved_reply_id,t.created_at,t.updated_at,t.resolved_at"
const ticketFrom = " FROM tickets t JOIN users r ON r.id=t.requester_id LEFT JOIN users a ON a.id=t.assignee_id"

func ScanTicket(r Scanner) (t domain.Ticket, e error) {
	var id, req int64
	var ass, job, reply sql.NullInt64
	var assname, resolved sql.NullString
	e = r.Scan(&id, &t.Title, &t.Description, &req, &t.RequesterName, &ass, &assname, &t.Category, &t.Priority, &t.Status, &t.Revision, &t.Content, &t.Conversation, &t.Cycle, &job, &reply, &t.CreatedAt, &t.UpdatedAt, &resolved)
	t.ID = ID(id)
	t.DisplayID = fmt.Sprintf("FD-%04d", id)
	t.RequesterID = ID(req)
	t.AssigneeID = NullID(ass)
	t.AssigneeName = NullString(assname)
	t.ActiveJobID = NullID(job)
	t.ApprovedReplyID = NullID(reply)
	t.ResolvedAt = NullString(resolved)
	return
}
func Ticket(ctx context.Context, q Executor, id string) (domain.Ticket, error) {
	t, e := ScanTicket(q.QueryRowContext(ctx, "SELECT "+ticketCols+ticketFrom+" WHERE t.id=?", id))
	if errors.Is(e, sql.ErrNoRows) {
		return t, domain.E("NOT_FOUND")
	}
	return t, e
}
func TicketSelect() string { return "SELECT " + ticketCols + ticketFrom }
func CheckTicket(u domain.User, t domain.Ticket) error {
	if !domain.Staff(u) && t.RequesterID != u.ID {
		return domain.E("NOT_FOUND")
	}
	return nil
}
func Comments(ctx context.Context, q Executor, id string, staff bool) ([]domain.Comment, error) {
	out := []domain.Comment{}
	where := ""
	if !staff {
		where = " AND c.visibility='public'"
	}
	rows, e := q.QueryContext(ctx, "SELECT c.id,c.ticket_id,c.cycle_no,u.display_name,c.visibility,c.body,c.created_at FROM comments c JOIN users u ON u.id=c.author_id WHERE c.ticket_id=?"+where+" ORDER BY c.id", id)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	for rows.Next() {
		var c domain.Comment
		var n, t int64
		if e = rows.Scan(&n, &t, &c.Cycle, &c.AuthorName, &c.Visibility, &c.Body, &c.CreatedAt); e != nil {
			return nil, e
		}
		c.ID = ID(n)
		c.TicketID = ID(t)
		out = append(out, c)
	}
	return out, rows.Err()
}
func Replies(ctx context.Context, q Executor, id string) ([]domain.Reply, error) {
	out := []domain.Reply{}
	rows, e := q.QueryContext(ctx, "SELECT r.id,r.cycle_no,r.reply_text,u.display_name,r.created_at FROM reviews r JOIN users u ON u.id=r.actor_id WHERE r.ticket_id=? AND r.decision IN ('approved','manual') ORDER BY r.id DESC", id)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	for rows.Next() {
		var r domain.Reply
		var n int64
		if e = rows.Scan(&n, &r.Cycle, &r.Body, &r.AuthorName, &r.CreatedAt); e != nil {
			return nil, e
		}
		r.ID = ID(n)
		out = append(out, r)
	}
	return out, rows.Err()
}
func Article(ctx context.Context, q Executor, id string) (domain.Article, error) {
	var a domain.Article
	var n int64
	var tags string
	e := q.QueryRowContext(ctx, "SELECT id,title,body,tags_json,version,archived,updated_at FROM knowledge_articles WHERE id=?", id).Scan(&n, &a.Title, &a.Body, &tags, &a.Version, &a.Archived, &a.UpdatedAt)
	if errors.Is(e, sql.ErrNoRows) {
		return a, domain.E("NOT_FOUND")
	}
	a.ID = ID(n)
	a.DisplayID = "K" + a.ID
	if e == nil {
		e = json.Unmarshal([]byte(tags), &a.Tags)
	}
	return a, e
}
func Knowledge(ctx context.Context, q Executor, includeArchived bool) ([]domain.Article, error) {
	where := ""
	if !includeArchived {
		where = " WHERE archived=0"
	}
	rows, e := q.QueryContext(ctx, "SELECT id,title,body,tags_json,version,archived,updated_at FROM knowledge_articles"+where+" ORDER BY id")
	if e != nil {
		return nil, e
	}
	out := []domain.Article{}
	defer rows.Close()
	for rows.Next() {
		var a domain.Article
		var n int64
		var tags string
		if e = rows.Scan(&n, &a.Title, &a.Body, &tags, &a.Version, &a.Archived, &a.UpdatedAt); e != nil {
			return nil, e
		}
		a.ID = ID(n)
		a.DisplayID = "K" + a.ID
		if e = json.Unmarshal([]byte(tags), &a.Tags); e != nil {
			return nil, e
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
func Settings(ctx context.Context, q Executor) (domain.AIConfig, []byte, error) {
	var c domain.AIConfig
	var text string
	var key []byte
	var v int64
	e := q.QueryRowContext(ctx, "SELECT config_json,encrypted_key,version FROM ai_settings WHERE singleton_id=1").Scan(&text, &key, &v)
	if e == nil {
		e = json.Unmarshal([]byte(text), &c)
		c.Version = v
	}
	return c, key, e
}
func Audit(ctx context.Context, q Executor, ticket *string, actor *string, action string, details any) error {
	_, e := q.ExecContext(ctx, "INSERT INTO audit_events(ticket_id,actor_id,action,details_json,created_at) VALUES(?,?,?,?,?)", ticket, actor, action, JSON(details), Now())
	return e
}
func Job(ctx context.Context, q Executor, id string) (v domain.JobRecord, e error) {
	var jid, tid, aid int64
	var pid sql.NullInt64
	var input, config string
	var errcode, stage, started, finished sql.NullString
	e = q.QueryRowContext(ctx, "SELECT id,ticket_id,actor_id,actor_authorization_version,parent_job_id,input_json,config_json,encrypted_secret,status,error_code,error_stage,request_id,created_at,started_at,finished_at FROM jobs WHERE id=?", id).Scan(&jid, &tid, &aid, &v.ActorAuthorizationVersion, &pid, &input, &config, &v.Secret, &v.Status, &errcode, &stage, &v.RequestID, &v.CreatedAt, &started, &finished)
	if errors.Is(e, sql.ErrNoRows) {
		return v, domain.E("NOT_FOUND")
	}
	if e != nil {
		return
	}
	v.ID = ID(jid)
	v.TicketID = ID(tid)
	v.ActorID = ID(aid)
	v.ParentJobID = NullID(pid)
	v.ErrorCode = NullString(errcode)
	v.ErrorStage = NullString(stage)
	v.StartedAt = NullString(started)
	v.FinishedAt = NullString(finished)
	if e = json.Unmarshal([]byte(input), &v.Input); e != nil {
		return
	}
	e = json.Unmarshal([]byte(config), &v.Config)
	v.Mode = v.Config.Mode
	v.Strategy = v.Config.Strategy
	v.Configuration = v.Config
	v.Steps = []domain.Step{}
	v.Calls = []domain.Call{}
	return
}
func JobDetails(ctx context.Context, q Executor, v *domain.Job) error {
	rows, e := q.QueryContext(ctx, "SELECT ordinal,kind,state,started_at,finished_at FROM job_steps WHERE job_id=? ORDER BY ordinal", v.ID)
	if e != nil {
		return e
	}
	for rows.Next() {
		var st domain.Step
		var end sql.NullString
		if e = rows.Scan(&st.Ordinal, &st.Kind, &st.State, &st.StartedAt, &end); e != nil {
			rows.Close()
			return e
		}
		st.FinishedAt = NullString(end)
		v.Steps = append(v.Steps, st)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	rows, e = q.QueryContext(ctx, "SELECT call_json FROM model_calls WHERE job_id=? ORDER BY ordinal", v.ID)
	if e != nil {
		return e
	}
	defer rows.Close()
	for rows.Next() {
		var text string
		var c domain.Call
		if e = rows.Scan(&text); e != nil {
			return e
		}
		if e = json.Unmarshal([]byte(text), &c); e != nil {
			return e
		}
		v.Calls = append(v.Calls, c)
	}
	return rows.Err()
}
func Draft(ctx context.Context, q Executor, id string) (d domain.Draft, e error) {
	var n, j, t int64
	var output, warnings string
	e = q.QueryRowContext(ctx, "SELECT id,job_id,ticket_id,content_version,conversation_version,cycle_no,provider_mode,output_json,context_omitted,warning_json,created_at FROM drafts WHERE id=?", id).Scan(&n, &j, &t, &d.Content, &d.Conversation, &d.Cycle, &d.Mode, &output, &d.ContextOmitted, &warnings, &d.CreatedAt)
	if errors.Is(e, sql.ErrNoRows) {
		return d, domain.E("NOT_FOUND")
	}
	if e != nil {
		return
	}
	d.ID = ID(n)
	d.JobID = ID(j)
	d.TicketID = ID(t)
	d.ReviewState = "pending"
	d.Sources = []domain.Source{}
	if e = json.Unmarshal([]byte(output), &d.AIOutput); e != nil {
		return
	}
	if e = json.Unmarshal([]byte(warnings), &d.Warnings); e != nil {
		return
	}
	rows, e := q.QueryContext(ctx, "SELECT article_id,evidence_id,version,title,excerpt,body_hash FROM draft_sources WHERE draft_id=? ORDER BY evidence_id", id)
	if e != nil {
		return d, e
	}
	for rows.Next() {
		var s domain.Source
		var a int64
		if e = rows.Scan(&a, &s.EvidenceID, &s.Version, &s.Title, &s.Excerpt, &s.BodyHash); e != nil {
			rows.Close()
			return d, e
		}
		s.ArticleID = ID(a)
		d.Sources = append(d.Sources, s)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return d, e
	}
	e = q.QueryRowContext(ctx, "SELECT decision FROM reviews WHERE draft_id=?", id).Scan(&d.ReviewState)
	if errors.Is(e, sql.ErrNoRows) {
		e = nil
	}
	return d, e
}
