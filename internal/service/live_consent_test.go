package service_test

import (
	"context"
	"flowdesk/internal/repository"
	"flowdesk/internal/service"
	"flowdesk/internal/testkit"
	"testing"
)

func TestLiveJobCannotBeCreatedWithoutConfirmingCurrentConfiguration(t *testing.T) {
	f := testkit.New(t)
	ctx := context.Background()
	cfg, _, err := repository.Settings(ctx, f.Store.DB)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Mode = "live"
	cfg.Model = "local-test-model"
	cfg.BaseURL = "http://127.0.0.1:12345/v1"
	if _, err = f.Service.PutSettings(ctx, f.Admin, "configure-consent", service.SettingsBody{AIConfig: cfg, APIKey: "synthetic-local-test-key"}); err != nil {
		t.Fatal(err)
	}
	_, err = f.Service.Enqueue(ctx, f.Agent, "1", "missing-consent", service.JobBody{ExpectedRevision: 1})
	testkit.Code(t, err, "LIVE_CONSENT_REQUIRED")
	var count int
	if err := f.Store.DB.QueryRow("SELECT COUNT(*) FROM jobs").Scan(&count); err != nil || count != 0 {
		t.Fatal("job created without consent", count, err)
	}
	view, err := f.Service.TicketWorkspace(ctx, f.Agent, "1")
	if err != nil || view.Ticket.ActiveJobID != nil {
		t.Fatal("ticket changed without consent", view, err)
	}
}
