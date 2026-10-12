package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	contract "flowdesk/api"
	"flowdesk/internal/domain"
	"flowdesk/internal/httpapi"
	"flowdesk/internal/repository"
	"flowdesk/internal/testkit"
	"flowdesk/internal/worker"
	webassets "flowdesk/web"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type fixture struct {
	f      *testkit.Fixture
	a      *httpapi.API
	m      *worker.Manager
	cookie *http.Cookie
}

func setup(t *testing.T) *fixture {
	t.Helper()
	f := testkit.New(t)
	m := worker.New(f.Service)
	a := httpapi.New(f.Service, m, webassets.FS(), slog.New(slog.NewJSONHandler(io.Discard, nil)))
	v := &fixture{f: f, a: a, m: m}
	r := v.call(t, "POST", "/api/auth/login", "/api/auth/login", map[string]any{"email": "admin@flowdesk.demo", "password": "FlowDeskDemo!2026"}, nil, 200)
	v.cookie = r.Result().Cookies()[0]
	return v
}
func (v *fixture) call(t *testing.T, method, path, template string, body any, cookie *http.Cookie, want int) *httptest.ResponseRecorder {
	t.Helper()
	var b []byte
	if body != nil {
		b = []byte(repository.JSON(body))
	}
	r := httptest.NewRequest(method, v.f.Config.PublicOrigin+path, bytes.NewReader(b))
	if body != nil {
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Origin", v.f.Config.PublicOrigin)
		r.Header.Set("Idempotency-Key", repository.Sum(method+path+string(b)))
	}
	if cookie != nil {
		r.AddCookie(cookie)
	}
	w := httptest.NewRecorder()
	v.a.ServeHTTP(w, r)
	if w.Code != want {
		t.Fatalf("%s %s want %d got %d: %s", method, path, want, w.Code, w.Body.String())
	}
	if e := contract.ValidateResponse(method, template, w.Code, w.Body.Bytes()); e != nil {
		t.Fatalf("%s %s contract: %v\n%s", method, path, e, w.Body.String())
	}
	if w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("X-Request-ID") == "" {
		t.Fatal("unsafe API headers")
	}
	return w
}
func data[T any](t *testing.T, w *httptest.ResponseRecorder) T {
	t.Helper()
	var v struct {
		Data T `json:"data"`
	}
	if e := json.Unmarshal(w.Body.Bytes(), &v); e != nil {
		t.Fatal(e)
	}
	return v.Data
}
func rev(t *testing.T, v *fixture, id string) int64 {
	t.Helper()
	x, e := repository.Ticket(context.Background(), v.f.Store.DB, id)
	if e != nil {
		t.Fatal(e)
	}
	return x.Revision
}
func TestAllRoutedOperationsHaveMatchingContracts(t *testing.T) {
	v := setup(t)
	seen := map[string]bool{}
	for _, r := range v.a.Routes() {
		key := strings.ToLower(r[0]) + " " + r[1]
		if _, ok := contract.Doc.Paths[r[1]][strings.ToLower(r[0])]; !ok {
			t.Fatal("undocumented route", key)
		}
		seen[key] = true
	}
	for path, methods := range contract.Doc.Paths {
		for method := range methods {
			if !seen[method+" "+path] {
				t.Fatal("contract has no route", method, path)
			}
		}
	}
	if len(seen) != 37 {
		t.Fatal("route inventory changed", len(seen))
	}
}
func TestHTTPBusinessFixturesMatchSchemas(t *testing.T) {
	v := setup(t)
	c := v.cookie
	call := func(method, path, template string, body any, want int) *httptest.ResponseRecorder {
		return v.call(t, method, path, template, body, c, want)
	}
	call("GET", "/api/health", "/api/health", nil, 200)
	call("GET", "/api/ready", "/api/ready", nil, 200)
	call("GET", "/api/auth/me", "/api/auth/me", nil, 200)
	call("GET", "/api/dashboard", "/api/dashboard", nil, 200)
	call("GET", "/api/tickets", "/api/tickets", nil, 200)
	ticket := data[domain.Ticket](t, call("POST", "/api/tickets", "/api/tickets", map[string]any{"title": "API 401", "description": "Authentication problem", "category": "other", "priority": "normal"}, 201))
	id := ticket.ID
	base := "/api/tickets/" + id
	call("GET", base, "/api/tickets/{id}", nil, 200)
	call("PATCH", base, "/api/tickets/{id}", map[string]any{"expected_revision": ticket.Revision, "title": "API returns 401"}, 200)
	call("POST", base+"/comments", "/api/tickets/{id}/comments", map[string]any{"expected_revision": rev(t, v, id), "visibility": "public", "body": "Request id safe-example"}, 201)
	call("GET", base+"/comments", "/api/tickets/{id}/comments", nil, 200)
	first := data[domain.Job](t, call("POST", base+"/jobs", "/api/tickets/{id}/jobs", map[string]any{"expected_revision": rev(t, v, id), "strategy": "workflow"}, 202))
	call("POST", "/api/jobs/"+first.ID+"/cancel", "/api/jobs/{id}/cancel", map[string]any{}, 200)
	second := data[domain.Job](t, call("POST", "/api/jobs/"+first.ID+"/retry", "/api/jobs/{id}/retry", map[string]any{"expected_revision": rev(t, v, id)}, 202))
	v.m.Start()
	defer v.m.Stop(5 * time.Second)
	testkit.Wait(t, func() bool {
		j, e := repository.Job(context.Background(), v.f.Store.DB, second.ID)
		return e == nil && j.Status == "succeeded"
	})
	call("GET", "/api/jobs", "/api/jobs", nil, 200)
	call("GET", "/api/jobs/"+second.ID, "/api/jobs/{id}", nil, 200)
	ds := data[[]domain.Draft](t, call("GET", base+"/drafts", "/api/tickets/{id}/drafts", nil, 200))
	d := ds[0]
	call("POST", base+"/apply-suggestion", "/api/tickets/{id}/apply-suggestion", map[string]any{"expected_revision": rev(t, v, id), "draft_id": d.ID, "apply_category": true}, 200)
	call("POST", base+"/approve", "/api/tickets/{id}/approve", map[string]any{"expected_revision": rev(t, v, id), "draft_id": d.ID, "reply_text": "A reviewed public reply", "expected_review_context": d.ReviewContext.Token, "acknowledgements": d.ReviewContext.Required}, 200)
	call("POST", base+"/reopen", "/api/tickets/{id}/reopen", map[string]any{"expected_revision": rev(t, v, id)}, 200)
	third := data[domain.Job](t, call("POST", base+"/jobs", "/api/tickets/{id}/jobs", map[string]any{"expected_revision": rev(t, v, id)}, 202))
	testkit.Wait(t, func() bool {
		j, e := repository.Job(context.Background(), v.f.Store.DB, third.ID)
		return e == nil && j.Status == "succeeded"
	})
	ds = data[[]domain.Draft](t, call("GET", base+"/drafts", "/api/tickets/{id}/drafts", nil, 200))
	call("POST", base+"/reject-draft", "/api/tickets/{id}/reject-draft", map[string]any{"expected_revision": rev(t, v, id), "draft_id": ds[0].ID, "reason_code": "incomplete"}, 200)
	call("POST", base+"/reply", "/api/tickets/{id}/reply", map[string]any{"expected_revision": rev(t, v, id), "reply_text": "Manual final reply"}, 200)
	call("GET", "/api/audit?ticket_id="+id, "/api/audit", nil, 200)
	call("GET", "/api/tickets/export?format=json", "/api/tickets/export", nil, 200)
	call("GET", "/api/knowledge", "/api/knowledge", nil, 200)
	article := data[domain.Article](t, call("POST", "/api/knowledge", "/api/knowledge", map[string]any{"title": "New public FAQ", "body": "Only public information", "tags": []string{"FAQ"}}, 201))
	call("GET", "/api/knowledge/"+article.ID, "/api/knowledge/{id}", nil, 200)
	call("PATCH", "/api/knowledge/"+article.ID, "/api/knowledge/{id}", map[string]any{"title": article.Title, "body": article.Body, "tags": article.Tags, "expected_version": 1, "archived": true}, 200)
	call("GET", "/api/users", "/api/users", nil, 200)
	u := data[domain.User](t, call("POST", "/api/users", "/api/users", map[string]any{"email": "new@example.test", "display_name": "new", "role": "requester", "password": "Temporary!2026"}, 201))
	call("PATCH", "/api/users/"+u.ID, "/api/users/{id}", map[string]any{"expected_revision": u.Revision, "display_name": "Updated", "role": "agent", "disabled": false}, 200)
	s := data[domain.Settings](t, call("GET", "/api/settings/ai", "/api/settings/ai", nil, 200))
	call("PUT", "/api/settings/ai", "/api/settings/ai", s.AIConfig, 200)
	call("POST", "/api/settings/ai/test", "/api/settings/ai/test", map[string]any{}, 400)
	call("POST", "/api/jobs/"+second.ID+"/retry-save", "/api/jobs/{id}/retry-save", map[string]any{}, 409)
	call("POST", "/api/auth/change-password", "/api/auth/change-password", map[string]any{"current_password": "FlowDeskDemo!2026", "new_password": "ChangedPassword!2026"}, 204)
	anon := v.call(t, "POST", "/api/auth/login", "/api/auth/login", map[string]any{"email": "admin@flowdesk.demo", "password": "ChangedPassword!2026"}, nil, 200)
	callCookie := anon.Result().Cookies()[0]
	v.call(t, "POST", "/api/auth/logout", "/api/auth/logout", map[string]any{}, callCookie, 204)
}
func TestStrictRequestsAndOriginGuards(t *testing.T) {
	v := setup(t)
	for _, body := range []string{`{"title":"first","title":"second","description":"x","category":"other","priority":"normal"}`, `{"title":"x","description":"x","category":"other","priority":"normal","actor_id":"1"}`, `{"title":"x","description":"x","category":"other","priority":"normal"} {}`, "null"} {
		req := httptest.NewRequest("POST", v.f.Config.PublicOrigin+"/api/tickets", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Origin", v.f.Config.PublicOrigin)
		req.AddCookie(v.cookie)
		w := httptest.NewRecorder()
		v.a.ServeHTTP(w, req)
		if w.Code != 400 {
			t.Fatal("invalid JSON accepted", w.Code)
		}
	}
	for _, kind := range []string{"origin", "host", "media"} {
		req := httptest.NewRequest("POST", v.f.Config.PublicOrigin+"/api/auth/logout", strings.NewReader("{}"))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Origin", v.f.Config.PublicOrigin)
		req.AddCookie(v.cookie)
		switch kind {
		case "origin":
			req.Header.Set("Origin", "https://evil.test")
		case "host":
			req.Host = "evil.test"
		case "media":
			req.Header.Set("Content-Type", "text/plain")
		}
		w := httptest.NewRecorder()
		v.a.ServeHTTP(w, req)
		if w.Code < 400 {
			t.Fatal("unsafe request accepted", kind)
		}
	}
}
func TestRequesterProtectedEndpointsAndNoInternalDTO(t *testing.T) {
	v := setup(t)
	login := v.call(t, "POST", "/api/auth/login", "/api/auth/login", map[string]any{"email": "customer@flowdesk.demo", "password": "FlowDeskDemo!2026"}, nil, 200)
	c := login.Result().Cookies()[0]
	for _, path := range []string{"/api/users", "/api/settings/ai", "/api/knowledge", "/api/jobs", "/api/tickets/1/drafts"} {
		template := path
		if strings.Contains(path, "/tickets/1/") {
			template = "/api/tickets/{id}/drafts"
		}
		v.call(t, "GET", path, template, nil, c, 403)
	}
	w := v.call(t, "GET", "/api/tickets/1", "/api/tickets/{id}", nil, c, 200)
	for _, field := range []string{"active_job_id", "encrypted_secret", "password_hash", "config_json", "token_hash"} {
		if strings.Contains(w.Body.String(), field) {
			t.Fatal("internal field leaked", field)
		}
	}
}
