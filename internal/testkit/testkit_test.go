package testkit_test

import (
	"flowdesk/internal/testkit"
	"os"
	"path/filepath"
	"testing"
)

func TestClonedFixturesKeepIndependentDataKeysWorkspacesAndSessions(t *testing.T) {
	a, b := testkit.New(t), testkit.New(t)
	if a.Store.WorkspaceID == b.Store.WorkspaceID || a.Admin.TokenHash == b.Admin.TokenHash {
		t.Fatal("fixture identity or session is shared")
	}
	x, err := os.ReadFile(filepath.Join(a.Config.DataDir, "master.key"))
	if err != nil {
		t.Fatal(err)
	}
	y, err := os.ReadFile(filepath.Join(b.Config.DataDir, "master.key"))
	if err != nil {
		t.Fatal(err)
	}
	if string(x) == string(y) {
		t.Fatal("fixture key is shared")
	}
	if _, err = a.Store.DB.Exec("UPDATE tickets SET title='Changed in one fixture' WHERE id=1"); err != nil {
		t.Fatal(err)
	}
	var title string
	if err = b.Store.DB.QueryRow("SELECT title FROM tickets WHERE id=1").Scan(&title); err != nil || title == "Changed in one fixture" {
		t.Fatal("fixture mutation leaked", err)
	}
}
