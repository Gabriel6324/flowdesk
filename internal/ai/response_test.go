package ai_test

import (
	"context"
	"encoding/json"
	"flowdesk/internal/ai"
	"flowdesk/internal/platform"
	"flowdesk/internal/testkit"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestBodyDeadlinePreservesUnknownUsageAndPreciseRequestStage(t *testing.T) {
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		_, _ = w.Write([]byte("{"))
		w.(http.Flusher).Flush()
		timer := time.NewTimer(150 * time.Millisecond)
		defer timer.Stop()
		select {
		case <-r.Context().Done():
		case <-timer.C:
		}
	}))
	defer provider.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	result, e := ai.Run(ctx, platform.Config{Profile: "desktop"}, input(), config(provider.URL), "mock-test", nil, ai.Hooks{})
	testkit.Code(t, e, "PROVIDER_TIMEOUT")
	if len(result.Calls) != 1 || result.Calls[0].UsageComplete || result.Calls[0].InputTokens != nil {
		t.Fatal(result)
	}
}
func TestRefusalsStillRecordAvailableUsageAndRejectNegativeUsage(t *testing.T) {
	for _, kind := range []string{"refusal", "content_filter", "negative"} {
		t.Run(kind, func(t *testing.T) {
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				message := map[string]any{"content": final()}
				finish := "stop"
				prompt := 100
				if kind == "refusal" {
					message["refusal"] = "Cannot comply"
				}
				if kind == "content_filter" {
					finish = kind
				}
				if kind == "negative" {
					prompt = -1
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": message, "finish_reason": finish}}, "usage": map[string]int{"prompt_tokens": prompt, "completion_tokens": 20}})
			}))
			defer provider.Close()
			c, e := ai.Client(platform.Config{Profile: "desktop"}, provider.URL)
			if e != nil {
				t.Fatal(e)
			}
			defer c.CloseIdleConnections()
			result, e := ai.Do(context.Background(), c, provider.URL, "mock-test", ai.Request{Model: "mock", Messages: []ai.Message{{Role: "user", Content: "synthetic"}}, MaxTokens: 256}, 1)
			if kind == "negative" {
				testkit.Code(t, e, "PROVIDER_RESPONSE_INVALID")
				if result.Call.InputTokens != nil || result.Call.UsageComplete {
					t.Fatal("invalid usage retained", result)
				}
			} else {
				testkit.Code(t, e, "PROVIDER_REFUSAL")
				if !result.Call.UsageComplete || *result.Call.InputTokens != 100 {
					t.Fatal("valid usage lost on refusal", result)
				}
			}
		})
	}
}
