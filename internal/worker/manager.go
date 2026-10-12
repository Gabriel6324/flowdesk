package worker

import (
	"context"
	"database/sql"
	"errors"
	"flowdesk/internal/ai"
	"flowdesk/internal/domain"
	"flowdesk/internal/repository"
	"flowdesk/internal/service"
	"fmt"
	"sync"
	"time"
)

type running struct {
	cancel      context.CancelFunc
	actor, mode string
}
type pending struct {
	job    domain.JobRecord
	result ai.Result
	err    error
}
type Manager struct {
	s       *service.Service
	ctx     context.Context
	cancel  context.CancelFunc
	wake    chan struct{}
	wg      sync.WaitGroup
	mu      sync.Mutex
	running map[string]running
	pending map[string]pending
	saving  map[string]bool
	Engine  func(context.Context, domain.JobRecord, string, []domain.Article, ai.Hooks) (ai.Result, error)
}

func New(s *service.Service) *Manager {
	ctx, cancel := context.WithCancel(context.Background())
	m := &Manager{s: s, ctx: ctx, cancel: cancel, wake: make(chan struct{}, 1), running: map[string]running{}, pending: map[string]pending{}, saving: map[string]bool{}}
	m.Engine = func(ctx context.Context, j domain.JobRecord, key string, kb []domain.Article, h ai.Hooks) (ai.Result, error) {
		return ai.Run(ctx, s.Config, j.Input, j.Config, key, kb, h)
	}
	s.Wake = m.Wake
	s.CancelJob = m.Cancel
	s.CancelActor = m.CancelActor
	s.RetrySave = m.RetrySave
	return m
}
func (m *Manager) Start() {
	m.wg.Add(3)
	for i := 0; i < 2; i++ {
		go m.loop()
	}
	go m.probe()
	m.Wake()
}
func (m *Manager) Wake() {
	select {
	case m.wake <- struct{}{}:
	default:
	}
}
func (m *Manager) Stop(timeout time.Duration) bool {
	m.s.Stopping.Store(true)
	m.cancel()
	done := make(chan struct{})
	go func() { m.wg.Wait(); close(done) }()
	select {
	case <-done:
		return true
	case <-time.After(timeout):
		return false
	}
}
func (m *Manager) Cancel(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	terminal := func(jid string) bool {
		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		defer cancel()
		j, e := repository.Job(ctx, m.s.Store.DB, jid)
		return e == nil && !domain.Enum(j.Status, "queued", "running")
	}
	for jid, r := range m.running {
		if (jid == id || id == "*live" && r.mode == "live") && terminal(jid) {
			r.cancel()
		}
	}
	for jid, p := range m.pending {
		if (jid == id || id == "*live" && p.job.Mode == "live") && terminal(jid) {
			delete(m.pending, jid)
		}
	}
}
func (m *Manager) CancelActor(actor string) {
	m.s.CancelTest(actor)
	m.mu.Lock()
	defer m.mu.Unlock()
	terminal := func(id string) bool {
		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		defer cancel()
		j, e := repository.Job(ctx, m.s.Store.DB, id)
		return e == nil && !domain.Enum(j.Status, "queued", "running")
	}
	for id, r := range m.running {
		if r.actor == actor && terminal(id) {
			r.cancel()
		}
	}
	for id, p := range m.pending {
		if p.job.ActorID == actor && terminal(id) {
			delete(m.pending, id)
		}
	}
}
func (m *Manager) Hint(id string) *domain.RuntimeHint {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.pending[id]
	if !ok {
		return nil
	}
	status, _ := intended(p.result, p.err)
	return &domain.RuntimeHint{StorageBlocked: true, IntendedTerminalState: status, PendingResultAvailable: true, ErrorCode: "STORAGE_WRITE_FAILED"}
}
func (m *Manager) PendingCount() int { m.mu.Lock(); defer m.mu.Unlock(); return len(m.pending) }
func (m *Manager) loop() {
	defer m.wg.Done()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-m.ctx.Done():
			return
		default:
		}
		if !m.s.StorageBlocked.Load() && !m.s.Stopping.Load() {
			j, ok, e := m.claim()
			if e != nil {
				m.s.StorageBlocked.Store(true)
			}
			if ok {
				m.execute(j)
				continue
			}
		}
		select {
		case <-m.ctx.Done():
			return
		case <-m.wake:
		case <-ticker.C:
		}
	}
}
func (m *Manager) probe() {
	defer m.wg.Done()
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-m.ctx.Done():
			return
		case <-ticker.C:
			if !m.s.StorageBlocked.Load() || m.PendingCount() > 0 {
				continue
			}
			ctx, cancel := context.WithTimeout(m.ctx, 500*time.Millisecond)
			e := m.s.Store.Probe(ctx)
			cancel()
			if e == nil {
				m.s.StorageBlocked.Store(false)
				m.Wake()
			}
		}
	}
}
func current(ctx context.Context, q repository.Executor, j domain.JobRecord, cfgProfile string, liveEnabled bool) error {
	a, e := repository.User(ctx, q, j.ActorID)
	if e != nil {
		return e
	}
	if a.Disabled || !domain.Staff(a) || a.AuthorizationVersion != j.ActorAuthorizationVersion {
		return domain.E("ACTOR_PERMISSION_CHANGED")
	}
	t, e := repository.Ticket(ctx, q, j.TicketID)
	if e != nil {
		return e
	}
	if !t.Version.Equal(j.Input.Version) {
		return domain.E("DRAFT_STALE")
	}
	if j.Mode == "live" && cfgProfile == "server" && !liveEnabled {
		return domain.E("LIVE_DISABLED")
	}
	return nil
}
func (m *Manager) claim() (job domain.JobRecord, ok bool, err error) {
	ctx, cancel := context.WithTimeout(m.ctx, time.Second)
	defer cancel()
	err = m.s.Store.Tx(ctx, func(tx *sql.Tx) error {
		var id int64
		e := tx.QueryRowContext(ctx, "SELECT id FROM jobs WHERE status='queued' ORDER BY id LIMIT 1").Scan(&id)
		if errors.Is(e, sql.ErrNoRows) {
			return nil
		}
		if e != nil {
			return e
		}
		job, e = repository.Job(ctx, tx, repository.ID(id))
		if e != nil {
			return e
		}
		if e = current(ctx, tx, job, m.s.Config.Profile, m.s.Config.LiveEnabled); e != nil {
			code := domain.AsError(e).Code
			status := "failed"
			if code == "DRAFT_STALE" {
				status = "stale"
			}
			if _, e = tx.ExecContext(ctx, "UPDATE jobs SET status=?,error_code=?,error_stage='claim',encrypted_secret=NULL,finished_at=? WHERE id=? AND status='queued'", status, code, repository.Now(), job.ID); e != nil {
				return e
			}
			return service.ClearActive(ctx, tx, job.TicketID, job.ID)
		}
		now := repository.Now()
		if _, e = tx.ExecContext(ctx, "UPDATE jobs SET status='running',started_at=? WHERE id=? AND status='queued'", now, job.ID); e != nil {
			return e
		}
		job.Status = "running"
		job.StartedAt = &now
		ok = true
		_, e = tx.ExecContext(ctx, "INSERT INTO job_steps VALUES(?,1,'input_snapshot','completed',?,?)", job.ID, now, now)
		return e
	})
	if err != nil {
		var ce *repository.CommitError
		if errors.As(err, &ce) && job.ID != "" {
			check, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			j, e := repository.Job(check, m.s.Store.DB, job.ID)
			if e == nil && j.Status == "running" {
				return j, true, nil
			}
			if e == nil && domain.Enum(j.Status, "failed", "stale") {
				return j, false, nil
			}
		}
		ok = false
	}
	return
}
func (m *Manager) execute(job domain.JobRecord) {
	ctx, cancel := context.WithTimeout(m.ctx, time.Duration(job.Config.TimeoutSeconds)*time.Second)
	m.mu.Lock()
	m.running[job.ID] = running{cancel: cancel, actor: job.ActorID, mode: job.Mode}
	m.mu.Unlock()
	defer func() { cancel(); m.mu.Lock(); delete(m.running, job.ID); m.mu.Unlock() }()
	var kb []domain.Article
	err := m.s.Store.Tx(ctx, func(tx *sql.Tx) error {
		j, e := repository.Job(ctx, tx, job.ID)
		if e != nil {
			return e
		}
		if j.Status != "running" {
			return domain.E("INVALID_TRANSITION")
		}
		if e = current(ctx, tx, job, m.s.Config.Profile, m.s.Config.LiveEnabled); e != nil {
			return e
		}
		kb, e = repository.Knowledge(ctx, tx, false)
		if e == nil && len(kb) > 200 {
			return fmt.Errorf("knowledge invariant")
		}
		return e
	})
	var result ai.Result
	if err != nil {
		var safe *domain.Error
		if !errors.As(err, &safe) {
			m.s.StorageBlocked.Store(true)
			err = domain.WithStage(service.DBError(err), "load_knowledge")
		}
	}
	key := ""
	if err == nil && job.Mode == "live" {
		key, err = m.s.Keys.Decrypt(job.Secret)
		if err != nil {
			err = domain.E("KEYSTORE_UNAVAILABLE")
		}
	}
	if err == nil {
		step := 2
		hooks := ai.Hooks{Before: func(ctx context.Context, n int, sources []domain.Source) error {
			if m.s.StorageBlocked.Load() {
				return domain.E("STORAGE_UNHEALTHY")
			}
			e := m.s.Store.Tx(ctx, func(tx *sql.Tx) error {
				j, e := repository.Job(ctx, tx, job.ID)
				if e != nil {
					return e
				}
				if j.Status != "running" {
					return domain.E("INVALID_TRANSITION")
				}
				if e = current(ctx, tx, job, m.s.Config.Profile, m.s.Config.LiveEnabled); e != nil {
					return e
				}
				if m.s.StorageBlocked.Load() {
					return domain.E("STORAGE_UNHEALTHY")
				}
				if _, e = tx.ExecContext(ctx, "INSERT INTO model_calls VALUES(?,?,?)", job.ID, n, repository.JSON(domain.Call{Ordinal: n, Status: "dispatch_pending"})); e != nil {
					return e
				}
				for _, s := range sources {
					if _, e = tx.ExecContext(ctx, "INSERT INTO job_source_snapshots VALUES(?,?,?,?,?,?,?) ON CONFLICT(job_id,evidence_id) DO NOTHING", job.ID, s.ArticleID, s.EvidenceID, s.Version, s.Title, s.Excerpt, s.BodyHash); e != nil {
						return e
					}
				}
				_, e = tx.ExecContext(ctx, "INSERT INTO job_steps VALUES(?,?,?,'dispatch_pending',?,NULL)", job.ID, step, fmt.Sprintf("model_request_%d", n), repository.Now())
				return e
			})
			step++
			if e != nil {
				var de *domain.Error
				if !errors.As(e, &de) {
					m.s.StorageBlocked.Store(true)
					return domain.E("STORAGE_WRITE_FAILED")
				}
			}
			return e
		}, After: func(_ context.Context, c domain.Call) error {
			save, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
			defer cancel()
			_, e := m.s.Store.DB.ExecContext(save, "UPDATE model_calls SET call_json=? WHERE job_id=? AND ordinal=?", repository.JSON(c), job.ID, c.Ordinal)
			if e != nil {
				m.s.StorageBlocked.Store(true)
			}
			return nil
		}, Step: func(ctx context.Context, kind, state string) error {
			now := repository.Now()
			_, e := m.s.Store.DB.ExecContext(ctx, "INSERT INTO job_steps VALUES(?,?,?,?,?,?)", job.ID, step, kind, state, now, now)
			step++
			if e != nil {
				m.s.StorageBlocked.Store(true)
				return domain.E("STORAGE_WRITE_FAILED")
			}
			return nil
		}}
		result, err = m.Engine(ctx, job, key, kb, hooks)
	}
	kb = nil
	key = ""
	// Preserve classified storage/config/tool failures and their precise stages.
	// Provider body/header deadlines are classified at the provider boundary.
	if errors.Is(err, context.DeadlineExceeded) {
		err = domain.WithStage(domain.E("PROVIDER_TIMEOUT"), "execution")
	}
	if m.s.Stopping.Load() && err != nil {
		err = domain.E("SHUTDOWN_INTERRUPTED")
	}
	job.Secret = nil
	_, saveErr := m.finalize(job, result, err, nil)
	if saveErr != nil {
		m.s.StorageBlocked.Store(true)
		m.mu.Lock()
		if len(m.pending) < 2 {
			m.pending[job.ID] = pending{job: job, result: result, err: err}
		}
		m.mu.Unlock()
	}
}
func intended(r ai.Result, e error) (string, *domain.Error) {
	if e == nil {
		return "succeeded", nil
	}
	d := domain.AsError(e)
	if d.Code == "DRAFT_STALE" {
		return "stale", d
	}
	return "failed", d
}
func (m *Manager) finalize(job domain.JobRecord, result ai.Result, runErr error, reviewer *domain.Actor) (domain.Job, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var out domain.Job
	var last error
	for attempt := 0; attempt < 3; attempt++ {
		e := m.s.Store.Tx(ctx, func(tx *sql.Tx) error {
			if reviewer != nil {
				actor, e := repository.Session(ctx, tx, reviewer.TokenHash)
				if e != nil {
					return e
				}
				if e = service.Staff(actor); e != nil {
					return e
				}
			}
			j, e := repository.Job(ctx, tx, job.ID)
			if e != nil {
				return e
			}
			if j.Status != "running" {
				out = j.Job
				return nil
			}
			status, de := intended(result, runErr)
			check := current(ctx, tx, job, m.s.Config.Profile, m.s.Config.LiveEnabled)
			if check != nil {
				status, de = intended(result, check)
			}
			if status == "succeeded" {
				if _, e = ai.ValidateOutput(repository.JSON(result.Output), result.Sources); e != nil {
					status, de = intended(result, e)
				}
			}
			for _, c := range result.Calls {
				if _, e = tx.ExecContext(ctx, "INSERT INTO model_calls VALUES(?,?,?) ON CONFLICT(job_id,ordinal) DO UPDATE SET call_json=excluded.call_json", job.ID, c.Ordinal, repository.JSON(c)); e != nil {
					return e
				}
			}
			if status == "succeeded" {
				r, e := tx.ExecContext(ctx, "INSERT INTO drafts(job_id,ticket_id,content_version,conversation_version,cycle_no,provider_mode,output_json,context_omitted,warning_json,created_at) VALUES(?,?,?,?,?,?,?,?,?,?)", job.ID, job.TicketID, job.Input.Content, job.Input.Conversation, job.Input.Cycle, job.Mode, repository.JSON(result.Output), job.Input.ContextOmitted, repository.JSON(nonNil(result.Warnings)), repository.Now())
				if e != nil {
					return e
				}
				did, _ := r.LastInsertId()
				for _, s := range result.Sources {
					if _, e = tx.ExecContext(ctx, "INSERT INTO draft_sources VALUES(?,?,?,?,?,?,?)", did, s.ArticleID, s.EvidenceID, s.Version, s.Title, s.Excerpt, s.BodyHash); e != nil {
						return e
					}
				}
			}
			var code, stage *string
			if de != nil {
				code = &de.Code
				v := de.Stage
				if v == "" {
					v = "execution"
				}
				stage = &v
			}
			now := repository.Now()
			if _, e = tx.ExecContext(ctx, "UPDATE jobs SET status=?,error_code=?,error_stage=?,encrypted_secret=NULL,finished_at=? WHERE id=? AND status='running'", status, code, stage, now, job.ID); e != nil {
				return e
			}
			if _, e = tx.ExecContext(ctx, "UPDATE job_steps SET state=CASE WHEN state='dispatch_pending' THEN 'completed' ELSE state END,finished_at=COALESCE(finished_at,?) WHERE job_id=?", now, job.ID); e != nil {
				return e
			}
			if _, e = tx.ExecContext(ctx, "INSERT INTO job_steps(job_id,ordinal,kind,state,started_at,finished_at) SELECT ?,COALESCE(MAX(ordinal),0)+1,'save_result',?,?,? FROM job_steps WHERE job_id=?", job.ID, status, now, now, job.ID); e != nil {
				return e
			}
			if e = service.ClearActive(ctx, tx, job.TicketID, job.ID); e != nil {
				return e
			}
			if e = repository.Audit(ctx, tx, &job.TicketID, &job.ActorID, "job_finished", map[string]any{"job_id": job.ID, "status": status, "error_code": code}); e != nil {
				return e
			}
			j, e = repository.Job(ctx, tx, job.ID)
			out = j.Job
			return e
		})
		if e == nil {
			return out, nil
		}
		last = e
		var de *domain.Error
		if errors.As(e, &de) {
			return domain.Job{}, de
		}
		var ce *repository.CommitError
		if errors.As(e, &ce) {
			check, cancel := context.WithTimeout(ctx, time.Second)
			j, readErr := repository.Job(check, m.s.Store.DB, job.ID)
			if readErr == nil && domain.Enum(j.Status, "succeeded", "failed", "stale", "canceled") {
				if j.Status == "succeeded" {
					var n int
					readErr = m.s.Store.DB.QueryRowContext(check, "SELECT COUNT(*) FROM drafts WHERE job_id=?", job.ID).Scan(&n)
					if n != 1 {
						readErr = fmt.Errorf("draft invariant")
					}
				}
				if readErr == nil {
					cancel()
					return j.Job, nil
				}
			}
			cancel()
		}
		if ctx.Err() != nil {
			break
		}
		select {
		case <-ctx.Done():
			return domain.Job{}, last
		case <-time.After(time.Duration(attempt+1) * 40 * time.Millisecond):
		}
	}
	return domain.Job{}, last
}
func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
func (m *Manager) RetrySave(_ context.Context, a domain.Actor, id string) (domain.Job, error) {
	if e := service.Staff(a); e != nil {
		return domain.Job{}, e
	}
	m.mu.Lock()
	p, ok := m.pending[id]
	busy := m.saving[id]
	if ok && !busy {
		m.saving[id] = true
	}
	m.mu.Unlock()
	if !ok || busy {
		return domain.Job{}, domain.E("INVALID_TRANSITION")
	}
	defer func() { m.mu.Lock(); delete(m.saving, id); m.mu.Unlock() }()
	out, e := m.finalize(p.job, p.result, p.err, &a)
	if e != nil {
		return out, service.DBError(e)
	}
	m.mu.Lock()
	delete(m.pending, id)
	m.mu.Unlock()
	if m.PendingCount() == 0 {
		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		e = m.s.Store.Probe(ctx)
		cancel()
		if e == nil {
			m.s.StorageBlocked.Store(false)
			m.Wake()
		}
	}
	return out, nil
}
