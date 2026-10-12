package repository_test

import (
	"context"
	"database/sql"
	"errors"
	"flowdesk/internal/platform"
	"flowdesk/internal/repository"
	"flowdesk/internal/testkit"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPreflightNeverRewritesRejectedWorkspace(t *testing.T) {
	for _, kind := range []string{"profile", "newer", "checksum"} {
		t.Run(kind, func(t *testing.T) {
			f := testkit.New(t)
			if kind == "newer" {
				_, e := f.Store.DB.Exec("INSERT INTO migrations VALUES(999,'future','now')")
				if e != nil {
					t.Fatal(e)
				}
			}
			if kind == "checksum" {
				_, e := f.Store.DB.Exec("UPDATE migrations SET checksum='changed'")
				if e != nil {
					t.Fatal(e)
				}
			}
			f.Store.DB.Close()
			path := filepath.Join(f.Config.DataDir, "flowdesk.db")
			before, _ := os.ReadFile(path)
			key, _ := os.ReadFile(filepath.Join(f.Config.DataDir, "master.key"))
			profile := "desktop"
			if kind == "profile" {
				profile = "server"
			}
			_, _, e := repository.Preflight(context.Background(), path, profile)
			if e == nil {
				t.Fatal("accepted rejected metadata")
			}
			after, _ := os.ReadFile(path)
			afterKey, _ := os.ReadFile(filepath.Join(f.Config.DataDir, "master.key"))
			if string(before) != string(after) || string(key) != string(afterKey) {
				t.Fatal("preflight changed files")
			}
		})
	}
}
func TestWALBackupIncludesCommittedRowsAndKey(t *testing.T) {
	f := testkit.New(t)
	_, e := f.Store.DB.Exec("INSERT INTO comments(ticket_id,cycle_no,author_id,visibility,body,created_at) VALUES(1,1,1,'public','WAL committed','now')")
	if e != nil {
		t.Fatal(e)
	}
	p, e := f.Store.Backup(context.Background(), true)
	if e != nil {
		t.Fatal(e)
	}
	db, e := sql.Open("sqlite", repository.DSN(filepath.Join(p, "flowdesk.db"), true))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	var body string
	if e = db.QueryRow("SELECT body FROM comments ORDER BY id DESC LIMIT 1").Scan(&body); e != nil || body != "WAL committed" {
		t.Fatal(body, e)
	}
	key, _ := os.ReadFile(filepath.Join(f.Config.DataDir, "master.key"))
	copy, _ := os.ReadFile(filepath.Join(p, "master.key"))
	if string(key) != string(copy) {
		t.Fatal("key not paired")
	}
}
func TestKeystoreRecoveryResumesEveryStageOnce(t *testing.T) {
	for _, stage := range []string{"prepared", "committed", "installed"} {
		t.Run(stage, func(t *testing.T) {
			f := testkit.New(t)
			old, _ := os.ReadFile(filepath.Join(f.Config.DataDir, "master.key"))
			if e := f.Store.RecoverKeystore(context.Background(), func(s string) error {
				if s == stage {
					return errors.New("injected crash")
				}
				return nil
			}); e == nil {
				t.Fatal("fault not injected")
			}
			if e := f.Store.RecoverKeystore(context.Background(), nil); e != nil {
				t.Fatal(e)
			}
			key, e := os.ReadFile(filepath.Join(f.Config.DataDir, "master.key"))
			if e != nil || len(key) != 32 || string(key) == string(old) {
				t.Fatal("new key missing")
			}
			var n, version, tickets int
			f.Store.DB.QueryRow("SELECT COUNT(*) FROM keystore_recoveries").Scan(&n)
			f.Store.DB.QueryRow("SELECT version FROM ai_settings").Scan(&version)
			f.Store.DB.QueryRow("SELECT COUNT(*) FROM tickets").Scan(&tickets)
			if n != 1 || version != 2 || tickets != 4 {
				t.Fatalf("%d %d %d", n, version, tickets)
			}
			if _, e = os.Stat(filepath.Join(f.Config.DataDir, "keystore-recovery.json")); !os.IsNotExist(e) {
				t.Fatal("marker retained")
			}
		})
	}
}
func TestRecoveryMissingTemporaryKeyStops(t *testing.T) {
	f := testkit.New(t)
	_ = f.Store.RecoverKeystore(context.Background(), func(s string) error { return errors.New("stop") })
	os.Remove(filepath.Join(f.Config.DataDir, "master.key.recovery"))
	if e := f.Store.RecoverKeystore(context.Background(), nil); e == nil || !strings.Contains(e.Error(), "INCOMPLETE") {
		t.Fatal(e)
	}
}
func TestInstanceLock(t *testing.T) {
	p := filepath.Join(t.TempDir(), "instance.lock")
	l, e := platform.AcquireLock(p)
	if e != nil {
		t.Fatal(e)
	}
	defer l.Close()
	if next, e := platform.AcquireLock(p); e == nil {
		next.Close()
		t.Fatal("double instance")
	}
}
func TestServerBootstrapHasNoDesktopAccounts(t *testing.T) {
	c := platform.Config{Profile: "server", DataDir: t.TempDir(), Listen: "127.0.0.1:0", PublicOrigin: "https://example.test"}
	if _, e := repository.Open(context.Background(), c, repository.Bootstrap{}); e == nil {
		t.Fatal("started without bootstrap")
	}
	s, e := repository.Open(context.Background(), c, repository.Bootstrap{InitAdmin: true, Email: "owner@example.test", DisplayName: "Owner", Password: "ServerStrong!2026"})
	if e != nil {
		t.Fatal(e)
	}
	defer s.DB.Close()
	var n int
	s.DB.QueryRow("SELECT COUNT(*) FROM users").Scan(&n)
	if n != 1 {
		t.Fatal(n)
	}
	var demo int
	s.DB.QueryRow("SELECT COUNT(*) FROM users WHERE email LIKE '%flowdesk.demo'").Scan(&demo)
	if demo != 0 {
		t.Fatal("desktop passwords seeded")
	}
}
