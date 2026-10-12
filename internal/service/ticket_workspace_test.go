package service_test

import (
	"context"
	"flowdesk/internal/domain"
	"flowdesk/internal/service"
	"flowdesk/internal/testkit"
	"testing"
)

func TestTicketWorkspaceKeepsJobAndTicketConsistentAndPrivate(t *testing.T) {
	f := testkit.New(t)
	ctx := context.Background()
	ticket, err := f.Service.Ticket(ctx, f.Admin, "1")
	if err != nil {
		t.Fatal(err)
	}
	result, err := f.Service.Enqueue(ctx, f.Admin, "1", "snapshot-job", service.JobBody{ExpectedRevision: ticket.Ticket.Revision, Strategy: "workflow"})
	if err != nil {
		t.Fatal(err)
	}
	queued := testkit.Data[domain.Job](t, result)
	for _, actor := range []domain.Actor{f.Admin, f.Agent} {
		view, err := f.Service.TicketWorkspace(ctx, actor, "1")
		if err != nil {
			t.Fatal(err)
		}
		if view.Work == nil || len(view.Work.Jobs) != 1 || view.Work.Jobs[0].ID != queued.ID || view.Work.Jobs[0].Status != "queued" || view.Ticket.ActiveJobID == nil || *view.Ticket.ActiveJobID != queued.ID {
			t.Fatal("inconsistent queued snapshot", view)
		}
	}
	private, err := f.Service.TicketWorkspace(ctx, f.Requester, "1")
	if err != nil || private.Work != nil || private.Ticket.ActiveJobID != nil {
		t.Fatal("requester sees staff-only work", private, err)
	}
	if _, err := f.Service.Cancel(ctx, f.Agent, queued.ID, "cancel-snapshot-job"); err != nil {
		t.Fatal(err)
	}
	for _, actor := range []domain.Actor{f.Admin, f.Agent} {
		view, err := f.Service.TicketWorkspace(ctx, actor, "1")
		if err != nil {
			t.Fatal(err)
		}
		if view.Ticket.ActiveJobID != nil || view.Work.Jobs[0].Status != "canceled" {
			t.Fatal("terminal job retained as active", view)
		}
	}
	if _, err := f.Store.DB.Exec("UPDATE users SET disabled=1 WHERE id=?", f.Agent.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Service.TicketWorkspace(ctx, f.Agent, "1"); err == nil {
		t.Fatal("revoked session returned a work snapshot")
	}
}
