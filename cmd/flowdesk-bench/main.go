package main

// An isolated closed-loop workload against real net/http + the application API.
// It creates only a temporary fictional workspace; it never loads user data.
import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"flowdesk/internal/auth"
	"flowdesk/internal/buildinfo"
	"flowdesk/internal/domain"
	"flowdesk/internal/httpapi"
	"flowdesk/internal/platform"
	"flowdesk/internal/repository"
	"flowdesk/internal/service"
	"flowdesk/internal/worker"
	webassets "flowdesk/web"
	"fmt"
	"io"
	"log/slog"
	"math"
	"math/rand/v2"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Sample struct {
	InitiatedMS  float64 `json:"initiated_ms"`
	Milliseconds float64 `json:"milliseconds"`
	Status       int     `json:"status"`
	Method       string  `json:"method"`
	Route        string  `json:"route"`
}
type Stats struct {
	Concurrency         int            `json:"concurrency"`
	WindowSeconds       float64        `json:"window_seconds"`
	DrainSeconds        float64        `json:"drain_seconds"`
	LogicalOperations   int            `json:"logical_operations"`
	WindowBoundarySkips int            `json:"window_boundary_skips"`
	HTTPRequests        int            `json:"http_requests"`
	RequestsPerSecond   float64        `json:"http_requests_per_second"`
	Statuses            map[string]int `json:"status_counts"`
	P50MS               float64        `json:"p50_ms"`
	P95MS               float64        `json:"p95_ms"`
	P99MS               float64        `json:"p99_ms"`
	Timeouts            int            `json:"timeouts"`
	Raw                 []Sample       `json:"samples"`
}
type Resource struct {
	HeapPeak      uint64  `json:"heap_peak_bytes"`
	RSSPeak       *uint64 `json:"linux_rss_peak_bytes"`
	GoroutinePeak int     `json:"goroutine_peak"`
}
type Report struct {
	Version        string         `json:"software_version"`
	Source         string         `json:"source_hash"`
	Created        string         `json:"created_at"`
	Go             string         `json:"go_version"`
	OS             string         `json:"os"`
	Arch           string         `json:"arch"`
	CPUs           int            `json:"logical_cpus"`
	GOMAXPROCS     int            `json:"gomaxprocs"`
	CGroupCPU      string         `json:"cgroup_cpu_max"`
	CGroupMemory   string         `json:"cgroup_memory_max"`
	SQLite         string         `json:"sqlite_version"`
	InitialTickets int            `json:"initial_tickets"`
	Articles       int            `json:"articles"`
	WarmupSeconds  float64        `json:"warmup_seconds"`
	Mix            string         `json:"mix"`
	KDFIterations  int            `json:"kdf_iterations"`
	KDFMS          []float64      `json:"kdf_sequential_samples_ms"`
	KDFMedianMS    float64        `json:"kdf_median_ms"`
	Phases         []Stats        `json:"phases"`
	Resources      Resource       `json:"resources"`
	Queue          map[string]any `json:"mock_queue"`
	Limits         []string       `json:"limitations"`
}

func main() {
	if e := run(); e != nil {
		fmt.Fprintln(os.Stderr, "Benchmark stopped:", e)
		os.Exit(1)
	}
}
func read(p string) string { b, _ := os.ReadFile(p); return strings.TrimSpace(string(b)) }
func rss() *uint64 {
	for _, line := range strings.Split(read("/proc/self/status"), "\n") {
		if strings.HasPrefix(line, "VmRSS:") {
			f := strings.Fields(line)
			if len(f) >= 2 {
				v, e := strconv.ParseUint(f[1], 10, 64)
				if e == nil {
					v *= 1024
					return &v
				}
			}
		}
	}
	return nil
}
func percentile(values []float64, p float64) float64 {
	if len(values) == 0 {
		return 0
	}
	i := int(math.Ceil(p*float64(len(values)))) - 1
	if i < 0 {
		i = 0
	}
	return values[i]
}
func run() error {
	var duration, warmup time.Duration
	var levels, out string
	var queue bool
	flag.DurationVar(&duration, "duration", 30*time.Second, "measurement window per level")
	flag.DurationVar(&warmup, "warmup", 10*time.Second, "unreported warmup per level")
	flag.StringVar(&levels, "concurrency", "1,10,25", "closed-loop client levels")
	flag.StringVar(&out, "out", "", "new JSON result file")
	flag.BoolVar(&queue, "queue", true, "also run the isolated 250ms provider/2-worker/50-active queue phase")
	flag.Parse()
	if out == "" || duration < time.Second || warmup < 0 || duration > 10*time.Minute {
		return fmt.Errorf("invalid output or duration")
	}
	if _, e := os.Stat(out); e == nil {
		return fmt.Errorf("output exists")
	}
	concurrency := []int{}
	for _, part := range strings.Split(levels, ",") {
		n, e := strconv.Atoi(part)
		if e != nil || n < 1 || n > 25 {
			return fmt.Errorf("concurrency must be 1..25")
		}
		concurrency = append(concurrency, n)
	}
	dir, e := os.MkdirTemp("", "flowdesk-bench-")
	if e != nil {
		return e
	}
	defer os.RemoveAll(dir)
	cfg := platform.Config{Profile: "desktop", DataDir: dir, NoBrowser: true}
	keys, e := platform.LoadKey(dir, true)
	if e != nil {
		return e
	}
	store, e := repository.Open(context.Background(), cfg, repository.Bootstrap{})
	if e != nil {
		return e
	}
	defer store.DB.Close()
	svc := service.New(store, cfg, keys)
	manager := worker.New(svc)
	manager.Start()
	defer manager.Stop(5 * time.Second)
	if e = store.Tx(context.Background(), func(tx *sql.Tx) error {
		now := repository.Now()
		for i := 5; i <= 1000; i++ {
			if _, e := tx.Exec("INSERT INTO tickets(title,description,requester_id,category,priority,status,created_at,updated_at) VALUES(?, ?,3,'technical','normal','open',?,?)", fmt.Sprintf("Benchmark ticket %d", i), "Synthetic API authentication question", now, now); e != nil {
				return e
			}
		}
		for i := 6; i <= 100; i++ {
			if _, e := tx.Exec("INSERT INTO knowledge_articles(title,body,tags_json,created_at,updated_at) VALUES(?,?,'[\"API\"]',?,?)", fmt.Sprintf("Synthetic API FAQ %d", i), strings.Repeat("Only public fictional API troubleshooting information. ", 20), now, now); e != nil {
				return e
			}
		}
		return nil
	}); e != nil {
		return e
	}
	var api *httpapi.API
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { api.ServeHTTP(w, r) }))
	defer server.Close()
	svc.Config.PublicOrigin = server.URL
	api = httpapi.New(svc, manager, webassets.FS(), slog.New(slog.NewJSONHandler(io.Discard, nil)))
	tr := &http.Transport{MaxIdleConns: 64, MaxIdleConnsPerHost: 32, Proxy: nil}
	defer tr.CloseIdleConnections()
	client := &http.Client{Transport: tr, Timeout: 5 * time.Second}
	status, body, cookies := send(context.Background(), client, server.URL, nil, "POST", "/api/auth/login", map[string]string{"email": "admin@flowdesk.demo", "password": "FlowDeskDemo!2026"})
	if status != 200 || len(cookies) == 0 {
		return fmt.Errorf("preauthentication failed: %d %s", status, body)
	}
	cookie := cookies[0]
	_, token, e := svc.Login(context.Background(), "admin@flowdesk.demo", "FlowDeskDemo!2026", "bench-local")
	if e != nil {
		return e
	}
	actor, e := svc.Actor(context.Background(), auth.TokenHash(token))
	if e != nil {
		return e
	}
	report := Report{Version: buildinfo.Version, Source: buildinfo.SourceHash, Created: time.Now().UTC().Format(time.RFC3339), Go: runtime.Version(), OS: runtime.GOOS, Arch: runtime.GOARCH, CPUs: runtime.NumCPU(), GOMAXPROCS: runtime.GOMAXPROCS(0), CGroupCPU: read("/sys/fs/cgroup/cpu.max"), CGroupMemory: read("/sys/fs/cgroup/memory.max"), SQLite: store.SQLiteVersion, InitialTickets: 1000, Articles: 100, WarmupSeconds: warmup.Seconds(), Mix: "70% list reads / 20% priority edits on per-client tickets (GET revision + PATCH) / 10% dashboard; seed=20261011; idempotency cache omitted for sustained isolated writes", KDFIterations: auth.Iterations, KDFMS: []float64{}, Phases: []Stats{}, Limits: []string{"Closed-loop local net/http load, not open-loop production capacity", "Sessions are prepared before measurement; KDF measured separately", "Writes include revision-refresh GET; every HTTP status is retained", "Request deadline/drain 5 seconds; status 0 counts as timeout/network failure", "Mock queue duration is artificial, not real model speed", "RSS/heap/goroutines include this local client and server process"}}
	salt := make([]byte, 16)
	for i := 0; i < 11; i++ {
		start := time.Now()
		_, e = auth.Derive("benchmark-only-password", salt, auth.Iterations)
		if e != nil {
			return e
		}
		if i > 0 {
			report.KDFMS = append(report.KDFMS, float64(time.Since(start).Microseconds())/1000)
		}
	}
	samples := append([]float64{}, report.KDFMS...)
	sort.Float64s(samples)
	report.KDFMedianMS = percentile(samples, .5)
	var resourceMu sync.Mutex
	stopSample := make(chan struct{})
	sampleJoined := make(chan struct{})
	go func() {
		defer close(sampleJoined)
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-stopSample:
				return
			case <-ticker.C:
				var ms runtime.MemStats
				runtime.ReadMemStats(&ms)
				resourceMu.Lock()
				if ms.HeapAlloc > report.Resources.HeapPeak {
					report.Resources.HeapPeak = ms.HeapAlloc
				}
				g := runtime.NumGoroutine()
				if g > report.Resources.GoroutinePeak {
					report.Resources.GoroutinePeak = g
				}
				if rr := rss(); rr != nil && (report.Resources.RSSPeak == nil || *rr > *report.Resources.RSSPeak) {
					report.Resources.RSSPeak = rr
				}
				resourceMu.Unlock()
			}
		}
	}()
	for _, n := range concurrency {
		fmt.Fprintf(os.Stderr, "Benchmark %d clients: warmup %s, measurement %s.\n", n, warmup, duration)
		phase(client, server.URL, cookie, n, warmup, false)
		result := phase(client, server.URL, cookie, n, duration, true)
		report.Phases = append(report.Phases, result)
	}
	if queue {
		q, e := queuePhase(store, svc, manager, actor, client, server.URL, cookie)
		if e != nil {
			close(stopSample)
			<-sampleJoined
			return e
		}
		report.Queue = q
	}
	close(stopSample)
	<-sampleJoined
	raw, e := json.MarshalIndent(report, "", "  ")
	if e != nil {
		return e
	}
	if e = platform.AtomicPrivateWrite(out, append(raw, '\n')); e != nil {
		return e
	}
	fmt.Printf("Benchmark complete: %s\n", out)
	return nil
}
func send(ctx context.Context, c *http.Client, origin string, cookie *http.Cookie, method, path string, body any) (int, []byte, []*http.Cookie) {
	var reader io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		reader = bytes.NewReader(b)
	}
	req, e := http.NewRequestWithContext(ctx, method, origin+path, reader)
	if e != nil {
		return 0, nil, nil
	}
	if cookie != nil {
		req.AddCookie(cookie)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Origin", origin)
	}
	resp, e := c.Do(req)
	if e != nil {
		return 0, nil, nil
	}
	defer resp.Body.Close()
	b, e := io.ReadAll(io.LimitReader(resp.Body, 1024*1024))
	if e != nil {
		return 0, nil, nil
	}
	return resp.StatusCode, b, resp.Cookies()
}
func phase(client *http.Client, origin string, cookie *http.Cookie, n int, d time.Duration, record bool) Stats {
	result := Stats{Concurrency: n, WindowSeconds: d.Seconds(), Statuses: map[string]int{}, Raw: []Sample{}}
	start := time.Now()
	end := start.Add(d)
	ctx, cancel := context.WithDeadline(context.Background(), end.Add(5*time.Second))
	defer cancel()
	type local struct {
		ops     int
		skips   int
		samples []Sample
	}
	parts := make([]local, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			rng := rand.New(rand.NewPCG(20261011, uint64(i+1)))
			ticket := strconv.Itoa(10 + i)
			do := func(method, path, template string, b any) (int, []byte) {
				before := time.Now()
				if !before.Before(end) {
					parts[i].skips++
					return -1, nil
				}
				status, raw, _ := send(ctx, client, origin, cookie, method, path, b)
				if record {
					parts[i].samples = append(parts[i].samples, Sample{float64(before.Sub(start).Microseconds()) / 1000, float64(time.Since(before).Microseconds()) / 1000, status, method, template})
				}
				return status, raw
			}
			for time.Now().Before(end) && ctx.Err() == nil {
				x := rng.IntN(100)
				if x < 70 {
					do("GET", "/api/tickets?page=1&page_size=20", "/api/tickets", nil)
				} else if x < 90 {
					code, b := do("GET", "/api/tickets/"+ticket, "/api/tickets/{id}", nil)
					if code == 200 {
						var detail struct {
							Data domain.TicketDetail `json:"data"`
						}
						if json.Unmarshal(b, &detail) == nil {
							priority := "low"
							if detail.Data.Ticket.Priority == "low" {
								priority = "normal"
							}
							do("PATCH", "/api/tickets/"+ticket, "/api/tickets/{id}", map[string]any{"expected_revision": detail.Data.Ticket.Revision, "priority": priority})
						}
					}
				} else {
					do("GET", "/api/dashboard", "/api/dashboard", nil)
				}
				parts[i].ops++
			}
		}(i)
	}
	wg.Wait()
	result.DrainSeconds = math.Max(0, time.Since(end).Seconds())
	times := []float64{}
	for _, part := range parts {
		result.LogicalOperations += part.ops
		result.WindowBoundarySkips += part.skips
		result.Raw = append(result.Raw, part.samples...)
	}
	result.HTTPRequests = len(result.Raw)
	for _, s := range result.Raw {
		result.Statuses[strconv.Itoa(s.Status)]++
		times = append(times, s.Milliseconds)
		if s.Status == 0 {
			result.Timeouts++
		}
	}
	sort.Float64s(times)
	result.P50MS = percentile(times, .5)
	result.P95MS = percentile(times, .95)
	result.P99MS = percentile(times, .99)
	if d > 0 {
		result.RequestsPerSecond = float64(result.HTTPRequests) / d.Seconds()
	}
	return result
}
func queuePhase(store *repository.Store, svc *service.Service, m *worker.Manager, actor domain.Actor, client *http.Client, origin string, cookie *http.Cookie) (map[string]any, error) {
	gate := make(chan struct{})
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-gate:
		case <-r.Context().Done():
			return
		}
		timer := time.NewTimer(250 * time.Millisecond)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-r.Context().Done():
			return
		}
		o := domain.AIOutput{Summary: "mock queue", Category: "technical", Priority: "normal", Reply: "Fictional mock output only.", EvidenceIDs: []string{}, NeedsMoreInformation: domain.Ptr(true)}
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": repository.JSON(o)}, "finish_reason": "stop"}}, "usage": map[string]int{"prompt_tokens": 100, "completion_tokens": 40}})
	}))
	defer provider.Close()
	cfg, _, e := repository.Settings(context.Background(), store.DB)
	if e != nil {
		return nil, e
	}
	cfg.Mode = "live"
	cfg.Strategy = "workflow"
	cfg.BaseURL = provider.URL
	cfg.Model = "250ms-protocol-mock"
	cfg.TimeoutSeconds = 30
	if _, e = svc.PutSettings(context.Background(), actor, "queue-settings", service.SettingsBody{AIConfig: cfg, APIKey: "mock-queue-not-a-real-key"}); e != nil {
		return nil, e
	}
	cfg, _, e = repository.Settings(context.Background(), store.DB)
	if e != nil {
		return nil, e
	}
	for i := 0; i < 60; i++ {
		now := repository.Now()
		if _, e = store.DB.Exec("INSERT INTO tickets(title,description,requester_id,category,priority,status,created_at,updated_at) VALUES('Queue benchmark','API authentication question',3,'technical','normal','open',?,?)", now, now); e != nil {
			return nil, e
		}
	}
	statuses := map[string]int{}
	jobs := []string{}
	start := time.Now()
	for i := 1001; i <= 1060; i++ {
		status, b, _ := send(context.Background(), client, origin, cookie, "POST", fmt.Sprintf("/api/tickets/%d/jobs", i), map[string]any{"expected_revision": 1, "expected_config_version": cfg.Version})
		statuses[strconv.Itoa(status)]++
		if status == 202 {
			var v struct {
				Data domain.Job `json:"data"`
			}
			if json.Unmarshal(b, &v) == nil {
				jobs = append(jobs, v.Data.ID)
			}
		}
	}
	close(gate)
	deadline := time.Now().Add(15 * time.Second)
	terminal := map[string]int{}
	calls := 0
	for time.Now().Before(deadline) {
		terminal = map[string]int{}
		calls = 0
		for _, id := range jobs {
			v, e := svc.Job(context.Background(), actor, id)
			if e != nil {
				return nil, e
			}
			terminal[v.Status]++
			calls += len(v.Calls)
		}
		if terminal["succeeded"]+terminal["failed"]+terminal["canceled"]+terminal["stale"] == len(jobs) {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	return map[string]any{"artificial_provider_delay_ms": 250, "workers": 2, "active_limit": 50, "submitted": 60, "enqueue_statuses": statuses, "accepted": len(jobs), "terminal_statuses": terminal, "recorded_calls": calls, "elapsed_seconds": time.Since(start).Seconds(), "queue_empty_after_drain": terminal["queued"]+terminal["running"] == 0}, nil
}
