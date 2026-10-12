package service_test

import (
	"context"
	"database/sql"
	"errors"
	"flowdesk/internal/domain"
	"flowdesk/internal/repository"
	"flowdesk/internal/service"
	"flowdesk/internal/testkit"
	"testing"
	"time"
)

func TestLoginCannotMintSessionAfterCredentialOrRoleEpochChange(t *testing.T) {
	for _, column := range []string{"credentials_version", "authorization_version"} {
		t.Run(column, func(t *testing.T) {
			f := testkit.New(t)
			entered, release := make(chan struct{}), make(chan struct{})
			f.Service.Auth.AfterVerify = func() { close(entered); <-release }
			result := make(chan error, 1)
			go func() {
				_, _, e := f.Service.Login(context.Background(), "agent@flowdesk.demo", "FlowDeskDemo!2026", "race-client")
				result <- e
			}()
			select {
			case <-entered:
			case <-time.After(10 * time.Second):
				t.Fatal("login barrier")
			}
			// Models an independently committed credential reset or permission change.
			_, e := f.Store.DB.Exec("UPDATE users SET "+column+"="+column+"+1 WHERE id=?", f.Agent.ID)
			close(release)
			if e != nil {
				t.Fatal(e)
			}
			testkit.Code(t, <-result, "LOGIN_FAILED")
			var n int
			if e = f.Store.DB.QueryRow("SELECT COUNT(*) FROM sessions WHERE user_id=? AND "+column+"=2", f.Agent.ID).Scan(&n); e != nil || n != 0 {
				t.Fatal(n, e)
			}
		})
	}
}
func TestCommittedButUnreadableOutcomeIsUnknownAndReplaysAfterReopen(t *testing.T) {
	f := testkit.New(t)
	f.Store.CommitHook = func(tx *sql.Tx) error {
		if e := tx.Commit(); e != nil {
			return e
		}
		_ = f.Store.DB.Close()
		return errors.New("ack lost and storage inaccessible")
	}
	b := service.TicketCreate{Title: "Durable unknown", Description: "No duplicate after storage returns", Category: "other", Priority: "normal"}
	_, e := f.Service.CreateTicket(context.Background(), f.Admin, "unknown-read", b)
	testkit.Code(t, e, "STORAGE_OUTCOME_UNKNOWN")
	if !f.Service.StorageBlocked.Load() {
		t.Fatal("paid calls not paused")
	}
	db, e := sql.Open("sqlite", repository.DSN(f.Config.DataDir+"/flowdesk.db", false))
	if e != nil {
		t.Fatal(e)
	}
	f.Store.DB = db
	db.SetMaxOpenConns(1)
	f.Store.CommitHook = nil
	t.Cleanup(func() { db.Close() })
	r, e := f.Service.CreateTicket(context.Background(), f.Admin, "unknown-read", b)
	if e != nil || !r.Replayed {
		t.Fatal(r, e)
	}
	var n int
	if e = db.QueryRow("SELECT COUNT(*) FROM tickets WHERE title=?", b.Title).Scan(&n); e != nil || n != 1 {
		t.Fatal(n, e)
	}
}
func TestFeeConsentBindsConfigurationVersionBeforeJobCreation(t *testing.T) {
	f := testkit.New(t)
	expected := int64(0)
	_, e := f.Service.Enqueue(context.Background(), f.Agent, "1", "stale-consent", service.JobBody{ExpectedRevision: 1, ExpectedConfigVersion: &expected})
	testkit.Code(t, e, "CONFIG_VERSION_CONFLICT")
	var n int
	if e = f.Store.DB.QueryRow("SELECT COUNT(*) FROM jobs").Scan(&n); e != nil || n != 0 {
		t.Fatal(n, e)
	}
	cfg, _, e := repository.Settings(context.Background(), f.Store.DB)
	if e != nil {
		t.Fatal(e)
	}
	expected = cfg.Version
	r, e := f.Service.Enqueue(context.Background(), f.Agent, "1", "current-consent", service.JobBody{ExpectedRevision: 1, ExpectedConfigVersion: &expected})
	if e != nil || testkit.Data[domain.Job](t, r).Status != "queued" {
		t.Fatal(r, e)
	}
}
