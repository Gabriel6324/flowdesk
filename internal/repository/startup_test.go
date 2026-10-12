package repository_test

import (
	"context"
	"database/sql"
	"flowdesk/internal/platform"
	"flowdesk/internal/repository"
	"flowdesk/internal/testkit"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDSNUsesCanonicalLocalFileURI(t *testing.T) {
	cases := []struct{ name, path, want string }{
		{"Windows backslashes", `C:\Users\测试 用户\AppData\Local\FlowDesk\flowdesk.db`, "/C:/Users/测试 用户/AppData/Local/FlowDesk/flowdesk.db"},
		{"Windows forward slashes", "D:/FlowDesk data/flowdesk.db", "/D:/FlowDesk data/flowdesk.db"},
		{"Windows mixed separators", `E:\FlowDesk data/数据库\flowdesk.db`, "/E:/FlowDesk data/数据库/flowdesk.db"},
		{"Windows extended local path", `\\?\C:\Users\测试\FlowDesk\flowdesk.db`, "/C:/Users/测试/FlowDesk/flowdesk.db"},
		{"Reserved URL characters", `C:\FlowDesk #%& data\flowdesk.db`, "/C:/FlowDesk #%& data/flowdesk.db"},
		{"Unix literal backslash", `/tmp/FlowDesk\data/flowdesk.db`, `/tmp/FlowDesk\data/flowdesk.db`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, ro := range []bool{false, true} {
				u, e := url.Parse(repository.DSN(tc.path, ro))
				if e != nil || u.Scheme != "file" || u.Host != "" || u.Path != tc.want || u.Fragment != "" {
					t.Fatalf("wrong database URI: %q, %v", repository.DSN(tc.path, ro), e)
				}
				if (u.Query().Get("mode") == "ro") != ro || len(u.Query()["_pragma"]) != 2 {
					t.Fatal("read-only or connection pragmas lost")
				}
			}
		})
	}
}

func TestOpenResumesPristineInterruptedInitialization(t *testing.T) {
	for _, kind := range []string{"zero-byte", "empty-SQLite", "legacy-schema-only"} {
		t.Run(kind, func(t *testing.T) {
			ctx := context.Background()
			dir := filepath.Join(t.TempDir(), "测试 用户 data #% &'")
			if e := platform.PrivateDir(dir); e != nil {
				t.Fatal(e)
			}
			path := filepath.Join(dir, "flowdesk.db")
			if e := os.WriteFile(path, nil, 0600); e != nil {
				t.Fatal(e)
			}
			if kind != "zero-byte" {
				db, e := sql.Open("sqlite", repository.DSN(path, false))
				if e != nil {
					t.Fatal(e)
				}
				if _, e = db.Exec("PRAGMA journal_mode=WAL"); e != nil {
					t.Fatal(e)
				}
				if kind == "legacy-schema-only" {
					schema, e := os.ReadFile("schema.sql")
					if e != nil {
						t.Fatal(e)
					}
					if _, e = db.Exec(string(schema)); e != nil {
						t.Fatal(e)
					}
					if _, e = db.Exec("INSERT INTO migrations VALUES(1,?,'interrupted')", repository.Sum(string(schema))); e != nil {
						t.Fatal(e)
					}
				}
				if e = db.Close(); e != nil {
					t.Fatal(e)
				}
			}
			if _, e := platform.LoadKey(dir, true); e != nil {
				t.Fatal(e)
			}
			oldKey, _ := os.ReadFile(filepath.Join(dir, "master.key"))
			w, exists, e := repository.Preflight(ctx, path, "desktop")
			if e != nil || w != "" || !exists {
				t.Fatalf("pristine preflight: %q %v %v", w, exists, e)
			}
			c := platform.Config{Profile: "desktop", DataDir: dir}
			s, e := repository.Open(ctx, c, repository.Bootstrap{})
			if e != nil {
				t.Fatal(e)
			}
			if s.WorkspaceID == "" {
				t.Fatal("workspace not initialized")
			}
			var tickets, users int
			if e = s.DB.QueryRow("SELECT COUNT(*) FROM tickets").Scan(&tickets); e != nil {
				t.Fatal(e)
			}
			if e = s.DB.QueryRow("SELECT COUNT(*) FROM users").Scan(&users); e != nil || tickets != 4 || users != 3 {
				t.Fatalf("seed %d %d %v", tickets, users, e)
			}
			workspaceID := s.WorkspaceID
			if e = s.DB.Close(); e != nil {
				t.Fatal(e)
			}
			s, e = repository.Open(ctx, c, repository.Bootstrap{})
			if e != nil {
				t.Fatal(e)
			}
			defer s.DB.Close()
			if s.WorkspaceID != workspaceID {
				t.Fatal("restart created another workspace")
			}
			if e = s.DB.QueryRow("SELECT COUNT(*) FROM tickets").Scan(&tickets); e != nil || tickets != 4 {
				t.Fatal("restart duplicated seed", e)
			}
			key, _ := os.ReadFile(filepath.Join(dir, "master.key"))
			if string(key) != string(oldKey) {
				t.Fatal("existing encryption key overwritten")
			}
		})
	}
}

func TestPreflightRejectsForeignAndCorruptFilesWithoutRewriting(t *testing.T) {
	for _, kind := range []string{"foreign-table", "foreign-user-version", "foreign-application-id", "corrupt-file"} {
		t.Run(kind, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "flowdesk.db")
			if kind == "corrupt-file" {
				if e := os.WriteFile(path, []byte("this is not a SQLite database"), 0600); e != nil {
					t.Fatal(e)
				}
			} else {
				db, e := sql.Open("sqlite", repository.DSN(path, false))
				if e != nil {
					t.Fatal(e)
				}
				statement := map[string]string{"foreign-table": "CREATE TABLE valuable_data(value TEXT); INSERT INTO valuable_data VALUES('preserve')", "foreign-user-version": "PRAGMA user_version=5", "foreign-application-id": "PRAGMA application_id=1234"}[kind]
				if _, e = db.Exec(statement); e != nil {
					t.Fatal(e)
				}
				if e = db.Close(); e != nil {
					t.Fatal(e)
				}
			}
			before, _ := os.ReadFile(path)
			_, exists, e := repository.Preflight(context.Background(), path, "desktop")
			if e == nil || !exists {
				t.Fatal("adopted foreign/corrupt database")
			}
			if kind == "corrupt-file" && !strings.HasPrefix(e.Error(), "DATABASE_CORRUPT") {
				t.Fatal("corruption disguised as a version mismatch", e)
			}
			after, _ := os.ReadFile(path)
			if string(before) != string(after) {
				t.Fatal("rejected database rewritten")
			}
		})
	}
}

func TestFailedFirstBootstrapLeavesPristineDatabaseForRetry(t *testing.T) {
	ctx := context.Background()
	c := platform.Config{Profile: "server", DataDir: t.TempDir()}
	_, e := repository.Open(ctx, c, repository.Bootstrap{InitAdmin: true, Email: "invalid", DisplayName: "Owner", Password: "StrongPassword!2026"})
	if e == nil {
		t.Fatal("invalid bootstrap accepted")
	}
	path := filepath.Join(c.DataDir, "flowdesk.db")
	db, e := sql.Open("sqlite", repository.DSN(path, true))
	if e != nil {
		t.Fatal(e)
	}
	var n int
	if e = db.QueryRow("SELECT COUNT(*) FROM sqlite_schema").Scan(&n); e != nil {
		t.Fatal(e)
	}
	db.Close()
	if n != 0 {
		t.Fatal("failed initialization committed half a schema")
	}
	if _, e = repository.Open(ctx, c, repository.Bootstrap{}); e == nil || !strings.HasPrefix(e.Error(), "SERVER_BOOTSTRAP_REQUIRED") {
		t.Fatal("empty server silently seeded desktop users", e)
	}
	s, e := repository.Open(ctx, c, repository.Bootstrap{InitAdmin: true, Email: "owner@example.test", DisplayName: "Owner", Password: "StrongPassword!2026"})
	if e != nil {
		t.Fatal(e)
	}
	defer s.DB.Close()
	if e = s.DB.QueryRow("SELECT COUNT(*) FROM users").Scan(&n); e != nil || n != 1 {
		t.Fatal("bootstrap retry duplicated/seeded wrong users", n, e)
	}
}

func TestMissingWorkspaceMetadataNeverReseedsBusinessData(t *testing.T) {
	f := testkit.New(t)
	if _, e := f.Store.DB.Exec("DELETE FROM workspace_meta"); e != nil {
		t.Fatal(e)
	}
	f.Store.DB.Close()
	path := filepath.Join(f.Config.DataDir, "flowdesk.db")
	before, _ := os.ReadFile(path)
	key, _ := os.ReadFile(filepath.Join(f.Config.DataDir, "master.key"))
	_, exists, e := repository.Preflight(context.Background(), path, "desktop")
	if !exists || e == nil || !strings.HasPrefix(e.Error(), "SCHEMA_INVALID") {
		t.Fatal("metadata loss accepted as new workspace", e)
	}
	after, _ := os.ReadFile(path)
	afterKey, _ := os.ReadFile(filepath.Join(f.Config.DataDir, "master.key"))
	if string(before) != string(after) || string(key) != string(afterKey) {
		t.Fatal("workspace/key changed on rejection")
	}
}

func TestLegacyInitializationRejectsUnexpectedSchemaObjects(t *testing.T) {
	for _, kind := range []string{"table", "index", "trigger", "view"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "flowdesk.db")
			db, e := sql.Open("sqlite", repository.DSN(path, false))
			if e != nil {
				t.Fatal(e)
			}
			schema, e := os.ReadFile("schema.sql")
			if e != nil {
				t.Fatal(e)
			}
			if _, e = db.Exec(string(schema)); e != nil {
				t.Fatal(e)
			}
			if _, e = db.Exec("INSERT INTO migrations VALUES(1,?,'interrupted')", repository.Sum(string(schema))); e != nil {
				t.Fatal(e)
			}
			statement := map[string]string{
				"table":   "CREATE TABLE valuable_data(value TEXT); INSERT INTO valuable_data VALUES('preserve')",
				"index":   "CREATE INDEX custom_index ON runtime_probe(probe_seq)",
				"trigger": "CREATE TRIGGER custom_trigger AFTER UPDATE ON runtime_probe BEGIN SELECT 1; END",
				"view":    "CREATE VIEW custom_view AS SELECT probe_seq FROM runtime_probe",
			}[kind]
			if _, e = db.Exec(statement); e != nil {
				t.Fatal(e)
			}
			db.Close()
			before, _ := os.ReadFile(path)
			_, exists, e := repository.Preflight(context.Background(), path, "desktop")
			if !exists || e == nil || !strings.HasPrefix(e.Error(), "SCHEMA_INVALID") {
				t.Fatal("nonstandard schema accepted as pristine", e)
			}
			after, _ := os.ReadFile(path)
			if string(before) != string(after) {
				t.Fatal("nonstandard database changed")
			}
		})
	}
}
