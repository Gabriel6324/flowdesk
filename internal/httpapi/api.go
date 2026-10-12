package httpapi

import (
	"context"
	"encoding/csv"
	"encoding/json"
	contract "flowdesk/api"
	"flowdesk/internal/auth"
	"flowdesk/internal/buildinfo"
	"flowdesk/internal/domain"
	"flowdesk/internal/jsonx"
	"flowdesk/internal/repository"
	"flowdesk/internal/service"
	"flowdesk/internal/worker"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"
)

const Version = buildinfo.Version

type route struct {
	Method, Pattern string
	Anonymous       bool
	Handle          func(http.ResponseWriter, *http.Request, domain.Actor)
}
type API struct {
	s                    *service.Service
	w                    *worker.Manager
	routes               []route
	assets               fs.FS
	log                  *slog.Logger
	origin, host, cookie string
}

func New(s *service.Service, w *worker.Manager, assets fs.FS, logger *slog.Logger) *API {
	origin := s.Config.PublicOrigin
	u, _ := url.Parse(origin)
	a := &API{s: s, w: w, assets: assets, log: logger, origin: origin, host: u.Host, cookie: "flowdesk_session_" + s.Store.WorkspaceID}
	if s.Config.Profile == "server" {
		a.cookie = "__Host-flowdesk_" + s.Store.WorkspaceID
	}
	a.register()
	return a
}
func (a *API) CookieName() string { return a.cookie }
func (a *API) add(method, path string, anon bool, h func(http.ResponseWriter, *http.Request, domain.Actor)) {
	a.routes = append(a.routes, route{method, path, anon, h})
}
func (a *API) Routes() [][2]string {
	v := [][2]string{}
	for _, r := range a.routes {
		v = append(v, [2]string{r.Method, r.Pattern})
	}
	return v
}
func match(pattern, path string) (string, bool) {
	pp := strings.Split(pattern, "/")
	sp := strings.Split(path, "/")
	if len(pp) != len(sp) {
		return "", false
	}
	id := ""
	for i, p := range pp {
		if p == "{id}" {
			id = sp[i]
			if _, e := repository.ParseID(id); e != nil {
				return "", false
			}
		} else if p != sp[i] {
			return "", false
		}
	}
	return id, true
}

type recorder struct {
	http.ResponseWriter
	status int
}

func (r *recorder) WriteHeader(n int) {
	if r.status == 0 {
		r.status = n
		r.ResponseWriter.WriteHeader(n)
	}
}
func (r *recorder) Write(b []byte) (int, error) {
	if r.status == 0 {
		r.WriteHeader(200)
	}
	return r.ResponseWriter.Write(b)
}
func (a *API) ServeHTTP(raw http.ResponseWriter, r *http.Request) {
	w := &recorder{ResponseWriter: raw}
	requestID := auth.RandomHex(12)
	r = r.WithContext(service.WithRequestID(r.Context(), requestID))
	w.Header().Set("X-Request-ID", requestID)
	if strings.HasPrefix(r.URL.Path, "/api/") {
		w.Header().Set("Cache-Control", "no-store")
	}
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'")
	start := time.Now()
	label := "static"
	defer func() {
		if p := recover(); p != nil {
			if w.status == 0 {
				a.fail(w, r, domain.E("UNKNOWN_ERROR"))
			}
			a.log.Error("handler_panic", "request_id", requestID, "code", "UNKNOWN_ERROR")
		}
		a.log.Info("http", "request_id", requestID, "method", r.Method, "route", label, "status", w.status, "duration_ms", time.Since(start).Milliseconds())
	}()
	if !strings.EqualFold(r.Host, a.host) {
		a.fail(w, r, domain.E("ORIGIN_BLOCKED"))
		return
	}
	if strings.HasPrefix(r.URL.Path, "/api/") {
		w.Header().Set("Cache-Control", "no-store")
		found := false
		for _, route := range a.routes {
			id, ok := match(route.Pattern, r.URL.Path)
			if !ok {
				continue
			}
			found = true
			if r.Method != route.Method {
				continue
			}
			label = route.Pattern
			r.SetPathValue("id", id)
			r.SetPathValue("route", route.Pattern)
			if e := contract.ValidateQuery(r.Method, route.Pattern, r.URL.Query()); e != nil {
				a.fail(w, r, domain.E("VALIDATION_ERROR"))
				return
			}
			if r.Method != "GET" {
				media, _, e := mime.ParseMediaType(r.Header.Get("Content-Type"))
				if e != nil || media != "application/json" {
					a.fail(w, r, domain.E("VALIDATION_ERROR"))
					return
				}
				if r.Header.Get("Origin") != a.origin {
					a.fail(w, r, domain.E("ORIGIN_BLOCKED"))
					return
				}
			}
			var actor domain.Actor
			if !route.Anonymous {
				cookie, e := r.Cookie(a.cookie)
				if e != nil {
					a.fail(w, r, domain.E("AUTH_REQUIRED"))
					return
				}
				actor, e = a.s.Actor(r.Context(), auth.TokenHash(cookie.Value))
				if e != nil {
					a.fail(w, r, e)
					return
				}
			}
			route.Handle(w, r, actor)
			return
		}
		if found {
			a.fail(w, r, domain.E("METHOD_NOT_ALLOWED"))
		} else {
			a.fail(w, r, domain.E("NOT_FOUND"))
		}
		return
	}
	if r.Method != "GET" && r.Method != "HEAD" {
		a.fail(w, r, domain.E("METHOD_NOT_ALLOWED"))
		return
	}
	if strings.HasPrefix(r.URL.Path, "/assets/") {
		w.Header().Set("Cache-Control", "public,max-age=31536000,immutable")
		http.FileServer(http.FS(a.assets)).ServeHTTP(w, r)
		return
	}
	if r.URL.Path != "/" {
		a.fail(w, r, domain.E("NOT_FOUND"))
		return
	}
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	b, e := fs.ReadFile(a.assets, "index.html")
	if e != nil {
		a.fail(w, r, domain.E("UNKNOWN_ERROR"))
		return
	}
	if r.Method == "HEAD" {
		w.WriteHeader(200)
	} else {
		_, _ = w.Write(b)
	}
}
func (a *API) ok(w http.ResponseWriter, r *http.Request, data any) {
	a.respond(w, r, service.Result{Data: json.RawMessage(repository.JSON(data)), Status: 200})
}
func (a *API) respond(w http.ResponseWriter, r *http.Request, result service.Result) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(result.Status)
	env := map[string]any{"data": result.Data, "request_id": w.Header().Get("X-Request-ID")}
	if result.Replayed {
		env["replayed"] = true
	}
	_ = json.NewEncoder(w).Encode(env)
}
func (a *API) fail(w http.ResponseWriter, r *http.Request, e error) {
	d := domain.AsError(e)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(d.Status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": d, "request_id": w.Header().Get("X-Request-ID")})
}
func (a *API) done(w http.ResponseWriter, r *http.Request, result service.Result, e error) {
	if e != nil {
		a.fail(w, r, e)
		return
	}
	a.respond(w, r, result)
}
func decode(w http.ResponseWriter, r *http.Request, v any) error {
	limit := int64(64 * 1024)
	if strings.HasPrefix(r.URL.Path, "/api/knowledge") {
		limit = 128 * 1024
	}
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	b, e := io.ReadAll(r.Body)
	if e != nil || jsonx.Decode(b, v) != nil || contract.ValidateRequest(r.Method, r.PathValue("route"), b) != nil {
		return domain.E("VALIDATION_ERROR")
	}
	return nil
}
func (a *API) empty(w http.ResponseWriter, r *http.Request) bool {
	var b struct{}
	if e := decode(w, r, &b); e != nil {
		a.fail(w, r, e)
		return false
	}
	return true
}
func number(r *http.Request, k string, fallback int) int {
	s := r.URL.Query().Get(k)
	if s == "" {
		return fallback
	}
	v, e := strconv.Atoi(s)
	if e != nil {
		return -1
	}
	return v
}
func filter(r *http.Request) service.TicketFilter {
	q := r.URL.Query()
	return service.TicketFilter{Status: q.Get("status"), Category: q.Get("category"), Priority: q.Get("priority"), Search: q.Get("search"), Page: number(r, "page", 1), PageSize: number(r, "page_size", 20)}
}
func (a *API) setCookie(w http.ResponseWriter, token string, age int) {
	http.SetCookie(w, &http.Cookie{Name: a.cookie, Value: token, Path: "/", MaxAge: age, HttpOnly: true, Secure: a.s.Config.Profile == "server", SameSite: http.SameSiteStrictMode})
}
func (a *API) clientIP(r *http.Request) string {
	host, _, e := net.SplitHostPort(r.RemoteAddr)
	if e != nil {
		host = r.RemoteAddr
	}
	ip := net.ParseIP(host)
	trusted := func(p net.IP) bool {
		for _, c := range a.s.Config.TrustedProxies {
			if c.Contains(p) {
				return true
			}
		}
		return false
	}
	if ip != nil && trusted(ip) {
		parts := strings.Split(r.Header.Get("X-Forwarded-For"), ",")
		for i := len(parts) - 1; i >= 0; i-- {
			p := net.ParseIP(strings.TrimSpace(parts[i]))
			if p == nil {
				break
			}
			ip = p
			if !trusted(p) {
				break
			}
		}
	}
	if ip == nil {
		return "unknown"
	}
	return ip.String()
}
func (a *API) register() {
	a.add("GET", "/api/health", true, func(w http.ResponseWriter, r *http.Request, _ domain.Actor) {
		a.ok(w, r, map[string]any{"status": "alive", "version": Version, "profile": a.s.Config.Profile, "workspace_mode": a.s.Config.WorkspaceMode(), "demo_accounts_available": a.s.Config.Profile == "desktop" && !a.s.Config.Workspace})
	})
	a.add("GET", "/api/ready", true, func(w http.ResponseWriter, r *http.Request, _ domain.Actor) {
		ctx, cancel := context.WithTimeout(r.Context(), 500*time.Millisecond)
		defer cancel()
		var n int
		e := a.s.Store.DB.QueryRowContext(ctx, "SELECT 1").Scan(&n)
		if e != nil || a.s.StorageBlocked.Load() || a.s.Stopping.Load() {
			a.fail(w, r, domain.E("STORAGE_UNHEALTHY"))
			return
		}
		a.ok(w, r, map[string]any{"status": "ready"})
	})
	a.add("POST", "/api/auth/login", true, func(w http.ResponseWriter, r *http.Request, _ domain.Actor) {
		var b struct {
			Email    string `json:"email"`
			Password string `json:"password"`
		}
		if e := decode(w, r, &b); e != nil {
			a.fail(w, r, e)
			return
		}
		me, token, e := a.s.Login(r.Context(), b.Email, b.Password, a.clientIP(r))
		if e != nil {
			a.fail(w, r, e)
			return
		}
		a.setCookie(w, token, 12*3600)
		a.ok(w, r, me)
	})
	a.add("POST", "/api/auth/logout", false, func(w http.ResponseWriter, r *http.Request, u domain.Actor) {
		if !a.empty(w, r) {
			return
		}
		if e := a.s.Logout(r.Context(), u); e != nil {
			a.fail(w, r, e)
			return
		}
		a.setCookie(w, "", -1)
		w.WriteHeader(204)
	})
	a.add("GET", "/api/auth/me", false, func(w http.ResponseWriter, r *http.Request, u domain.Actor) {
		a.ok(w, r, domain.Me{User: u.User, WorkspaceID: a.s.Store.WorkspaceID, Profile: a.s.Config.Profile})
	})
	a.add("POST", "/api/auth/change-password", false, func(w http.ResponseWriter, r *http.Request, u domain.Actor) {
		var b service.PasswordBody
		if e := decode(w, r, &b); e != nil {
			a.fail(w, r, e)
			return
		}
		if e := a.s.ChangePassword(r.Context(), u, b); e != nil {
			a.fail(w, r, e)
			return
		}
		a.setCookie(w, "", -1)
		w.WriteHeader(204)
	})
	a.add("GET", "/api/dashboard", false, func(w http.ResponseWriter, r *http.Request, u domain.Actor) {
		v, e := a.s.Dashboard(r.Context(), u)
		if e != nil {
			a.fail(w, r, e)
			return
		}
		a.ok(w, r, v)
	})
	a.add("GET", "/api/tickets", false, func(w http.ResponseWriter, r *http.Request, u domain.Actor) {
		v, e := a.s.Tickets(r.Context(), u, filter(r))
		if e != nil {
			a.fail(w, r, e)
			return
		}
		a.ok(w, r, v)
	})
	a.add("POST", "/api/tickets", false, func(w http.ResponseWriter, r *http.Request, u domain.Actor) {
		var b service.TicketCreate
		if e := decode(w, r, &b); e != nil {
			a.fail(w, r, e)
			return
		}
		v, e := a.s.CreateTicket(r.Context(), u, r.Header.Get("Idempotency-Key"), b)
		a.done(w, r, v, e)
	})
	a.add("GET", "/api/tickets/export", false, a.export)
	a.add("GET", "/api/tickets/{id}", false, func(w http.ResponseWriter, r *http.Request, u domain.Actor) {
		v, e := a.s.TicketWorkspace(r.Context(), u, r.PathValue("id"))
		if e != nil {
			a.fail(w, r, e)
			return
		}
		if v.Work != nil {
			for i := range v.Work.Jobs {
				v.Work.Jobs[i].RuntimeHint = a.w.Hint(v.Work.Jobs[i].ID)
			}
		}
		a.ok(w, r, v)
	})
	a.add("PATCH", "/api/tickets/{id}", false, func(w http.ResponseWriter, r *http.Request, u domain.Actor) {
		var b service.TicketPatch
		if e := decode(w, r, &b); e != nil {
			a.fail(w, r, e)
			return
		}
		v, e := a.s.PatchTicket(r.Context(), u, r.PathValue("id"), r.Header.Get("Idempotency-Key"), b)
		a.done(w, r, v, e)
	})
	a.add("POST", "/api/tickets/{id}/reopen", false, func(w http.ResponseWriter, r *http.Request, u domain.Actor) {
		var b service.VersionBody
		if e := decode(w, r, &b); e != nil {
			a.fail(w, r, e)
			return
		}
		v, e := a.s.Reopen(r.Context(), u, r.PathValue("id"), r.Header.Get("Idempotency-Key"), b)
		a.done(w, r, v, e)
	})
	a.add("GET", "/api/tickets/{id}/comments", false, func(w http.ResponseWriter, r *http.Request, u domain.Actor) {
		v, e := a.s.Ticket(r.Context(), u, r.PathValue("id"))
		if e != nil {
			a.fail(w, r, e)
			return
		}
		a.ok(w, r, v.Comments)
	})
	a.add("POST", "/api/tickets/{id}/comments", false, func(w http.ResponseWriter, r *http.Request, u domain.Actor) {
		var b service.CommentBody
		if e := decode(w, r, &b); e != nil {
			a.fail(w, r, e)
			return
		}
		v, e := a.s.Comment(r.Context(), u, r.PathValue("id"), r.Header.Get("Idempotency-Key"), b)
		a.done(w, r, v, e)
	})
	a.add("GET", "/api/tickets/{id}/drafts", false, func(w http.ResponseWriter, r *http.Request, u domain.Actor) {
		v, e := a.s.Drafts(r.Context(), u, r.PathValue("id"))
		if e != nil {
			a.fail(w, r, e)
			return
		}
		a.ok(w, r, v)
	})
	a.add("POST", "/api/tickets/{id}/jobs", false, func(w http.ResponseWriter, r *http.Request, u domain.Actor) {
		var b service.JobBody
		if e := decode(w, r, &b); e != nil {
			a.fail(w, r, e)
			return
		}
		v, e := a.s.Enqueue(r.Context(), u, r.PathValue("id"), r.Header.Get("Idempotency-Key"), b)
		a.done(w, r, v, e)
	})
	for _, action := range []string{"apply-suggestion", "reject-draft", "approve"} {
		action := action
		a.add("POST", "/api/tickets/{id}/"+action, false, func(w http.ResponseWriter, r *http.Request, u domain.Actor) {
			var b service.DraftAction
			if e := decode(w, r, &b); e != nil {
				a.fail(w, r, e)
				return
			}
			v, e := a.s.DraftAction(r.Context(), u, r.PathValue("id"), r.Header.Get("Idempotency-Key"), action, b)
			a.done(w, r, v, e)
		})
	}
	a.add("POST", "/api/tickets/{id}/reply", false, func(w http.ResponseWriter, r *http.Request, u domain.Actor) {
		var b service.ManualBody
		if e := decode(w, r, &b); e != nil {
			a.fail(w, r, e)
			return
		}
		v, e := a.s.Reply(r.Context(), u, r.PathValue("id"), r.Header.Get("Idempotency-Key"), b)
		a.done(w, r, v, e)
	})
	a.add("GET", "/api/jobs", false, func(w http.ResponseWriter, r *http.Request, u domain.Actor) {
		v, e := a.s.Jobs(r.Context(), u, number(r, "page", 1), number(r, "page_size", 20), r.URL.Query().Get("ticket_id"))
		if e != nil {
			a.fail(w, r, e)
			return
		}
		for i := range v.Items {
			v.Items[i].RuntimeHint = a.w.Hint(v.Items[i].ID)
		}
		a.ok(w, r, v)
	})
	a.add("GET", "/api/jobs/{id}", false, func(w http.ResponseWriter, r *http.Request, u domain.Actor) {
		v, e := a.s.Job(r.Context(), u, r.PathValue("id"))
		if e != nil {
			a.fail(w, r, e)
			return
		}
		v.RuntimeHint = a.w.Hint(v.ID)
		a.ok(w, r, v)
	})
	a.add("POST", "/api/jobs/{id}/cancel", false, func(w http.ResponseWriter, r *http.Request, u domain.Actor) {
		if !a.empty(w, r) {
			return
		}
		v, e := a.s.Cancel(r.Context(), u, r.PathValue("id"), r.Header.Get("Idempotency-Key"))
		a.done(w, r, v, e)
	})
	a.add("POST", "/api/jobs/{id}/retry", false, func(w http.ResponseWriter, r *http.Request, u domain.Actor) {
		var b service.JobBody
		if e := decode(w, r, &b); e != nil {
			a.fail(w, r, e)
			return
		}
		v, e := a.s.RetryJob(r.Context(), u, r.PathValue("id"), r.Header.Get("Idempotency-Key"), b)
		a.done(w, r, v, e)
	})
	a.add("POST", "/api/jobs/{id}/retry-save", false, func(w http.ResponseWriter, r *http.Request, u domain.Actor) {
		if !a.empty(w, r) {
			return
		}
		v, e := a.s.RetrySave(r.Context(), u, r.PathValue("id"))
		if e != nil {
			a.fail(w, r, e)
			return
		}
		a.ok(w, r, v)
	})
	a.add("GET", "/api/knowledge", false, func(w http.ResponseWriter, r *http.Request, u domain.Actor) {
		v, e := a.s.Knowledge(r.Context(), u, number(r, "page", 1), number(r, "page_size", 20), r.URL.Query().Get("include_archived") == "true", r.URL.Query().Get("search"))
		if e != nil {
			a.fail(w, r, e)
			return
		}
		a.ok(w, r, v)
	})
	a.add("POST", "/api/knowledge", false, func(w http.ResponseWriter, r *http.Request, u domain.Actor) {
		var b service.ArticleBody
		if e := decode(w, r, &b); e != nil {
			a.fail(w, r, e)
			return
		}
		v, e := a.s.CreateArticle(r.Context(), u, r.Header.Get("Idempotency-Key"), b)
		a.done(w, r, v, e)
	})
	a.add("GET", "/api/knowledge/{id}", false, func(w http.ResponseWriter, r *http.Request, u domain.Actor) {
		if e := service.Staff(u); e != nil {
			a.fail(w, r, e)
			return
		}
		v, e := repository.Article(r.Context(), a.s.Store.DB, r.PathValue("id"))
		if e != nil {
			a.fail(w, r, e)
			return
		}
		a.ok(w, r, v)
	})
	a.add("PATCH", "/api/knowledge/{id}", false, func(w http.ResponseWriter, r *http.Request, u domain.Actor) {
		var b service.ArticleBody
		if e := decode(w, r, &b); e != nil {
			a.fail(w, r, e)
			return
		}
		v, e := a.s.PatchArticle(r.Context(), u, r.PathValue("id"), r.Header.Get("Idempotency-Key"), b)
		a.done(w, r, v, e)
	})
	a.add("GET", "/api/users", false, func(w http.ResponseWriter, r *http.Request, u domain.Actor) {
		v, e := a.s.Users(r.Context(), u)
		if e != nil {
			a.fail(w, r, e)
			return
		}
		a.ok(w, r, v)
	})
	a.add("POST", "/api/users", false, func(w http.ResponseWriter, r *http.Request, u domain.Actor) {
		var b service.UserCreate
		if e := decode(w, r, &b); e != nil {
			a.fail(w, r, e)
			return
		}
		v, e := a.s.CreateUser(r.Context(), u, r.Header.Get("Idempotency-Key"), b)
		a.done(w, r, v, e)
	})
	a.add("PATCH", "/api/users/{id}", false, func(w http.ResponseWriter, r *http.Request, u domain.Actor) {
		var b service.UserPatch
		if e := decode(w, r, &b); e != nil {
			a.fail(w, r, e)
			return
		}
		v, e := a.s.PatchUser(r.Context(), u, r.PathValue("id"), r.Header.Get("Idempotency-Key"), b)
		a.done(w, r, v, e)
	})
	a.add("GET", "/api/settings/ai", false, func(w http.ResponseWriter, r *http.Request, u domain.Actor) {
		v, e := a.s.Settings(r.Context(), u)
		if e != nil {
			a.fail(w, r, e)
			return
		}
		a.ok(w, r, v)
	})
	a.add("PUT", "/api/settings/ai", false, func(w http.ResponseWriter, r *http.Request, u domain.Actor) {
		var b service.SettingsBody
		if e := decode(w, r, &b); e != nil {
			a.fail(w, r, e)
			return
		}
		v, e := a.s.PutSettings(r.Context(), u, r.Header.Get("Idempotency-Key"), b)
		a.done(w, r, v, e)
	})
	a.add("POST", "/api/settings/ai/test", false, func(w http.ResponseWriter, r *http.Request, u domain.Actor) {
		if !a.empty(w, r) {
			return
		}
		v, e := a.s.TestModel(r.Context(), u)
		if e != nil {
			a.fail(w, r, e)
			return
		}
		a.ok(w, r, v)
	})
	a.add("GET", "/api/audit", false, func(w http.ResponseWriter, r *http.Request, u domain.Actor) {
		id := r.URL.Query().Get("ticket_id")
		if _, e := repository.ParseID(id); e != nil {
			a.fail(w, r, e)
			return
		}
		v, e := a.s.Audit(r.Context(), u, id)
		if e != nil {
			a.fail(w, r, e)
			return
		}
		a.ok(w, r, v)
	})
}
func safeCSV(s string) string {
	t := strings.TrimLeftFunc(s, unicode.IsSpace)
	if t != "" && strings.ContainsRune("=+-@", rune(t[0])) || strings.HasPrefix(s, "\t") || strings.HasPrefix(s, "\r") {
		return "'" + s
	}
	return s
}
func (a *API) export(w http.ResponseWriter, r *http.Request, u domain.Actor) {
	format := r.URL.Query().Get("format")
	if !domain.Enum(format, "json", "csv") {
		a.fail(w, r, domain.E("VALIDATION_ERROR"))
		return
	}
	items, e := a.s.ExportTickets(r.Context(), u, filter(r))
	if e != nil {
		a.fail(w, r, e)
		return
	}
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"flowdesk-tickets.%s\"", format))
	if format == "json" {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_ = json.NewEncoder(w).Encode(map[string]any{"schema_version": 1, "exported_at": repository.Now(), "count": len(items), "items": items})
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	_, _ = io.WriteString(w, "\ufeff")
	cw := csv.NewWriter(w)
	_ = cw.Write([]string{"id", "title", "description", "category", "priority", "status", "created_at"})
	for _, t := range items {
		_ = cw.Write([]string{t.DisplayID, safeCSV(t.Title), safeCSV(t.Description), t.Category, t.Priority, t.Status, t.CreatedAt})
	}
	cw.Flush()
}
