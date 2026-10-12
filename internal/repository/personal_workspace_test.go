package repository_test

import (
	"context"
	"flowdesk/internal/platform"
	"flowdesk/internal/repository"
	"flowdesk/internal/testkit"
	"testing"
)

func TestPersonalWorkspaceStartsEmptyAndRestartsWithoutReseeding(t *testing.T) {
	c := platform.Config{Profile: "desktop", Workspace: true, DataDir: t.TempDir()}
	ctx := context.Background()
	if _, err := repository.Open(ctx, c, repository.Bootstrap{}); err == nil {
		t.Fatal("personal mode created public demo accounts")
	}
	s, err := repository.Open(ctx, c, repository.Bootstrap{InitAdmin: true, Email: "owner@example.test", DisplayName: "Owner", Password: "PersonalStrong!2026"})
	if err != nil {
		t.Fatal(err)
	}
	w := s.WorkspaceID
	for table, want := range map[string]int{"users": 1, "tickets": 0, "knowledge_articles": 0, "jobs": 0} {
		var count int
		if err := s.DB.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&count); err != nil || count != want {
			t.Fatal(table, count, err)
		}
	}
	s.DB.Close()
	s, err = repository.Open(ctx, c, repository.Bootstrap{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB.Close()
	if s.WorkspaceID != w {
		t.Fatal("restart replaced workspace")
	}
	var users, tickets int
	s.DB.QueryRow("SELECT COUNT(*) FROM users").Scan(&users)
	s.DB.QueryRow("SELECT COUNT(*) FROM tickets").Scan(&tickets)
	if users != 1 || tickets != 0 {
		t.Fatal("restart inserted demo data", users, tickets)
	}
}

func TestPersonalWorkspaceRejectsAnExistingDemoWithoutRemovingData(t *testing.T) {
	f := testkit.New(t)
	c := f.Config
	c.Workspace = true
	if _, err := repository.Open(context.Background(), c, repository.Bootstrap{}); err == nil || err.Error() != "WORKSPACE_DEMO_ACCOUNTS" {
		t.Fatal(err)
	}
	var users, tickets int
	f.Store.DB.QueryRow("SELECT COUNT(*) FROM users").Scan(&users)
	f.Store.DB.QueryRow("SELECT COUNT(*) FROM tickets").Scan(&tickets)
	if users != 3 || tickets != 4 {
		t.Fatal("existing demo was modified", users, tickets)
	}
}
