package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flowdesk/internal/domain"
	"flowdesk/internal/jsonx"
	"flowdesk/internal/platform"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"time"
	"unicode/utf8"
)

func Endpoint(base string) (*url.URL, error) {
	u, e := url.Parse(base)
	if e != nil || !domain.Enum(u.Scheme, "http", "https") || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.Opaque != "" {
		return nil, domain.E("PROVIDER_ENDPOINT")
	}
	if u.Port() != "" {
		if _, e := net.LookupPort("tcp", u.Port()); e != nil {
			return nil, domain.E("PROVIDER_ENDPOINT")
		}
	}
	u.Path = strings.TrimRight(u.Path, "/")
	if !strings.HasSuffix(u.Path, "/chat/completions") {
		u.Path += "/chat/completions"
	}
	u.RawPath = ""
	return u, nil
}
func ValidateTarget(c platform.Config, base string) error {
	u, e := Endpoint(base)
	if e != nil {
		return e
	}
	if c.Profile != "server" {
		return nil
	}
	if !c.LiveEnabled {
		return domain.E("LIVE_DISABLED")
	}
	port := u.Port()
	if port == "" {
		port = "443"
	}
	host := strings.ToLower(u.Hostname())
	allowed := false
	for _, s := range c.EgressHosts {
		if strings.ToLower(strings.TrimSpace(s)) == net.JoinHostPort(host, port) {
			allowed = true
		}
	}
	if u.Scheme != "https" || !allowed {
		return domain.E("PROVIDER_TARGET_BLOCKED")
	}
	if ip := net.ParseIP(host); ip != nil && !PublicIP(ip) {
		return domain.E("PROVIDER_TARGET_BLOCKED")
	}
	return nil
}
func PublicIP(ip net.IP) bool {
	if !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsMulticast() || ip.IsUnspecified() {
		return false
	}
	for _, s := range []string{"0.0.0.0/8", "100.64.0.0/10", "192.0.0.0/24", "192.0.2.0/24", "198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24", "240.0.0.0/4", "2001:db8::/32"} {
		_, n, _ := net.ParseCIDR(s)
		if n.Contains(ip) {
			return false
		}
	}
	return true
}
func Client(c platform.Config, base string) (*http.Client, error) {
	if e := ValidateTarget(c, base); e != nil {
		return nil, e
	}
	tr := &http.Transport{Proxy: nil, MaxIdleConns: 4, MaxIdleConnsPerHost: 2, IdleConnTimeout: 30 * time.Second, TLSHandshakeTimeout: 8 * time.Second, ResponseHeaderTimeout: 60 * time.Second}
	d := net.Dialer{Timeout: 8 * time.Second, KeepAlive: 30 * time.Second}
	if c.Profile == "server" {
		tr.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
			host, port, e := net.SplitHostPort(address)
			if e != nil {
				return nil, domain.E("PROVIDER_TARGET_BLOCKED")
			}
			ips, e := net.DefaultResolver.LookupIPAddr(ctx, host)
			if e != nil {
				return nil, e
			}
			if len(ips) == 0 {
				return nil, domain.E("PROVIDER_TARGET_BLOCKED")
			}
			for _, ip := range ips {
				if !PublicIP(ip.IP) {
					return nil, domain.E("PROVIDER_TARGET_BLOCKED")
				}
			}
			var last error
			for _, ip := range ips {
				conn, e := d.DialContext(ctx, network, net.JoinHostPort(ip.IP.String(), port))
				if e == nil {
					return conn, nil
				}
				last = e
			}
			return nil, last
		}
	} else {
		tr.DialContext = d.DialContext
	}
	return &http.Client{Transport: tr, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}, nil
}

type ToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}
type Message struct {
	Role       string     `json:"role"`
	Content    any        `json:"content,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
}
type Request struct {
	Model          string    `json:"model"`
	Messages       []Message `json:"messages"`
	MaxTokens      int       `json:"max_tokens"`
	Temperature    float64   `json:"temperature"`
	Tools          []any     `json:"tools,omitempty"`
	ResponseFormat any       `json:"response_format,omitempty"`
}
type response struct {
	Choices []struct {
		Message struct {
			Content   json.RawMessage `json:"content"`
			Refusal   *string         `json:"refusal"`
			ToolCalls []ToolCall      `json:"tool_calls"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage *struct {
		Input   *int64 `json:"prompt_tokens"`
		Output  *int64 `json:"completion_tokens"`
		Details *struct {
			Cached *int64 `json:"cached_tokens"`
		} `json:"prompt_tokens_details"`
	} `json:"usage"`
}
type Completion struct {
	Content string
	Tools   []ToolCall
	Call    domain.Call
}

// RequestBudget is an optional independent-harness ceiling, shared by every
// engine run in that harness. The application uses its per-job engine bounds.
type RequestBudget struct {
	Limit int64
	used  atomic.Int64
}
type requestBudgetKey struct{}

func WithRequestBudget(ctx context.Context, b *RequestBudget) context.Context {
	return context.WithValue(ctx, requestBudgetKey{}, b)
}
func (b *RequestBudget) Used() int64 { return b.used.Load() }
func (b *RequestBudget) take() bool {
	for {
		n := b.used.Load()
		if n >= b.Limit {
			return false
		}
		if b.used.CompareAndSwap(n, n+1) {
			return true
		}
	}
}

func classify(status int, b []byte) error {
	var raw struct {
		Error struct {
			Code  string `json:"code"`
			Type  string `json:"type"`
			Param string `json:"param"`
		} `json:"error"`
	}
	_ = json.Unmarshal(b, &raw)
	code := raw.Error.Code
	switch {
	case status == 401 || status == 403:
		return domain.E("PROVIDER_AUTH")
	case status == 429:
		if domain.Enum(code, "insufficient_quota", "quota_exceeded", "billing_hard_limit_reached") {
			return domain.E("PROVIDER_QUOTA")
		}
		return domain.E("PROVIDER_RATE_LIMIT")
	case status == 404:
		if code == "model_not_found" {
			return domain.E("PROVIDER_MODEL_NOT_FOUND")
		}
		return domain.E("PROVIDER_ENDPOINT")
	case status == 400 || status == 422:
		if raw.Error.Param == "tools" || code == "unsupported_tools" {
			return domain.E("PROVIDER_UNSUPPORTED_TOOLS")
		}
		if code == "model_not_found" {
			return domain.E("PROVIDER_MODEL_NOT_FOUND")
		}
		return domain.E("PROVIDER_PARAMETER_INVALID")
	case status >= 500:
		return domain.E("PROVIDER_UNAVAILABLE")
	default:
		return domain.E("PROVIDER_ENDPOINT")
	}
}
func Do(ctx context.Context, client *http.Client, base, key string, r Request, ordinal int) (out Completion, err error) {
	out.Call = domain.Call{Ordinal: ordinal, Status: "dispatch_pending"}
	b, e := json.Marshal(r)
	if e != nil {
		return out, e
	}
	if len(b) > 128*1024 || utf8.RuneCount(b) > 20000 {
		return out, domain.E("CONTEXT_BUDGET_EXCEEDED")
	}
	u, e := Endpoint(base)
	if e != nil {
		return out, e
	}
	req, e := http.NewRequestWithContext(ctx, "POST", u.String(), bytes.NewReader(b))
	if e != nil {
		return out, domain.E("PROVIDER_ENDPOINT")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+key)
	if budget, ok := ctx.Value(requestBudgetKey{}).(*RequestBudget); ok && !budget.take() {
		out.Call.Status = "not_sent"
		return out, domain.E("TOOL_BUDGET_EXCEEDED")
	}
	start := time.Now()
	defer func() { out.Call.DurationMS = time.Since(start).Milliseconds() }()
	resp, e := client.Do(req)
	defer func() {
		if err != nil {
			code := domain.AsError(err).Code
			out.Call.ErrorCode = &code
			if out.Call.Status == "dispatch_pending" {
				out.Call.Status = "unknown"
			}
		}
	}()
	if e != nil {
		var de *domain.Error
		if errors.As(e, &de) {
			return out, de
		}
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return out, domain.E("PROVIDER_TIMEOUT")
		}
		return out, domain.E("PROVIDER_NETWORK")
	}
	out.Call.DispatchConfirmed = true
	defer resp.Body.Close()
	b, e = io.ReadAll(io.LimitReader(resp.Body, 1024*1024+1))
	out.Call.Status = "response_received"
	if e != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return out, domain.E("PROVIDER_TIMEOUT")
		}
		return out, domain.E("PROVIDER_NETWORK")
	}
	if len(b) > 1024*1024 || !utf8.Valid(b) {
		return out, domain.E("PROVIDER_RESPONSE_INVALID")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return out, classify(resp.StatusCode, b)
	}
	if jsonx.Check(b) != nil {
		return out, domain.E("PROVIDER_RESPONSE_INVALID")
	}
	var v response
	if e = json.Unmarshal(b, &v); e != nil || len(v.Choices) != 1 {
		return out, domain.E("PROVIDER_RESPONSE_INVALID")
	}
	if v.Usage != nil {
		out.Call.InputTokens = v.Usage.Input
		out.Call.OutputTokens = v.Usage.Output
		if v.Usage.Details != nil {
			out.Call.CachedTokens = v.Usage.Details.Cached
		}
		if out.Call.InputTokens != nil && *out.Call.InputTokens < 0 || out.Call.OutputTokens != nil && *out.Call.OutputTokens < 0 || out.Call.CachedTokens != nil && *out.Call.CachedTokens < 0 {
			out.Call.InputTokens = nil
			out.Call.OutputTokens = nil
			out.Call.CachedTokens = nil
			return out, domain.E("PROVIDER_RESPONSE_INVALID")
		}
		if out.Call.CachedTokens != nil && out.Call.InputTokens != nil && *out.Call.CachedTokens > *out.Call.InputTokens {
			out.Call.CachedTokens = nil
			return out, domain.E("PROVIDER_RESPONSE_INVALID")
		}
		out.Call.UsageComplete = out.Call.InputTokens != nil && out.Call.OutputTokens != nil
	}
	m := v.Choices[0].Message
	if m.Refusal != nil && *m.Refusal != "" || v.Choices[0].FinishReason == "content_filter" {
		return out, domain.E("PROVIDER_REFUSAL")
	}
	if len(m.Content) > 0 && string(m.Content) != "null" {
		if e = json.Unmarshal(m.Content, &out.Content); e != nil {
			return out, domain.E("PROVIDER_RESPONSE_INVALID")
		}
	}
	out.Tools = m.ToolCalls
	if v.Choices[0].FinishReason == "length" {
		return out, domain.E("OUTPUT_INVALID")
	}
	if out.Content == "" && len(out.Tools) == 0 {
		return out, domain.E("PROVIDER_RESPONSE_INVALID")
	}
	out.Call.Status = "completed"
	return out, nil
}
