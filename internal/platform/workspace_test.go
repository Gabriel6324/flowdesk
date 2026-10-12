package platform_test

import (
	"flowdesk/internal/platform"
	"path/filepath"
	"testing"
)

func TestPersonalWorkspaceUsesSeparateDefaultAndKeepsExplicitDirectory(t *testing.T) {
	base := platform.DefaultDir()
	for _, explicit := range []bool{false, true} {
		c := platform.Config{Profile: "desktop", Workspace: true}
		want := filepath.Join(base, "workspace")
		if explicit {
			want = t.TempDir()
			c.DataDir = want
		}
		if err := c.Validate(false); err != nil {
			t.Fatal(err)
		}
		if c.DataDir != want || c.WorkspaceMode() != "personal" {
			t.Fatal(c.DataDir, want)
		}
	}
	c := platform.Config{Profile: "server", Workspace: true}
	if err := c.Validate(false); err == nil {
		t.Fatal("mixed workspace profiles accepted")
	}
}
