package domain

import (
	"embed"
	"encoding/json"
	"errors"
)

//go:embed guides.json
var guideFS embed.FS

type Guide struct {
	Title string   `json:"title"`
	Steps []string `json:"steps"`
}

var Guides = func() map[string]Guide {
	b, _ := guideFS.ReadFile("guides.json")
	var v map[string]Guide
	if e := json.Unmarshal(b, &v); e != nil {
		panic(e)
	}
	return v
}()

type Error struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Stage     string `json:"stage,omitempty"`
	Retryable bool   `json:"retryable"`
	GuideID   string `json:"guide_id"`
	Status    int    `json:"-"`
}

func E(code string) *Error {
	g, ok := Guides[code]
	if !ok {
		code = "UNKNOWN_ERROR"
		g = Guides[code]
	}
	status := 400
	switch code {
	case "AUTH_REQUIRED", "SESSION_EXPIRED", "LOGIN_FAILED":
		status = 401
	case "FORBIDDEN", "LIVE_DISABLED", "ORIGIN_BLOCKED":
		status = 403
	case "METHOD_NOT_ALLOWED":
		status = 405
	case "NOT_FOUND":
		status = 404
	case "VERSION_CONFLICT", "ACTIVE_JOB_EXISTS", "INVALID_TRANSITION", "DRAFT_STALE", "DRAFT_REVIEWED", "REVIEW_CONTEXT_CHANGED", "REVIEW_ACK_REQUIRED", "IDEMPOTENCY_CONFLICT", "CONFIG_CHANGED", "CONFIG_VERSION_CONFLICT":
		status = 409
	case "LOGIN_RATE_LIMITED", "MODEL_TEST_BUSY", "QUEUE_FULL":
		status = 429
	case "STORAGE_BUSY", "STORAGE_WRITE_FAILED", "STORAGE_UNHEALTHY", "STORAGE_OUTCOME_UNKNOWN", "KEYSTORE_UNAVAILABLE":
		status = 503
	case "UNKNOWN_ERROR":
		status = 500
	}
	return &Error{Code: code, Message: g.Title, Status: status, GuideID: code, Retryable: Enum(code, "VERSION_CONFLICT", "STORAGE_BUSY", "STORAGE_WRITE_FAILED", "STORAGE_OUTCOME_UNKNOWN", "PROVIDER_NETWORK", "PROVIDER_TIMEOUT", "PROVIDER_RATE_LIMIT", "PROVIDER_UNAVAILABLE", "CLIENT_NETWORK")}
}
func (e *Error) Error() string { return e.Code }
func WithStage(e error, stage string) error {
	var d *Error
	if errors.As(e, &d) {
		cp := *d
		cp.Stage = stage
		return &cp
	}
	return &Error{Code: "UNKNOWN_ERROR", Message: Guides["UNKNOWN_ERROR"].Title, GuideID: "UNKNOWN_ERROR", Stage: stage, Status: 500}
}
func AsError(e error) *Error {
	var d *Error
	if errors.As(e, &d) {
		return d
	}
	return E("UNKNOWN_ERROR")
}
