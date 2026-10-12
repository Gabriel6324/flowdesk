package testkit

import (
	"context"
	"encoding/json"
	"flowdesk/internal/auth"
	"flowdesk/internal/domain"
	"flowdesk/internal/platform"
	"flowdesk/internal/repository"
	"flowdesk/internal/service"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

type Fixture struct {
	Store                   *repository.Store
	Service                 *service.Service
	Config                  platform.Config
	Keys                    *platform.Keystore
	Admin, Agent, Requester domain.Actor
}

var seedOnce sync.Once
var seedBytes []byte
var seedError error

// Use one genuinely initialized database per test binary. Cloning its immutable
// bytes avoids repeating password derivation in unrelated race/transaction
// tests. Every fixture still gets its own database, key, workspace and sessions.
// Login-specific tests continue to call Login with the production iteration count.
func fixtureSeed(t *testing.T) []byte {
	t.Helper()
	seedOnce.Do(func() {
		dir := t.TempDir()
		store, err := repository.Open(context.Background(), platform.Config{Profile: "desktop", DataDir: dir}, repository.Bootstrap{})
		if err != nil {
			seedError = err
			return
		}
		if err = store.DB.Close(); err != nil {
			seedError = err
			return
		}
		seedBytes, seedError = os.ReadFile(filepath.Join(dir, "flowdesk.db"))
	})
	if seedError != nil {
		t.Fatal(seedError)
	}
	return seedBytes
}

func New(t *testing.T) *Fixture {
	t.Helper()
	c := platform.Config{Profile: "desktop", DataDir: t.TempDir(), Listen: "127.0.0.1:0", PublicOrigin: "http://127.0.0.1:8787", NoBrowser: true}
	k, e := platform.LoadKey(c.DataDir, true)
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(filepath.Join(c.DataDir, "flowdesk.db"), fixtureSeed(t), 0600); e != nil {
		t.Fatal(e)
	}
	s, e := repository.Open(context.Background(), c, repository.Bootstrap{})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { s.DB.Close() })
	s.WorkspaceID = auth.RandomHex(16)
	if _, e = s.DB.Exec("UPDATE workspace_meta SET workspace_id=? WHERE singleton_id=1", s.WorkspaceID); e != nil {
		t.Fatal(e)
	}
	svc := service.New(s, c, k)
	f := &Fixture{Store: s, Service: svc, Config: c, Keys: k}
	f.Admin = f.authenticatedFixture(t, "1")
	f.Agent = f.authenticatedFixture(t, "2")
	f.Requester = f.authenticatedFixture(t, "3")
	return f
}
func (f *Fixture) authenticatedFixture(t *testing.T, id string) domain.Actor {
	t.Helper()
	user, err := repository.User(context.Background(), f.Store.DB, id)
	if err != nil {
		t.Fatal(err)
	}
	hash := auth.TokenHash(auth.RandomHex(32))
	if _, err = f.Store.DB.Exec("INSERT INTO sessions VALUES(?,?,?,?,?)", hash, user.ID, user.CredentialsVersion, user.AuthorizationVersion, time.Now().UTC().Add(12*time.Hour).Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	actor, err := f.Service.Actor(context.Background(), hash)
	if err != nil {
		t.Fatal(err)
	}
	return actor
}
func (f *Fixture) Login(t *testing.T, role string) domain.Actor {
	t.Helper()
	_, token, e := f.Service.Login(context.Background(), role+"@flowdesk.demo", "FlowDeskDemo!2026", "127.0.0.1")
	if e != nil {
		t.Fatal(e)
	}
	a, e := f.Service.Actor(context.Background(), auth.TokenHash(token))
	if e != nil {
		t.Fatal(e)
	}
	return a
}
func Code(t *testing.T, e error, code string) {
	t.Helper()
	if e == nil || domain.AsError(e).Code != code {
		t.Fatalf("want %s, got %v", code, e)
	}
}
func Data[T any](t *testing.T, r service.Result) T {
	t.Helper()
	var v T
	if e := json.Unmarshal(r.Data, &v); e != nil {
		t.Fatal(e)
	}
	return v
}
func Wait(t *testing.T, fn func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if fn() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition not reached")
}
