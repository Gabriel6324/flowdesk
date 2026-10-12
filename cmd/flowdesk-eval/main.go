package main

// This harness uses the production engine, never the production workspace.
// Inputs are loaded here; gold is read only by a separate offline scoring tool.
import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"flowdesk/internal/ai"
	"flowdesk/internal/buildinfo"
	"flowdesk/internal/domain"
	"flowdesk/internal/jsonx"
	"flowdesk/internal/platform"
	"fmt"
	"math"
	"math/rand/v2"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type Case struct {
	ID    string       `json:"id"`
	Slice string       `json:"slice"`
	Split string       `json:"split"`
	Input domain.Input `json:"input"`
}
type Dataset struct {
	Version string `json:"version"`
	Cases   []Case `json:"cases"`
}
type Trial struct {
	ID            string           `json:"id"`
	CaseID        string           `json:"case_id"`
	Slice         string           `json:"slice"`
	Strategy      string           `json:"strategy"`
	Repeat        int              `json:"repeat"`
	Attempted     bool             `json:"attempted"`
	Status        string           `json:"status"`
	Reason        string           `json:"reason,omitempty"`
	ErrorCode     *string          `json:"error_code"`
	DurationMS    *int64           `json:"duration_ms"`
	Output        *domain.AIOutput `json:"output"`
	Sources       []domain.Source  `json:"sources"`
	Calls         []domain.Call    `json:"calls"`
	Steps         []string         `json:"steps"`
	EstimatedCost *float64         `json:"estimated_cost"`
	HumanReview   string           `json:"human_review"`
}
type Report struct {
	Version         string            `json:"software_version"`
	SourceHash      string            `json:"source_hash"`
	CreatedAt       string            `json:"created_at"`
	Mode            string            `json:"mode"`
	Plan            string            `json:"plan"`
	DatasetVersion  string            `json:"dataset_version"`
	Hashes          map[string]string `json:"sha256"`
	PromptVersion   string            `json:"prompt_version"`
	RetrievalPolicy string            `json:"retrieval_policy"`
	Config          domain.AIConfig   `json:"configuration"`
	Sampling        map[string]any    `json:"sampling"`
	Planned         int               `json:"planned"`
	DistinctCases   int               `json:"distinct_cases"`
	WorstRequests   int               `json:"worst_requests"`
	RequestCap      int64             `json:"request_cap"`
	Requests        int64             `json:"actual_requests"`
	MaxCost         float64           `json:"max_estimated_cost"`
	StopReason      string            `json:"stop_reason"`
	Trials          []Trial           `json:"trials"`
}

func load(path string, dst any) ([]byte, error) {
	b, e := os.ReadFile(path)
	if e != nil {
		return nil, e
	}
	if jsonx.Decode(b, dst) != nil {
		return nil, fmt.Errorf("invalid JSON file %s", filepath.Base(path))
	}
	return b, nil
}
func digest(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }
func main() {
	if e := run(); e != nil {
		fmt.Fprintln(os.Stderr, "Evaluation stopped:", e)
		os.Exit(1)
	}
}
func run() error {
	var root, mode, plan, out, configPath string
	var acknowledge bool
	var cap int64
	var maxCost float64
	flag.StringVar(&root, "data", "evaluation", "frozen dataset directory")
	flag.StringVar(&mode, "mode", "plan", "plan, demo, mock or live; plan never calls a provider")
	flag.StringVar(&plan, "plan", "pilot", "pilot development (16 trials) or full holdout (192 trials)")
	flag.StringVar(&out, "out", "", "new private results directory for an execution")
	flag.StringVar(&configPath, "config", "", "private JSON AIConfig file; key supplied only through FLOWDESK_EVAL_API_KEY")
	flag.BoolVar(&acknowledge, "acknowledge-cost", false, "explicitly allow paid live requests")
	flag.Int64Var(&cap, "max-requests", 0, "hard total provider request cap, required in live")
	flag.Float64Var(&maxCost, "max-estimated-cost", 0, "estimated-cost threshold; may not match the provider invoice")
	flag.Parse()
	if !domain.Enum(mode, "plan", "demo", "mock", "live") || !domain.Enum(plan, "pilot", "full") || flag.NArg() != 0 {
		return errors.New("invalid mode or plan")
	}
	// Manifest hashes are checked before loading any case. No gold content is parsed.
	var manifest struct {
		Version     string            `json:"version"`
		Distinct    int               `json:"distinct_cases"`
		Development int               `json:"development"`
		Holdout     int               `json:"holdout"`
		Slices      int               `json:"slices"`
		Seed        int               `json:"seed"`
		Hashes      map[string]string `json:"sha256"`
		Limits      []string          `json:"limitations"`
	}
	if _, e := load(filepath.Join(root, "manifest.json"), &manifest); e != nil {
		return e
	}
	for name, expected := range manifest.Hashes {
		if filepath.IsAbs(name) || strings.Contains(name, "..") {
			return errors.New("invalid manifest path")
		}
		b, e := os.ReadFile(filepath.Join(root, name))
		if e != nil || digest(b) != expected {
			return fmt.Errorf("frozen dataset changed: %s", name)
		}
	}
	var ds Dataset
	b, e := load(filepath.Join(root, "input", "cases.json"), &ds)
	if e != nil {
		return e
	}
	var kb []domain.Article
	// Arrays use ordinary Unmarshal plus duplicate-key validation, since jsonx.Decode requires an object.
	kbb, e := os.ReadFile(filepath.Join(root, "input", "knowledge.json"))
	if e != nil {
		return e
	}
	if jsonx.Check(kbb) != nil || json.Unmarshal(kbb, &kb) != nil || len(kb) > 200 {
		return errors.New("invalid knowledge snapshot")
	}
	cfg := domain.AIConfig{Mode: "live", Strategy: "workflow", Model: "protocol-mock", TimeoutSeconds: 30, OutputTokenLimit: 1024, Version: 1}
	if configPath != "" {
		if _, e = load(configPath, &cfg); e != nil {
			return e
		}
	}
	if cfg.OutputTokenLimit < 256 || cfg.OutputTokenLimit > 4096 || cfg.TimeoutSeconds < 5 || cfg.TimeoutSeconds > 120 {
		return errors.New("invalid engine limits")
	}
	// Execution labels must match the engine path even if a supplied app config
	// happens to have demo selected. A live report can never run the rule baseline.
	if mode != "demo" {
		cfg.Mode = "live"
	}
	if cfg.Pricing != nil && (math.IsNaN(cfg.Pricing.Input) || math.IsNaN(cfg.Pricing.Output) || math.IsInf(cfg.Pricing.Input, 0) || math.IsInf(cfg.Pricing.Output, 0)) {
		return errors.New("invalid pricing")
	}
	rep := Report{Version: buildinfo.Version, SourceHash: buildinfo.SourceHash, CreatedAt: time.Now().UTC().Format(time.RFC3339), Mode: mode, Plan: plan, DatasetVersion: ds.Version, Hashes: manifest.Hashes, PromptVersion: ai.PromptVersion, RetrievalPolicy: ai.RetrievalVersion, Config: cfg, Sampling: map[string]any{"temperature": 0, "order_seed": 20261011, "provider_seed": "unsupported/not requested", "concurrency": 1}, StopReason: "", Trials: []Trial{}}
	rep.Hashes["manifest.json"] = digest(mustRead(filepath.Join(root, "manifest.json")))
	rep.Hashes["input/cases.json"] = digest(b)
	rep.Hashes["input/knowledge.json"] = digest(kbb)
	rep.Hashes["system_prompt"] = digest([]byte(ai.SystemPrompt))
	cases := []Case{}
	seenSlices := map[string]bool{}
	ids := map[string]bool{}
	for _, c := range ds.Cases {
		if ids[c.ID] || c.ID == "" || !domain.Enum(c.Split, "development", "holdout") {
			return errors.New("invalid case identity or split")
		}
		ids[c.ID] = true
		if plan == "pilot" && c.Split == "development" && !seenSlices[c.Slice] {
			cases = append(cases, c)
			seenSlices[c.Slice] = true
		}
		if plan == "full" && c.Split == "holdout" {
			cases = append(cases, c)
		}
	}
	if len(ds.Cases) != 64 || plan == "pilot" && len(cases) != 8 || plan == "full" && len(cases) != 48 {
		return errors.New("dataset counts differ from frozen protocol")
	}
	repeats := 1
	if plan == "full" {
		repeats = 2
	}
	for _, c := range cases {
		for repeat := 1; repeat <= repeats; repeat++ {
			for _, strategy := range []string{"workflow", "agent"} {
				rep.Trials = append(rep.Trials, Trial{ID: fmt.Sprintf("%s-%s-%d", c.ID, strategy, repeat), CaseID: c.ID, Slice: c.Slice, Strategy: strategy, Repeat: repeat, Status: "not_attempted", Sources: []domain.Source{}, Calls: []domain.Call{}, Steps: []string{}, HumanReview: "unreviewed"})
				if strategy == "workflow" {
					rep.WorstRequests++
				} else {
					rep.WorstRequests += 4
				}
			}
		}
	}
	rand.New(rand.NewPCG(20261011, 1)).Shuffle(len(rep.Trials), func(i, j int) { rep.Trials[i], rep.Trials[j] = rep.Trials[j], rep.Trials[i] })
	rep.Planned = len(rep.Trials)
	rep.DistinctCases = len(cases)
	if cap <= 0 && mode != "live" {
		cap = int64(rep.WorstRequests)
	}
	rep.RequestCap = cap
	rep.MaxCost = maxCost
	if mode == "plan" {
		rep.Config.BaseURL = "not requested"
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(rep)
	}
	if out == "" {
		return errors.New("execution requires --out pointing to a new directory")
	}
	key := "protocol-mock-no-real-secret"
	policy := platform.Config{Profile: "desktop"}
	var server *httptest.Server
	if mode == "mock" {
		server = httptest.NewServer(http.HandlerFunc(mock))
		defer server.Close()
		cfg.BaseURL = server.URL
	}
	if mode == "demo" {
		cfg.Mode = "demo"
		cfg.BaseURL = ""
		cfg.Model = "rule-baseline"
	}
	if mode == "live" {
		if !acknowledge || cap <= 0 || maxCost <= 0 || math.IsNaN(maxCost) || math.IsInf(maxCost, 0) || cfg.Pricing == nil || cfg.Pricing.Input < 0 || cfg.Pricing.Output < 0 || cfg.Pricing.Input+cfg.Pricing.Output <= 0 || !domain.Enum(cfg.Pricing.Currency, "CNY", "USD") {
			return errors.New("live requires explicit cost acknowledgement, positive request/cost caps and declared pricing")
		}
		key = os.Getenv("FLOWDESK_EVAL_API_KEY")
		if key == "" || len(key) > 4096 || strings.ContainsAny(key, "\r\n\x00") {
			return errors.New("FLOWDESK_EVAL_API_KEY missing or invalid")
		}
		u, e := url.Parse(cfg.BaseURL)
		if e != nil {
			return errors.New("invalid provider base URL")
		}
		host := strings.ToLower(u.Hostname())
		port := u.Port()
		if port == "" {
			port = "443"
		}
		policy = platform.Config{Profile: "server", LiveEnabled: true, EgressHosts: []string{host + ":" + port}}
		if e = ai.ValidateTarget(policy, cfg.BaseURL); e != nil {
			return domain.AsError(e)
		}
	}
	rep.Config = cfg
	if e = os.Mkdir(out, 0700); e != nil {
		return errors.New("output directory already exists or cannot be created")
	}
	if e = platform.PrivateDir(out); e != nil {
		return e
	}
	budget := &ai.RequestBudget{Limit: cap}
	totalCost := 0.0
	byID := map[string]Case{}
	for _, c := range cases {
		byID[c.ID] = c
	}
	checkpoint := func() error {
		rep.Requests = budget.Used()
		raw, e := json.MarshalIndent(rep, "", "  ")
		if e != nil {
			return e
		}
		return platform.AtomicPrivateWrite(filepath.Join(out, "results.json"), append(raw, '\n'))
	}
	if e = checkpoint(); e != nil {
		return e
	}
	for i := range rep.Trials {
		t := &rep.Trials[i]
		if rep.StopReason != "" {
			t.Reason = rep.StopReason
			continue
		}
		if mode != "demo" && budget.Used() >= cap {
			rep.StopReason = "request_limit"
			t.Reason = rep.StopReason
			continue
		}
		if mode == "live" && totalCost >= maxCost {
			rep.StopReason = "estimated_cost_limit"
			t.Reason = rep.StopReason
			continue
		}
		t.Attempted = true
		cfg.Strategy = t.Strategy
		ctx, cancel := context.WithTimeout(ai.WithRequestBudget(context.Background(), budget), time.Duration(cfg.TimeoutSeconds)*time.Second)
		start := time.Now()
		result, runErr := ai.Run(ctx, policy, byID[t.CaseID].Input, cfg, key, kb, ai.Hooks{
			Before: func(context.Context, int, []domain.Source) error {
				if budget.Used() >= cap {
					return domain.E("TOOL_BUDGET_EXCEEDED")
				}
				return nil
			},
			Step: func(_ context.Context, kind, state string) error {
				t.Steps = append(t.Steps, kind+":"+state)
				return nil
			},
		})
		cancel()
		ms := time.Since(start).Milliseconds()
		t.DurationMS = &ms
		t.Calls = result.Calls
		t.Sources = result.Sources
		if runErr == nil {
			t.Status = "succeeded"
			t.Output = &result.Output
		} else {
			t.Status = "failed"
			code := domain.AsError(runErr).Code
			t.ErrorCode = &code
		}
		complete := true
		estimate := 0.0
		for _, call := range t.Calls {
			if !call.UsageComplete || call.InputTokens == nil || call.OutputTokens == nil {
				complete = false
			}
			if complete && cfg.Pricing != nil {
				estimate += (float64(*call.InputTokens)*cfg.Pricing.Input + float64(*call.OutputTokens)*cfg.Pricing.Output) / 1e6
			}
		}
		if mode == "demo" {
			zero := 0.0
			t.EstimatedCost = &zero
		} else if complete && cfg.Pricing != nil && !math.IsInf(estimate, 0) && !math.IsNaN(estimate) {
			t.EstimatedCost = &estimate
			totalCost += estimate
		}
		if mode == "live" && (math.IsInf(estimate, 0) || math.IsNaN(estimate)) {
			rep.StopReason = "usage_or_fee_unknown"
		}
		if mode == "live" && !complete {
			rep.StopReason = "usage_or_fee_unknown"
		}
		if runErr != nil && domain.AsError(runErr).Code == "PROVIDER_QUOTA" {
			rep.StopReason = "provider_quota"
		}
		if e = checkpoint(); e != nil {
			return e
		}
	}
	if e = checkpoint(); e != nil {
		return e
	}
	fmt.Printf("%s completed: planned=%d actual_requests=%d stop=%q. Human grounding remains unreviewed. Results: %s\n", mode, rep.Planned, rep.Requests, rep.StopReason, filepath.Join(out, "results.json"))
	return nil
}
func mustRead(p string) []byte {
	b, e := os.ReadFile(p)
	if e != nil {
		panic(e)
	}
	return b
}
func mock(w http.ResponseWriter, r *http.Request) {
	var req ai.Request
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 128*1024)).Decode(&req) != nil {
		w.WriteHeader(400)
		return
	}
	var in domain.Input
	for _, m := range req.Messages {
		if m.Role == "user" {
			raw, _ := json.Marshal(m.Content)
			if str, ok := m.Content.(string); ok {
				raw = []byte(str)
			}
			var body struct {
				Ticket domain.Input `json:"ticket"`
			}
			if json.Unmarshal(raw, &body) == nil && body.Ticket.TicketID != "" {
				in = body.Ticket
				break
			}
		}
	}
	finish := "stop"
	message := map[string]any{}
	if len(req.Tools) > 0 && len(req.Messages) == 2 {
		finish = "tool_calls"
		message["tool_calls"] = []any{map[string]any{"id": "search-1", "type": "function", "function": map[string]string{"name": "search_knowledge", "arguments": string(mustJSON(map[string]any{"query": in.Title, "limit": 3}))}}}
	} else {
		sources := map[string]bool{}
		for _, m := range req.Messages {
			str, ok := m.Content.(string)
			if !ok {
				continue
			}
			var body struct {
				Knowledge []domain.Source `json:"knowledge"`
			}
			if json.Unmarshal([]byte(str), &body) == nil {
				for _, s := range body.Knowledge {
					sources[s.EvidenceID] = true
				}
			}
			var toolSources []domain.Source
			if m.Role == "tool" && json.Unmarshal([]byte(str), &toolSources) == nil {
				for _, s := range toolSources {
					sources[s.EvidenceID] = true
				}
			}
		}
		evidence := []string{}
		for id := range sources {
			evidence = append(evidence, id)
		}
		sort.Strings(evidence)
		o := domain.AIOutput{Summary: in.Title, Category: "other", Priority: "normal", Reply: "协议模拟输出：请由工作人员核对资料并补充安全的信息。此文本不用于衡量模型业务效果。", EvidenceIDs: evidence, NeedsMoreInformation: domain.Ptr(true)}
		b, _ := json.Marshal(o)
		message["content"] = string(b)
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": message, "finish_reason": finish}}, "usage": map[string]int{"prompt_tokens": 100, "completion_tokens": 60}})
}

func mustJSON(v any) []byte {
	b, e := json.Marshal(v)
	if e != nil {
		panic(e)
	}
	return b
}
