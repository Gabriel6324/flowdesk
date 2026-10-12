package worker_test

import (
	"context"
	"encoding/json"
	"flowdesk/internal/ai"
	"flowdesk/internal/domain"
	"flowdesk/internal/repository"
	"flowdesk/internal/service"
	"flowdesk/internal/testkit"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestMockLiveHasDurableIntentFrozenSourcesUsageAndClearedSecret(t *testing.T) {
	f := testkit.New(t)
	var requests atomic.Int64
	const key = "synthetic-test-key-never-real"
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.URL.Path != "/chat/completions" || r.Header.Get("Authorization") != "Bearer "+key {
			t.Error("incorrect provider request")
		}
		var intents, sources int
		if e := f.Store.DB.QueryRow("SELECT COUNT(*) FROM model_calls WHERE json_extract(call_json,'$.status')='dispatch_pending'").Scan(&intents); e != nil || intents != 1 {
			t.Error("HTTP before durable intent", intents, e)
		}
		if e := f.Store.DB.QueryRow("SELECT COUNT(*) FROM job_source_snapshots").Scan(&sources); e != nil || sources < 1 {
			t.Error("missing frozen source", sources, e)
		}
		var req ai.Request
		if e := json.NewDecoder(r.Body).Decode(&req); e != nil {
			t.Error(e)
		}
		if len(req.Tools) != 0 || len(req.Messages) != 3 {
			t.Error("workflow protocol", req)
		}
		output := domain.AIOutput{Summary: "登录问题", Category: "account", Priority: "normal", Reply: "请按公开说明检查登录信息；请勿发送密码。", EvidenceIDs: []string{"K1"}, NeedsMoreInformation: domain.Ptr(true)}
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": repository.JSON(output)}, "finish_reason": "stop"}}, "usage": map[string]int{"prompt_tokens": 120, "completion_tokens": 40}})
	}))
	defer provider.Close()
	cfg, _, e := repository.Settings(context.Background(), f.Store.DB)
	if e != nil {
		t.Fatal(e)
	}
	cfg.Mode = "live"
	cfg.Strategy = "workflow"
	cfg.Model = "local-protocol-mock"
	cfg.BaseURL = provider.URL
	if _, e = f.Service.PutSettings(context.Background(), f.Admin, "configure-live", service.SettingsBody{AIConfig: cfg, APIKey: key}); e != nil {
		t.Fatal(e)
	}
	start(t, f)
	j := job(t, f, "1")
	succeeded(t, f, j.ID)
	v, e := repository.Job(context.Background(), f.Store.DB, j.ID)
	if e != nil || v.Secret != nil {
		t.Fatal(v, e)
	}
	detail, e := f.Service.Job(context.Background(), f.Agent, j.ID)
	if e != nil || requests.Load() != 1 || len(detail.Calls) != 1 || !detail.Calls[0].UsageComplete || *detail.Calls[0].InputTokens != 120 {
		t.Fatal(detail, e)
	}
	drafts, e := f.Service.Drafts(context.Background(), f.Agent, "1")
	if e != nil || len(drafts) != 1 || len(drafts[0].Sources) < 1 {
		t.Fatal(drafts, e)
	}
	if strings.Contains(repository.JSON(detail), key) || strings.Contains(repository.JSON(drafts), key) {
		t.Fatal("secret in public DTO")
	}
}
