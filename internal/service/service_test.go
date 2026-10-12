package service_test

import (
	"context"
	"database/sql"
	"errors"
	"flowdesk/internal/domain"
	"flowdesk/internal/repository"
	"flowdesk/internal/service"
	"flowdesk/internal/testkit"
	"strings"
	"sync"
	"testing"
)

func TestConcurrentIdempotencyCreatesOnce(t *testing.T) {
	f := testkit.New(t)
	b := service.TicketCreate{Title: "Concurrent", Description: "One operation", Category: "other", Priority: "normal"}
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, e := f.Service.CreateTicket(context.Background(), f.Admin, "same-key", b)
			errs <- e
		}()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		if e != nil {
			t.Fatal(e)
		}
	}
	var n int
	f.Store.DB.QueryRow("SELECT COUNT(*) FROM tickets WHERE title='Concurrent'").Scan(&n)
	if n != 1 {
		t.Fatal(n)
	}
	b.Title = "Different"
	_, e := f.Service.CreateTicket(context.Background(), f.Admin, "same-key", b)
	testkit.Code(t, e, "IDEMPOTENCY_CONFLICT")
}
func TestCommitAlreadyAppliedReturnsDurableIdempotentResult(t *testing.T) {
	f := testkit.New(t)
	f.Store.CommitHook = func(tx *sql.Tx) error {
		if e := tx.Commit(); e != nil {
			return e
		}
		return errors.New("commit acknowledgment lost")
	}
	b := service.TicketCreate{Title: "Committed", Description: "Original safe response", Category: "other", Priority: "normal"}
	r, e := f.Service.CreateTicket(context.Background(), f.Admin, "commit-key", b)
	if e != nil || !r.Replayed {
		t.Fatalf("%v %+v", e, r)
	}
	v := testkit.Data[domain.Ticket](t, r)
	if v.Title != b.Title {
		t.Fatal(v)
	}
}
func TestCommitNotAppliedReturnsUnknownWithoutDuplicate(t *testing.T) {
	f := testkit.New(t)
	f.Store.CommitHook = func(*sql.Tx) error { return errors.New("commit failed") }
	b := service.TicketCreate{Title: "Retry once", Description: "Same operation", Category: "other", Priority: "normal"}
	_, e := f.Service.CreateTicket(context.Background(), f.Admin, "commit-retry", b)
	testkit.Code(t, e, "STORAGE_OUTCOME_UNKNOWN")
	f.Store.CommitHook = nil
	if _, e = f.Service.CreateTicket(context.Background(), f.Admin, "commit-retry", b); e != nil {
		t.Fatal(e)
	}
	var n int
	f.Store.DB.QueryRow("SELECT COUNT(*) FROM tickets WHERE title='Retry once'").Scan(&n)
	if n != 1 {
		t.Fatal(n)
	}
}
func TestConcurrentCASDoesNotOverwrite(t *testing.T) {
	f := testkit.New(t)
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for _, title := range []string{"A", "B"} {
		title := title
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, e := f.Service.PatchTicket(context.Background(), f.Admin, "1", title, service.TicketPatch{ExpectedRevision: 1, Title: &title})
			errs <- e
		}()
	}
	wg.Wait()
	close(errs)
	success, conflict := 0, 0
	for e := range errs {
		if e == nil {
			success++
		} else if domain.AsError(e).Code == "VERSION_CONFLICT" {
			conflict++
		} else {
			t.Fatal(e)
		}
	}
	if success != 1 || conflict != 1 {
		t.Fatalf("%d %d", success, conflict)
	}
}
func TestRequesterScopesAndInternalComment(t *testing.T) {
	f := testkit.New(t)
	_, e := f.Service.Comment(context.Background(), f.Admin, "1", "note", service.CommentBody{ExpectedRevision: 1, Visibility: "internal", Body: "Private staff note"})
	if e != nil {
		t.Fatal(e)
	}
	v, e := f.Service.Ticket(context.Background(), f.Requester, "1")
	if e != nil || len(v.Comments) != 0 || v.Ticket.ActiveJobID != nil {
		t.Fatal(v, e)
	}
	r, e := f.Service.CreateTicket(context.Background(), f.Admin, "private", service.TicketCreate{Title: "Admin own", Description: "Only this requester", Category: "other", Priority: "normal"})
	if e != nil {
		t.Fatal(e)
	}
	ticket := testkit.Data[domain.Ticket](t, r)
	_, e = f.Service.Ticket(context.Background(), f.Requester, ticket.ID)
	testkit.Code(t, e, "NOT_FOUND")
	_, e = f.Service.Drafts(context.Background(), f.Requester, "1")
	testkit.Code(t, e, "FORBIDDEN")
	_, e = f.Service.Users(context.Background(), f.Agent)
	testkit.Code(t, e, "FORBIDDEN")
	_, e = f.Service.Settings(context.Background(), f.Agent)
	testkit.Code(t, e, "FORBIDDEN")
}
func TestReopenPreservesReplyAndChangesCycle(t *testing.T) {
	f := testkit.New(t)
	r, e := f.Service.Reply(context.Background(), f.Agent, "1", "manual", service.ManualBody{ExpectedRevision: 1, ReplyText: "  Exact published text\n"})
	if e != nil {
		t.Fatal(e)
	}
	ticket := testkit.Data[domain.Ticket](t, r)
	b := service.VersionBody{ExpectedRevision: ticket.Revision}
	r, e = f.Service.Reopen(context.Background(), f.Agent, "1", "reopen", b)
	if e != nil {
		t.Fatal(e)
	}
	ticket = testkit.Data[domain.Ticket](t, r)
	if ticket.Cycle != 2 || ticket.Status != "in_progress" || ticket.ApprovedReplyID != nil {
		t.Fatal(ticket)
	}
	v, e := f.Service.Ticket(context.Background(), f.Requester, "1")
	if e != nil || len(v.Replies) != 1 || v.Replies[0].Body != "  Exact published text\n" {
		t.Fatal(v, e)
	}
	if _, e = f.Service.Reopen(context.Background(), f.Agent, "1", "reopen", b); e != nil {
		t.Fatal("replay should precede old revision", e)
	}
}
func TestRoleRevocationInvalidatesOldSessionsEvenWhenRestored(t *testing.T) {
	f := testkit.New(t)
	r, e := f.Service.Enqueue(context.Background(), f.Agent, "1", "job", service.JobBody{ExpectedRevision: 1})
	if e != nil {
		t.Fatal(e)
	}
	job := testkit.Data[domain.Job](t, r)
	u, e := repository.User(context.Background(), f.Store.DB, f.Agent.ID)
	if e != nil {
		t.Fatal(e)
	}
	_, e = f.Service.PatchUser(context.Background(), f.Admin, u.ID, "down", service.UserPatch{ExpectedRevision: u.Revision, DisplayName: u.DisplayName, Role: "requester"})
	if e != nil {
		t.Fatal(e)
	}
	u, _ = repository.User(context.Background(), f.Store.DB, u.ID)
	_, e = f.Service.PatchUser(context.Background(), f.Admin, u.ID, "up", service.UserPatch{ExpectedRevision: u.Revision, DisplayName: u.DisplayName, Role: "agent"})
	if e != nil {
		t.Fatal(e)
	}
	_, e = f.Service.Actor(context.Background(), f.Agent.TokenHash)
	if e == nil {
		t.Fatal("old session survived")
	}
	j, e := repository.Job(context.Background(), f.Store.DB, job.ID)
	if e != nil || j.Status != "failed" || j.Secret != nil || j.ErrorCode == nil || *j.ErrorCode != "ACTOR_PERMISSION_CHANGED" {
		t.Fatal(j, e)
	}
}
func TestLastAdminAndSelfDisable(t *testing.T) {
	f := testkit.New(t)
	_, e := f.Service.PatchUser(context.Background(), f.Admin, "1", "disable", service.UserPatch{ExpectedRevision: 1, DisplayName: "Admin", Role: "admin", Disabled: true})
	testkit.Code(t, e, "SELF_DISABLE_FORBIDDEN")
	_, e = f.Service.PatchUser(context.Background(), f.Admin, "1", "downgrade", service.UserPatch{ExpectedRevision: 1, DisplayName: "Admin", Role: "agent"})
	testkit.Code(t, e, "LAST_ADMIN")
}
func TestCommentSnapshotUsesNewestCompleteContiguousComments(t *testing.T) {
	f := testkit.New(t)
	for i := 0; i < 12; i++ {
		_, e := f.Store.DB.Exec("INSERT INTO comments(ticket_id,cycle_no,author_id,visibility,body,created_at) VALUES(1,1,3,'public',?,'now')", strings.Repeat("好", 500))
		if e != nil {
			t.Fatal(e)
		}
	}
	r, e := f.Service.Enqueue(context.Background(), f.Agent, "1", "snapshot", service.JobBody{ExpectedRevision: 1})
	if e != nil {
		t.Fatal(e)
	}
	j := testkit.Data[domain.Job](t, r)
	full, e := repository.Job(context.Background(), f.Store.DB, j.ID)
	if e != nil || len(full.Input.Comments) != 6 || !full.Input.ContextOmitted || full.Input.Comments[0].ID != "7" || full.Input.Comments[5].ID != "12" {
		t.Fatal(full.Input, e)
	}
}
