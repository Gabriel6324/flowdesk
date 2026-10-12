package worker_test

import (
	"context"
	"database/sql"
	"errors"
	"flowdesk/internal/ai"
	"flowdesk/internal/domain"
	"flowdesk/internal/repository"
	"flowdesk/internal/service"
	"flowdesk/internal/testkit"
	"flowdesk/internal/worker"
	"sync/atomic"
	"testing"
	"time"
)

func start(t *testing.T, f *testkit.Fixture) *worker.Manager {
	t.Helper()
	m := worker.New(f.Service)
	m.Start()
	t.Cleanup(func() {
		if !m.Stop(5 * time.Second) {
			t.Error("worker did not join")
		}
	})
	return m
}
func job(t *testing.T, f *testkit.Fixture, ticket string) domain.Job {
	t.Helper()
	v, e := repository.Ticket(context.Background(), f.Store.DB, ticket)
	if e != nil {
		t.Fatal(e)
	}
	cfg, _, e := repository.Settings(context.Background(), f.Store.DB)
	if e != nil {
		t.Fatal(e)
	}
	r, e := f.Service.Enqueue(context.Background(), f.Agent, ticket, "generate-"+ticket, service.JobBody{ExpectedRevision: v.Revision, ExpectedConfigVersion: &cfg.Version})
	if e != nil {
		t.Fatal(e)
	}
	return testkit.Data[domain.Job](t, r)
}
func succeeded(t *testing.T, f *testkit.Fixture, id string) {
	t.Helper()
	testkit.Wait(t, func() bool {
		j, e := repository.Job(context.Background(), f.Store.DB, id)
		return e == nil && j.Status == "succeeded"
	})
}
func TestDemoGeneratesImmutableHistoryWithZeroModelCalls(t *testing.T) {
	f := testkit.New(t)
	start(t, f)
	j := job(t, f, "1")
	succeeded(t, f, j.ID)
	d, e := f.Service.Drafts(context.Background(), f.Agent, "1")
	if e != nil || len(d) != 1 || !d[0].Valid || d[0].Mode != "demo" || len(d[0].Sources) == 0 {
		t.Fatal(d, e)
	}
	jfull, e := f.Service.Job(context.Background(), f.Agent, j.ID)
	if e != nil || len(jfull.Calls) != 0 {
		t.Fatal(jfull, e)
	}
	ticket, _ := repository.Ticket(context.Background(), f.Store.DB, "1")
	r, e := f.Service.Enqueue(context.Background(), f.Agent, "1", "new-run", service.JobBody{ExpectedRevision: ticket.Revision})
	if e != nil {
		t.Fatal(e)
	}
	newJob := testkit.Data[domain.Job](t, r)
	succeeded(t, f, newJob.ID)
	d, e = f.Service.Drafts(context.Background(), f.Agent, "1")
	if e != nil || len(d) != 2 || d[0].JobID != newJob.ID || d[1].JobID != j.ID {
		t.Fatal(d, e)
	}
}
func TestPublicCommentInvalidatesOldDraftButInternalNoteDoesNot(t *testing.T) {
	f := testkit.New(t)
	start(t, f)
	j := job(t, f, "1")
	succeeded(t, f, j.ID)
	d, _ := f.Service.Drafts(context.Background(), f.Agent, "1")
	ticket, _ := repository.Ticket(context.Background(), f.Store.DB, "1")
	_, e := f.Service.Comment(context.Background(), f.Agent, "1", "note", service.CommentBody{ExpectedRevision: ticket.Revision, Visibility: "internal", Body: "Internal only"})
	if e != nil {
		t.Fatal(e)
	}
	after, _ := f.Service.Drafts(context.Background(), f.Agent, "1")
	if !after[0].Valid {
		t.Fatal("internal note invalidated draft")
	}
	ticket, _ = repository.Ticket(context.Background(), f.Store.DB, "1")
	_, e = f.Service.Comment(context.Background(), f.Requester, "1", "public", service.CommentBody{ExpectedRevision: ticket.Revision, Visibility: "public", Body: "New information"})
	if e != nil {
		t.Fatal(e)
	}
	ticket, _ = repository.Ticket(context.Background(), f.Store.DB, "1")
	_, e = f.Service.DraftAction(context.Background(), f.Agent, "1", "old", "apply-suggestion", service.DraftAction{ExpectedRevision: ticket.Revision, DraftID: d[0].ID, ApplyCategory: true})
	testkit.Code(t, e, "DRAFT_STALE")
	after, _ = f.Service.Drafts(context.Background(), f.Agent, "1")
	if after[0].Valid || ticket.Status != "in_progress" {
		t.Fatal(after, ticket)
	}
}
func TestSourceChangesRequireFreshReviewTokenAndExplicitAcknowledgment(t *testing.T) {
	f := testkit.New(t)
	start(t, f)
	j := job(t, f, "1")
	succeeded(t, f, j.ID)
	ds, _ := f.Service.Drafts(context.Background(), f.Agent, "1")
	d := ds[0]
	source := d.Sources[0]
	a, _ := repository.Article(context.Background(), f.Store.DB, source.ArticleID)
	_, e := f.Service.PatchArticle(context.Background(), f.Agent, a.ID, "article-edit", service.ArticleBody{Title: a.Title, Body: a.Body + "\n新说明", Tags: a.Tags, ExpectedVersion: a.Version})
	if e != nil {
		t.Fatal(e)
	}
	ticket, _ := repository.Ticket(context.Background(), f.Store.DB, "1")
	b := service.DraftAction{DraftID: d.ID, ExpectedRevision: ticket.Revision, ReplyText: "人工核对后的正文", ExpectedReviewContext: d.ReviewContext.Token, Acknowledgements: []string{"sources_changed"}}
	_, e = f.Service.DraftAction(context.Background(), f.Agent, "1", "approve", "approve", b)
	testkit.Code(t, e, "REVIEW_CONTEXT_CHANGED")
	ds, _ = f.Service.Drafts(context.Background(), f.Agent, "1")
	b.ExpectedReviewContext = ds[0].ReviewContext.Token
	b.Acknowledgements = []string{}
	_, e = f.Service.DraftAction(context.Background(), f.Agent, "1", "approve", "approve", b)
	testkit.Code(t, e, "REVIEW_ACK_REQUIRED")
	b.Acknowledgements = []string{"sources_changed"}
	r, e := f.Service.DraftAction(context.Background(), f.Agent, "1", "approve", "approve", b)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = f.Service.DraftAction(context.Background(), f.Agent, "1", "approve", "approve", b); e != nil {
		t.Fatal("approval replay failed", e)
	}
	v := testkit.Data[domain.Ticket](t, r)
	if v.Status != "resolved" {
		t.Fatal(v)
	}
	detail, _ := f.Service.Ticket(context.Background(), f.Requester, "1")
	if len(detail.Replies) != 1 || detail.Replies[0].Body != b.ReplyText {
		t.Fatal(detail)
	}
}
func TestStoragePendingRetriesSQLWithoutExecutingEngineAgain(t *testing.T) {
	f := testkit.New(t)
	m := worker.New(f.Service)
	var fail atomic.Bool
	var runs atomic.Int64
	f.Store.CommitHook = func(tx *sql.Tx) error {
		if fail.Load() {
			var n int
			_ = tx.QueryRow("SELECT COUNT(*) FROM jobs WHERE status='succeeded'").Scan(&n)
			if n > 0 {
				return errors.New("disk failure")
			}
		}
		return tx.Commit()
	}
	m.Engine = func(context.Context, domain.JobRecord, string, []domain.Article, ai.Hooks) (ai.Result, error) {
		runs.Add(1)
		fail.Store(true)
		return ai.Result{Output: domain.AIOutput{Summary: "summary", Category: "other", Priority: "normal", Reply: "saved only once", EvidenceIDs: []string{}, NeedsMoreInformation: domain.Ptr(true)}, Sources: []domain.Source{}, Warnings: []string{"no_evidence"}}, nil
	}
	m.Start()
	t.Cleanup(func() { m.Stop(5 * time.Second) })
	j := job(t, f, "1")
	testkit.Wait(t, func() bool { return m.PendingCount() == 1 })
	stored, _ := repository.Job(context.Background(), f.Store.DB, j.ID)
	if stored.Status != "running" || !f.Service.StorageBlocked.Load() || m.Hint(j.ID) == nil {
		t.Fatal(stored)
	}
	fail.Store(false)
	out, e := m.RetrySave(context.Background(), f.Admin, j.ID)
	if e != nil || out.Status != "succeeded" || runs.Load() != 1 || m.PendingCount() != 0 {
		t.Fatal(out, e, runs.Load())
	}
	drafts, _ := f.Service.Drafts(context.Background(), f.Agent, "1")
	if len(drafts) != 1 || drafts[0].Reply != "saved only once" {
		t.Fatal(drafts)
	}
}
func TestCancelRejectsLateCompletion(t *testing.T) {
	f := testkit.New(t)
	m := worker.New(f.Service)
	entered := make(chan struct{})
	release := make(chan struct{})
	m.Engine = func(context.Context, domain.JobRecord, string, []domain.Article, ai.Hooks) (ai.Result, error) {
		close(entered)
		<-release
		return ai.Result{Output: domain.AIOutput{Summary: "late", Category: "other", Priority: "normal", Reply: "must not save", EvidenceIDs: []string{}, NeedsMoreInformation: domain.Ptr(true)}}, nil
	}
	m.Start()
	t.Cleanup(func() { m.Stop(5 * time.Second) })
	j := job(t, f, "1")
	<-entered
	_, e := f.Service.Cancel(context.Background(), f.Agent, j.ID, "cancel")
	if e != nil {
		t.Fatal(e)
	}
	close(release)
	testkit.Wait(t, func() bool {
		v, _ := repository.Job(context.Background(), f.Store.DB, j.ID)
		return v.Status == "canceled"
	})
	if !m.Stop(5 * time.Second) {
		t.Fatal("workers not joined")
	}
	drafts, _ := f.Service.Drafts(context.Background(), f.Agent, "1")
	if len(drafts) != 0 {
		t.Fatal("late completion created draft")
	}
}
func TestRestartInterruptsRunningButRecoversQueued(t *testing.T) {
	f := testkit.New(t)
	j := job(t, f, "1")
	queued := job(t, f, "2")
	if _, e := f.Store.DB.Exec("UPDATE jobs SET status='running',encrypted_secret=x'AB' WHERE id=?", j.ID); e != nil {
		t.Fatal(e)
	}
	if e := f.Store.RecoverJobs(context.Background()); e != nil {
		t.Fatal(e)
	}
	v, _ := repository.Job(context.Background(), f.Store.DB, j.ID)
	if v.Status != "failed" || v.Secret != nil || v.ErrorCode == nil || *v.ErrorCode != "RESTART_INTERRUPTED" {
		t.Fatal(v)
	}
	start(t, f)
	succeeded(t, f, queued.ID)
	drafts, _ := f.Service.Drafts(context.Background(), f.Agent, "1")
	if len(drafts) != 0 {
		t.Fatal("running task replayed")
	}
}
