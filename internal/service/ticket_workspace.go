package service

import (
	"context"
	"database/sql"
	"flowdesk/internal/domain"
	"flowdesk/internal/repository"
)

// TicketWorkspace takes a single snapshot. Independent ticket, job and draft
// reads can straddle a worker's completion and permanently stop UI polling.
func (s *Service) TicketWorkspace(ctx context.Context, a domain.Actor, id string) (domain.TicketDetail, error) {
	var out domain.TicketDetail
	err := s.Store.Tx(ctx, func(tx *sql.Tx) error {
		current, err := repository.Session(ctx, tx, a.TokenHash)
		if err != nil {
			return err
		}
		ticket, err := repository.Ticket(ctx, tx, id)
		if err != nil {
			return err
		}
		if err = repository.CheckTicket(current.User, ticket); err != nil {
			return err
		}
		staff := domain.Staff(current.User)
		if !staff {
			ticket.ActiveJobID = nil
		}
		out.Ticket = ticket
		if out.Comments, err = repository.Comments(ctx, tx, id, staff); err != nil {
			return err
		}
		if out.Replies, err = repository.Replies(ctx, tx, id); err != nil {
			return err
		}
		if !staff {
			return nil
		}
		out.Work = &domain.TicketWork{Drafts: []domain.Draft{}, Jobs: []domain.Job{}}
		if out.Work.Drafts, err = ticketDrafts(ctx, tx, ticket); err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, "SELECT id FROM jobs WHERE ticket_id=? ORDER BY id DESC LIMIT 10", id)
		if err != nil {
			return err
		}
		ids := []string{}
		for rows.Next() {
			var n int64
			if err = rows.Scan(&n); err != nil {
				rows.Close()
				return err
			}
			ids = append(ids, repository.ID(n))
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		for _, jid := range ids {
			job, err := repository.Job(ctx, tx, jid)
			if err != nil {
				return err
			}
			if err = repository.JobDetails(ctx, tx, &job.Job); err != nil {
				return err
			}
			out.Work.Jobs = append(out.Work.Jobs, job.Job)
		}
		return nil
	})
	return out, DBError(err)
}

func ticketDrafts(ctx context.Context, q repository.Executor, ticket domain.Ticket) ([]domain.Draft, error) {
	out := []domain.Draft{}
	rows, err := q.QueryContext(ctx, "SELECT id FROM drafts WHERE ticket_id=? ORDER BY id DESC", ticket.ID)
	if err != nil {
		return nil, err
	}
	ids := []string{}
	for rows.Next() {
		var n int64
		if err = rows.Scan(&n); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, repository.ID(n))
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	for _, id := range ids {
		draft, err := repository.Draft(ctx, q, id)
		if err != nil {
			return nil, err
		}
		draft.Valid = draft.Version.Equal(ticket.Version) && draft.ReviewState == "pending" && ticket.Status != "resolved"
		if draft.ReviewContext, err = ReviewContext(ctx, q, draft); err != nil {
			return nil, err
		}
		out = append(out, draft)
	}
	return out, nil
}
