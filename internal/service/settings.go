package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"flowdesk/internal/ai"
	"flowdesk/internal/domain"
	"flowdesk/internal/repository"
	"math"
	"strings"
	"time"
)

type SettingsBody struct {
	domain.AIConfig
	APIKey      string `json:"api_key"`
	ClearAPIKey bool   `json:"clear_api_key"`
}

func (s *Service) Settings(ctx context.Context, a domain.Actor) (domain.Settings, error) {
	if e := Admin(a); e != nil {
		return domain.Settings{}, e
	}
	cfg, key, e := repository.Settings(ctx, s.Store.DB)
	if e != nil {
		return domain.Settings{}, DBError(e)
	}
	return s.settingsDTO(cfg, key), nil
}
func (s *Service) settingsDTO(c domain.AIConfig, key []byte) domain.Settings {
	mask := ""
	if len(key) > 0 {
		mask = "已保存（不显示密钥）"
	}
	return domain.Settings{AIConfig: c, HasAPIKey: len(key) > 0, MaskedKey: mask, KeystoreAvailable: s.Keys.Available(), LiveAllowed: s.Config.Profile == "desktop" || s.Config.LiveEnabled}
}
func (s *Service) PutSettings(ctx context.Context, a domain.Actor, key string, b SettingsBody) (Result, error) {
	if !domain.Enum(b.Mode, "demo", "live") || !domain.Enum(b.Strategy, "workflow", "agent") || b.TimeoutSeconds < 5 || b.TimeoutSeconds > 120 || b.OutputTokenLimit < 256 || b.OutputTokenLimit > 4096 || len(b.APIKey) > 4096 || strings.ContainsAny(b.APIKey, "\r\n\x00") || b.APIKey != "" && b.ClearAPIKey {
		return Result{}, domain.E("VALIDATION_ERROR")
	}
	if b.Pricing != nil {
		p := b.Pricing
		if p.Input < 0 || p.Output < 0 || math.IsNaN(p.Input) || math.IsNaN(p.Output) || math.IsInf(p.Input, 0) || math.IsInf(p.Output, 0) || !domain.Enum(p.Currency, "USD", "CNY") {
			return Result{}, domain.E("VALIDATION_ERROR")
		}
	}
	if b.BaseURL != "" {
		if _, e := ai.Endpoint(b.BaseURL); e != nil {
			return Result{}, e
		}
	}
	if b.Mode == "live" {
		if e := ai.ValidateTarget(s.Config, b.BaseURL); e != nil {
			return Result{}, e
		}
		if !Text(b.Model, 200) {
			return Result{}, domain.E("CONFIG_MISSING")
		}
	}
	out, e := s.mutate(ctx, a, "PUT /api/settings/ai", key, b, adminScope, func(ctx context.Context, tx *sql.Tx, a domain.Actor) (any, int, error) {
		cfg, encrypted, e := repository.Settings(ctx, tx)
		if e != nil {
			return nil, 0, e
		}
		if cfg.Version != b.Version {
			return nil, 0, domain.E("VERSION_CONFLICT")
		}
		if b.APIKey != "" {
			encrypted, e = s.Keys.Encrypt(b.APIKey)
			if e != nil {
				return nil, 0, domain.E("KEYSTORE_UNAVAILABLE")
			}
		}
		if b.ClearAPIKey {
			encrypted = nil
			if _, e = tx.ExecContext(ctx, "UPDATE jobs SET status='failed',error_code='CONFIG_CHANGED',error_stage='configuration',encrypted_secret=NULL,finished_at=? WHERE status IN ('queued','running') AND json_extract(config_json,'$.mode')='live'", repository.Now()); e != nil {
				return nil, 0, e
			}
			if e = repository.ClearTerminatedActive(ctx, tx); e != nil {
				return nil, 0, e
			}
		}
		if b.Mode == "live" && len(encrypted) == 0 && !b.ClearAPIKey {
			return nil, 0, domain.E("CONFIG_MISSING")
		}
		cfg = b.AIConfig
		cfg.Version++
		if _, e = tx.ExecContext(ctx, "UPDATE ai_settings SET config_json=?,encrypted_key=?,version=? WHERE singleton_id=1", repository.JSON(cfg), encrypted, cfg.Version); e != nil {
			return nil, 0, e
		}
		if e = s.audit(ctx, tx, a, nil, "model_settings_updated", map[string]any{"config_version": cfg.Version, "mode": cfg.Mode, "key_cleared": b.ClearAPIKey}); e != nil {
			return nil, 0, e
		}
		return s.settingsDTO(cfg, encrypted), 200, nil
	})
	if e == nil && b.ClearAPIKey {
		if s.CancelJob != nil {
			s.CancelJob("*live")
		}
		s.testMu.Lock()
		if s.testCancel != nil {
			s.testCancel()
		}
		s.testMu.Unlock()
	}
	return out, e
}
func (s *Service) CancelTest(actor string) {
	s.testMu.Lock()
	defer s.testMu.Unlock()
	if s.testActor == actor && s.testCancel != nil {
		s.testCancel()
	}
}
func (s *Service) TestModel(ctx context.Context, a domain.Actor) (any, error) {
	if e := Admin(a); e != nil {
		return nil, e
	}
	if s.StorageBlocked.Load() {
		return nil, domain.E("STORAGE_UNHEALTHY")
	}
	s.testMu.Lock()
	if s.testActive || time.Since(s.testLast) < 5*time.Second {
		s.testMu.Unlock()
		return nil, domain.E("MODEL_TEST_BUSY")
	}
	s.testActive = true
	s.testLast = time.Now()
	run, cancel := context.WithTimeout(ctx, 15*time.Second)
	s.testCancel = cancel
	s.testActor = a.ID
	s.testMu.Unlock()
	defer func() {
		cancel()
		s.testMu.Lock()
		s.testActive = false
		s.testCancel = nil
		s.testActor = ""
		s.testMu.Unlock()
	}()
	cfg, secret, e := repository.Settings(run, s.Store.DB)
	if e != nil {
		return nil, DBError(e)
	}
	if cfg.Mode != "live" {
		return nil, domain.E("CONFIG_MISSING")
	}
	key, e := s.Keys.Decrypt(secret)
	if e != nil {
		return nil, domain.E("KEYSTORE_UNAVAILABLE")
	}
	client, e := ai.Client(s.Config, cfg.BaseURL)
	if e != nil {
		return nil, e
	}
	defer client.CloseIdleConnections()
	var testID int64
	e = s.Store.Tx(run, func(tx *sql.Tx) error {
		current, e := repository.Session(run, tx, a.TokenHash)
		if e != nil {
			return e
		}
		if e = Admin(current); e != nil {
			return e
		}
		if s.StorageBlocked.Load() {
			return domain.E("STORAGE_UNHEALTHY")
		}
		cc, _, e := repository.Settings(run, tx)
		if e != nil {
			return e
		}
		if cc.Version != cfg.Version {
			return domain.E("CONFIG_CHANGED")
		}
		r, e := tx.ExecContext(run, "INSERT INTO connection_tests(actor_id,call_json,created_at) VALUES(?,?,?)", a.ID, repository.JSON(domain.Call{Ordinal: 1, Status: "dispatch_pending"}), repository.Now())
		if e == nil {
			testID, e = r.LastInsertId()
		}
		return e
	})
	if e != nil {
		s.StorageBlocked.Store(true)
		return nil, DBError(e)
	}
	req := ai.Request{Model: cfg.Model, MaxTokens: 256, Temperature: 0, Messages: []ai.Message{{Role: "system", Content: "Return exactly one JSON object: {\"ok\":true}. This is only a basic connection test."}}, ResponseFormat: map[string]string{"type": "json_object"}}
	if cfg.Strategy == "agent" {
		req.Tools = []any{map[string]any{"type": "function", "function": map[string]any{"name": "get_current_ticket", "parameters": map[string]any{"type": "object", "properties": map[string]any{}}}}}
	}
	comp, callErr := ai.Do(run, client, cfg.BaseURL, key, req, 1)
	saveCtx, saveCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer saveCancel()
	_, e = s.Store.DB.ExecContext(saveCtx, "UPDATE connection_tests SET call_json=? WHERE id=?", repository.JSON(comp.Call), testID)
	if e != nil {
		s.StorageBlocked.Store(true)
		return nil, domain.E("STORAGE_WRITE_FAILED")
	}
	if callErr != nil {
		return nil, domain.WithStage(callErr, "connection_test")
	}
	current, e := repository.Session(saveCtx, s.Store.DB, a.TokenHash)
	if e != nil || Admin(current) != nil {
		return nil, domain.E("ACTOR_PERMISSION_CHANGED")
	}
	var ok struct {
		OK bool `json:"ok"`
	}
	if json.Unmarshal([]byte(comp.Content), &ok) != nil || !ok.OK {
		return nil, domain.E("PROVIDER_RESPONSE_INVALID")
	}
	return map[string]any{"basic_call_succeeded": true, "tools_verified": false, "model_call": comp.Call}, nil
}
