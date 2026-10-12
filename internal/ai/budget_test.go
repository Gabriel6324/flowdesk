package ai_test

import (
	"context"
	"flowdesk/internal/ai"
	"flowdesk/internal/platform"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestHarnessRequestCeilingAlsoEnforcedAtProviderBoundary(t *testing.T) {
	var requests atomic.Int64
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1); completion(w, final(), nil) }))
	defer provider.Close()
	c, e := ai.Client(platform.Config{Profile: "desktop"}, provider.URL)
	if e != nil {
		t.Fatal(e)
	}
	defer c.CloseIdleConnections()
	budget := &ai.RequestBudget{Limit: 1}
	ctx := ai.WithRequestBudget(context.Background(), budget)
	req := ai.Request{Model: "mock", Messages: []ai.Message{{Role: "user", Content: "synthetic"}}, MaxTokens: 256}
	if _, e := ai.Do(ctx, c, provider.URL, "mock-key", req, 1); e != nil {
		t.Fatal(e)
	}
	result, e := ai.Do(ctx, c, provider.URL, "mock-key", req, 2)
	if e == nil || budget.Used() != 1 || requests.Load() != 1 || result.Call.Status != "not_sent" {
		t.Fatal(result, e, requests.Load())
	}
}
