package ai_test

import (
	"context"
	"encoding/json"
	"flowdesk/internal/ai"
	"flowdesk/internal/domain"
	"flowdesk/internal/platform"
	"flowdesk/internal/testkit"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func config(url string) domain.AIConfig {
	return domain.AIConfig{Mode: "live", Strategy: "agent", BaseURL: url, Model: "mock", TimeoutSeconds: 60, OutputTokenLimit: 256}
}
func input() domain.Input {
	return domain.Input{TicketID: "1", Title: "API 401", Description: "authentication failed", Version: domain.Version{Content: 1, Conversation: 1, Cycle: 1}, Comments: []domain.Comment{}}
}
func final() string {
	return "{\"summary\":\"Authentication issue\",\"suggested_category\":\"technical\",\"suggested_priority\":\"normal\",\"reply_text\":\"Please check authentication safely.\",\"evidence_ids\":[],\"needs_more_information\":true}"
}
func completion(w http.ResponseWriter, content any, tools any) {
	_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": content, "tool_calls": tools}, "finish_reason": "stop"}}, "usage": map[string]any{"prompt_tokens": 100, "completion_tokens": 30}})
}
func tool(id, name, args string) map[string]any {
	return map[string]any{"id": id, "type": "function", "function": map[string]string{"name": name, "arguments": args}}
}
func TestAgentKeepsPairedToolMessagesAndStopsAtFinal(t *testing.T) {
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		var req ai.Request
		if e := json.NewDecoder(r.Body).Decode(&req); e != nil {
			t.Error(e)
		}
		if n == 1 {
			completion(w, nil, []any{tool("t1", "get_current_ticket", "{}")})
			return
		}
		if len(req.Messages) != 4 || req.Messages[2].Role != "assistant" || req.Messages[3].ToolCallID != "t1" {
			t.Error("unpaired tool messages", req.Messages)
		}
		completion(w, final(), nil)
	}))
	defer server.Close()
	result, e := ai.Run(context.Background(), platform.Config{Profile: "desktop"}, input(), config(server.URL), "test-key", nil, ai.Hooks{})
	if e != nil || calls.Load() != 2 || len(result.Calls) != 2 || !result.Calls[0].UsageComplete {
		t.Fatal(result, e, calls.Load())
	}
}
func TestFourthRequestCannotStartAnotherToolRound(t *testing.T) {
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		completion(w, nil, []any{tool(string(rune('0'+n)), "get_current_ticket", "{}")})
	}))
	defer server.Close()
	_, e := ai.Run(context.Background(), platform.Config{Profile: "desktop"}, input(), config(server.URL), "test-key", nil, ai.Hooks{})
	testkit.Code(t, e, "TOOL_BUDGET_EXCEEDED")
	if calls.Load() != 4 {
		t.Fatal(calls.Load())
	}
}
func TestToolGroupValidatedBeforeAnyExecution(t *testing.T) {
	for _, kind := range []string{"cross-ticket", "unknown", "duplicate", "mixed", "oversized"} {
		t.Run(kind, func(t *testing.T) {
			tools := []any{tool("x", "get_current_ticket", "{}")}
			content := any(nil)
			switch kind {
			case "cross-ticket":
				tools = []any{tool("x", "get_current_ticket", "{\"id\":\"2\"}")}
			case "unknown":
				tools = []any{tool("x", "shell", "{}")}
			case "duplicate":
				tools = append(tools, tool("x", "get_current_ticket", "{}"))
			case "mixed":
				content = final()
			case "oversized":
				tools = []any{tool("x", "search_knowledge", strings.Repeat(" ", 8193))}
			}
			var steps atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { completion(w, content, tools) }))
			defer server.Close()
			_, e := ai.Run(context.Background(), platform.Config{Profile: "desktop"}, input(), config(server.URL), "test-key", nil, ai.Hooks{Step: func(context.Context, string, string) error { steps.Add(1); return nil }})
			testkit.Code(t, e, "TOOL_INVALID")
			if steps.Load() != 0 {
				t.Fatal("partially executed invalid group")
			}
		})
	}
}
func TestContextRejectedBeforeCallIntentAndHTTP(t *testing.T) {
	var calls, intents atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1) }))
	defer server.Close()
	in := input()
	in.Description = strings.Repeat("语", 20001)
	_, e := ai.Run(context.Background(), platform.Config{Profile: "desktop"}, in, config(server.URL), "test-key", nil, ai.Hooks{Before: func(context.Context, int, []domain.Source) error { intents.Add(1); return nil }})
	testkit.Code(t, e, "CONTEXT_BUDGET_EXCEEDED")
	if calls.Load() != 0 || intents.Load() != 0 {
		t.Fatal("charged oversized context")
	}
}
func TestOutputRejectsDuplicateMissingAndFalseCitations(t *testing.T) {
	for _, s := range []string{strings.Replace(final(), `"summary":`, `"summary":"duplicate","summary":`, 1), strings.Replace(final(), `,"needs_more_information":true`, "", 1), strings.Replace(final(), `"evidence_ids":[]`, `"evidence_ids":["K999"]`, 1)} {
		_, e := ai.ValidateOutput(s, nil)
		if e == nil {
			t.Fatal("invalid output accepted", s)
		}
	}
}
func TestProviderErrorsNeverExposeRawBodyAndRedirectNeverFollows(t *testing.T) {
	secret := "sensitive-provider-key"
	var targetCalls atomic.Int64
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { targetCalls.Add(1); completion(w, final(), nil) }))
	defer target.Close()
	for _, status := range []int{401, 429, 500, 302} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Location", target.URL)
			w.WriteHeader(status)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"message": secret}})
		}))
		client, _ := ai.Client(platform.Config{Profile: "desktop"}, server.URL)
		_, e := ai.Do(context.Background(), client, server.URL, secret, ai.Request{Model: "x", MaxTokens: 256, Messages: []ai.Message{{Role: "user", Content: "test"}}}, 1)
		b, _ := json.Marshal(domain.AsError(e))
		if e == nil || strings.Contains(string(b), secret) {
			t.Fatal(e, string(b))
		}
		server.Close()
	}
	if targetCalls.Load() != 0 {
		t.Fatal("redirect sent authorization")
	}
}
func TestTimeoutKeepsUnknownUsage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(100 * time.Millisecond):
		}
	}))
	defer server.Close()
	client, _ := ai.Client(platform.Config{Profile: "desktop"}, server.URL)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	r, e := ai.Do(ctx, client, server.URL, "x", ai.Request{Model: "x", MaxTokens: 256}, 1)
	testkit.Code(t, e, "PROVIDER_TIMEOUT")
	if r.Call.InputTokens != nil || r.Call.OutputTokens != nil || r.Call.Status != "unknown" {
		t.Fatal(r.Call)
	}
}
func TestRetrievalDoesNotForceUnrelatedEvidence(t *testing.T) {
	r := ai.NewRetriever([]domain.Article{{ID: "1", Title: "账单核对", Body: "付款记录", Version: 1}})
	out, _ := r.Search("quantum vegetable unicorn", 3)
	if len(out) != 0 || len(r.Sources) != 0 {
		t.Fatal(out)
	}
}
func TestServerRejectsPrivateMappedIPAndNonAllowlistedTargets(t *testing.T) {
	c := platform.Config{Profile: "server", LiveEnabled: true, EgressHosts: []string{"api.example.test:443", "127.0.0.1:443", "[::ffff:127.0.0.1]:443"}}
	for _, url := range []string{"http://api.example.test/v1", "https://different.example.test/v1", "https://127.0.0.1/v1", "https://[::ffff:127.0.0.1]/v1"} {
		if ai.ValidateTarget(c, url) == nil {
			t.Fatal("target accepted", url)
		}
	}
}
