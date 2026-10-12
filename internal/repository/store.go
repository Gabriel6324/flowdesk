package repository

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flowdesk/internal/auth"
	"flowdesk/internal/domain"
	"flowdesk/internal/platform"
	"fmt"
	_ "modernc.org/sqlite"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

//go:embed schema.sql
var schemaFS embed.FS

const SchemaVersion = 1

type Executor interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}
type Store struct {
	DB                                       *sql.DB
	WorkspaceID, Profile, SQLiteVersion, Dir string
	CommitHook                               func(*sql.Tx) error
}
type Bootstrap struct {
	Email, DisplayName, Password string
	InitAdmin                    bool
}
type CommitError struct{ Err error }

func (e *CommitError) Error() string { return "commit result uncertain" }
func (e *CommitError) Unwrap() error { return e.Err }
func Now() string                    { return time.Now().UTC().Format(time.RFC3339Nano) }
func JSON(v any) string {
	b, e := json.Marshal(v)
	if e != nil {
		panic(e)
	}
	return string(b)
}
func Sum(v string) string { h := sha256.Sum256([]byte(v)); return hex.EncodeToString(h[:]) }
func ID(n int64) string   { return strconv.FormatInt(n, 10) }
func ParseID(s string) (int64, error) {
	v, e := strconv.ParseInt(s, 10, 64)
	if e != nil || v <= 0 || ID(v) != s {
		return 0, domain.E("VALIDATION_ERROR")
	}
	return v, nil
}
func NullID(n sql.NullInt64) *string {
	if !n.Valid {
		return nil
	}
	return domain.Ptr(ID(n.Int64))
}
func NullString(s sql.NullString) *string {
	if !s.Valid {
		return nil
	}
	return &s.String
}
func DSN(p string, ro bool) string {
	// A Windows drive is a URI path, never an authority. Preserve literal
	// backslashes in Unix paths; normalize only recognizable Windows drives.
	drivePath := strings.TrimPrefix(p, "\\\\?\\")
	if len(drivePath) >= 3 && ((drivePath[0] >= 'A' && drivePath[0] <= 'Z') || (drivePath[0] >= 'a' && drivePath[0] <= 'z')) && drivePath[1] == ':' && (drivePath[2] == '\\' || drivePath[2] == '/') {
		p = "/" + strings.ReplaceAll(drivePath, "\\", "/")
	}
	u := url.URL{Scheme: "file", Path: p, OmitHost: !strings.HasPrefix(p, "/")}
	q := "_pragma=foreign_keys(1)&_pragma=busy_timeout(250)"
	if ro {
		q += "&mode=ro"
	}
	return u.String() + "?" + q
}
func checksum() string { b, _ := schemaFS.ReadFile("schema.sql"); return Sum(string(b)) }

var schemaObjectPattern = regexp.MustCompile("(?m)^CREATE (?:UNIQUE )?(TABLE|INDEX) ([A-Za-z_][A-Za-z0-9_]*)")

func pristineSchema(ctx context.Context, db *sql.DB) error {
	schema, _ := schemaFS.ReadFile("schema.sql")
	expected := map[string]string{}
	for _, match := range schemaObjectPattern.FindAllStringSubmatch(string(schema), -1) {
		expected[match[2]] = strings.ToLower(match[1])
	}
	rows, e := db.QueryContext(ctx, "SELECT name,type FROM sqlite_schema WHERE name NOT GLOB 'sqlite_*'")
	if e != nil {
		return databaseFailure("inspect_initialization", e)
	}
	defer rows.Close()
	seen := 0
	for rows.Next() {
		var name, kind string
		if e = rows.Scan(&name, &kind); e != nil {
			return databaseFailure("inspect_initialization", e)
		}
		if expected[name] != kind {
			return &DatabaseError{Code: "SCHEMA_INVALID", Operation: "foreign_database"}
		}
		seen++
	}
	if e = rows.Err(); e != nil {
		return databaseFailure("inspect_initialization", e)
	}
	if seen != len(expected) {
		return &DatabaseError{Code: "SCHEMA_INVALID", Operation: "foreign_database"}
	}
	return nil
}

// DatabaseError keeps a safe startup code/stage separate from the underlying
// driver error. A failed read must not be reported as a schema mismatch.
type DatabaseError struct {
	Code, Operation string
	Err             error
}

func (e *DatabaseError) Error() string { return e.Code + ": " + e.Operation }
func (e *DatabaseError) Unwrap() error { return e.Err }
func databaseFailure(operation string, err error) error {
	code := "DATABASE_QUERY_FAILED"
	var sqliteError interface{ Code() int }
	if errors.As(err, &sqliteError) {
		switch sqliteError.Code() & 0xff {
		case 11, 26:
			code = "DATABASE_CORRUPT"
		case 14:
			code = "DATABASE_OPEN_FAILED"
		case 3, 8, 10, 13:
			code = "DATABASE_IO_FAILED"
		case 5, 6:
			code = "DATABASE_BUSY"
		case 1:
			code = "SCHEMA_INVALID"
		}
	}
	return &DatabaseError{Code: code, Operation: operation, Err: err}
}

type workspaceMetadata struct {
	workspaceID         string
	exists, schemaReady bool
}

func Preflight(ctx context.Context, path, profile string) (string, bool, error) {
	m, e := inspectWorkspace(ctx, path, profile)
	return m.workspaceID, m.exists, e
}
func inspectWorkspace(ctx context.Context, path, profile string) (workspaceMetadata, error) {
	var m workspaceMetadata
	info, e := os.Stat(path)
	if os.IsNotExist(e) {
		return m, nil
	}
	if e != nil {
		return m, &DatabaseError{Code: "DATABASE_OPEN_FAILED", Operation: "stat_database", Err: e}
	}
	m.exists = true
	if !info.Mode().IsRegular() {
		return m, &DatabaseError{Code: "DATABASE_PATH_INVALID", Operation: "stat_database"}
	}
	db, e := sql.Open("sqlite", DSN(path, true))
	if e != nil {
		return m, databaseFailure("open_database", e)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	var objects, userVersion, applicationID int
	if e = db.QueryRowContext(ctx, "SELECT COUNT(*) FROM sqlite_schema").Scan(&objects); e != nil {
		return m, databaseFailure("inspect_schema", e)
	}
	if objects == 0 {
		if e = db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&userVersion); e != nil {
			return m, databaseFailure("inspect_schema", e)
		}
		if e = db.QueryRowContext(ctx, "PRAGMA application_id").Scan(&applicationID); e != nil {
			return m, databaseFailure("inspect_schema", e)
		}
		if userVersion != 0 || applicationID != 0 {
			return m, &DatabaseError{Code: "SCHEMA_INVALID", Operation: "foreign_database"}
		}
		// A zero-byte file or pristine SQLite header can be left by interrupted
		// first launch. Read-only inspection proves that it has no schema/data.
		return m, nil
	}
	var max sql.NullInt64
	if e = db.QueryRowContext(ctx, "SELECT MAX(version) FROM migrations").Scan(&max); e != nil {
		return m, databaseFailure("read_migrations", e)
	}
	if max.Valid && max.Int64 > SchemaVersion {
		return m, &DatabaseError{Code: "SCHEMA_TOO_NEW", Operation: "read_migrations"}
	}
	if !max.Valid || max.Int64 != SchemaVersion {
		return m, &DatabaseError{Code: "SCHEMA_INVALID", Operation: "read_migrations"}
	}
	var cs string
	if e = db.QueryRowContext(ctx, "SELECT checksum FROM migrations WHERE version=1").Scan(&cs); e != nil {
		return m, databaseFailure("read_migrations", e)
	}
	if cs != checksum() {
		return m, &DatabaseError{Code: "SCHEMA_CHECKSUM_MISMATCH", Operation: "read_migrations"}
	}
	m.schemaReady = true
	var w, p string
	e = db.QueryRowContext(ctx, "SELECT workspace_id,origin_profile FROM workspace_meta WHERE singleton_id=1").Scan(&w, &p)
	if errors.Is(e, sql.ErrNoRows) {
		// Legacy first launch committed schema before the seed transaction.
		// Resume only when every business table is empty, not when metadata
		// vanished from an existing workspace.
		if e = pristineSchema(ctx, db); e != nil {
			return m, e
		}
		for _, table := range []string{"users", "sessions", "tickets", "comments", "knowledge_articles", "jobs", "job_steps", "model_calls", "connection_tests", "job_source_snapshots", "drafts", "draft_sources", "reviews", "audit_events", "ai_settings", "idempotency", "keystore_recoveries"} {
			var n int
			if e = db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table).Scan(&n); e != nil {
				return m, databaseFailure("inspect_initialization", e)
			}
			if n != 0 {
				return m, &DatabaseError{Code: "SCHEMA_INVALID", Operation: "incomplete_workspace_metadata"}
			}
		}
		return m, nil
	}
	if e != nil {
		return m, databaseFailure("read_workspace", e)
	}
	if w == "" {
		return m, &DatabaseError{Code: "SCHEMA_INVALID", Operation: "read_workspace"}
	}
	if p != profile {
		return m, &DatabaseError{Code: "PROFILE_MISMATCH", Operation: "read_workspace"}
	}
	m.workspaceID = w
	return m, nil
}
func Open(ctx context.Context, c platform.Config, b Bootstrap) (*Store, error) {
	if e := platform.PrivateDir(c.DataDir); e != nil {
		return nil, e
	}
	path := filepath.Join(c.DataDir, "flowdesk.db")
	m, e := inspectWorkspace(ctx, path, c.Profile)
	if e != nil {
		return nil, e
	}
	w, exists := m.workspaceID, m.exists
	if c.Workspace {
		if c.Profile != "desktop" {
			return nil, fmt.Errorf("SERVER_CONFIG_INVALID: workspace requires desktop")
		}
		if w == "" && !b.InitAdmin {
			return nil, fmt.Errorf("WORKSPACE_INITIALIZATION_REQUIRED")
		}
		if w != "" {
			check, err := sql.Open("sqlite", DSN(path, true))
			if err != nil {
				return nil, databaseFailure("inspect_workspace_accounts", err)
			}
			var demo int
			err = check.QueryRowContext(ctx, "SELECT COUNT(*) FROM users WHERE email IN ('admin@flowdesk.demo','agent@flowdesk.demo','customer@flowdesk.demo')").Scan(&demo)
			check.Close()
			if err != nil {
				return nil, databaseFailure("inspect_workspace_accounts", err)
			}
			if demo != 0 {
				return nil, fmt.Errorf("WORKSPACE_DEMO_ACCOUNTS")
			}
		}
		if b.InitAdmin && domain.Enum(strings.ToLower(strings.TrimSpace(b.Email)), "admin@flowdesk.demo", "agent@flowdesk.demo", "customer@flowdesk.demo") {
			return nil, domain.E("VALIDATION_ERROR")
		}
	}
	if c.Profile == "server" && w == "" && !b.InitAdmin {
		return nil, fmt.Errorf("SERVER_BOOTSTRAP_REQUIRED")
	}
	if b.InitAdmin && w != "" {
		return nil, fmt.Errorf("WORKSPACE_ALREADY_INITIALIZED")
	}
	if !exists {
		f, e := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
		if e != nil {
			return nil, e
		}
		f.Close()
	}
	if e = platform.PrivateFile(path); e != nil {
		return nil, e
	}
	db, e := sql.Open("sqlite", DSN(path, false))
	if e != nil {
		return nil, databaseFailure("open_database", e)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	s := &Store{DB: db, Dir: c.DataDir, WorkspaceID: w, Profile: c.Profile}
	ok := false
	defer func() {
		if !ok {
			db.Close()
		}
	}()
	if e = db.QueryRowContext(ctx, "SELECT sqlite_version()").Scan(&s.SQLiteVersion); e != nil {
		return nil, databaseFailure("probe_sqlite", e)
	}
	parts := strings.Split(s.SQLiteVersion, ".")
	if len(parts) != 3 {
		return nil, fmt.Errorf("SQLITE_VERSION_UNVERIFIED")
	}
	major, _ := strconv.Atoi(parts[0])
	minor, _ := strconv.Atoi(parts[1])
	patch, _ := strconv.Atoi(parts[2])
	if major < 3 || (major == 3 && (minor < 51 || (minor == 51 && patch < 3))) {
		return nil, fmt.Errorf("SQLITE_WAL_PATCH_REQUIRED")
	}
	if _, e = db.ExecContext(ctx, "PRAGMA journal_mode=WAL"); e != nil {
		return nil, databaseFailure("enable_wal", e)
	}
	if w == "" {
		if e = s.bootstrap(ctx, b, !m.schemaReady); e != nil {
			return nil, e
		}
	}
	ok = true
	return s, nil
}
func (s *Store) Tx(ctx context.Context, fn func(*sql.Tx) error) (err error) {
	tx, e := s.DB.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer func() {
		_ = tx.Rollback()
		if p := recover(); p != nil {
			panic(p)
		}
	}()
	if e = fn(tx); e != nil {
		return e
	}
	if s.CommitHook != nil {
		e = s.CommitHook(tx)
	} else {
		e = tx.Commit()
	}
	if e != nil {
		return &CommitError{e}
	}
	return nil
}
func (s *Store) bootstrap(ctx context.Context, b Bootstrap, createSchema bool) error {
	type seedUser struct {
		email, name, role, password string
		hash, salt                  []byte
	}
	us := []seedUser{}
	demo := s.Profile == "desktop" && !b.InitAdmin
	if demo {
		us = []seedUser{{email: "admin@flowdesk.demo", name: "演示管理员", role: "admin", password: "FlowDeskDemo!2026"}, {email: "agent@flowdesk.demo", name: "演示处理员", role: "agent", password: "FlowDeskDemo!2026"}, {email: "customer@flowdesk.demo", name: "演示申请人", role: "requester", password: "FlowDeskDemo!2026"}}
	} else {
		email, e := auth.Email(b.Email)
		if e != nil || !auth.PasswordValid(b.Password) || !utf8.ValidString(b.DisplayName) || utf8.RuneCountInString(b.DisplayName) > 100 || strings.ContainsRune(b.DisplayName, 0) || strings.TrimSpace(b.DisplayName) == "" {
			return domain.E("VALIDATION_ERROR")
		}
		us = []seedUser{{email: email, name: b.DisplayName, role: "admin", password: b.Password}}
	}
	for i := range us {
		var e error
		us[i].hash, us[i].salt, e = auth.Hash(us[i].password)
		if e != nil {
			return e
		}
	}
	w := auth.RandomHex(16)
	now := Now()
	cfg := domain.AIConfig{Mode: "demo", Strategy: "workflow", TimeoutSeconds: 60, OutputTokenLimit: 2048, Version: 1}
	e := s.Tx(ctx, func(tx *sql.Tx) error {
		if createSchema {
			schema, _ := schemaFS.ReadFile("schema.sql")
			if _, e := tx.ExecContext(ctx, string(schema)); e != nil {
				return databaseFailure("initialize_schema", e)
			}
			if _, e := tx.ExecContext(ctx, "INSERT INTO migrations VALUES(1,?,?)", checksum(), now); e != nil {
				return databaseFailure("initialize_schema", e)
			}
		}
		for _, u := range us {
			if _, e := tx.ExecContext(ctx, "INSERT INTO users(email,display_name,role,password_hash,password_salt,password_iterations,created_at) VALUES(?,?,?,?,?,?,?)", u.email, u.name, u.role, u.hash, u.salt, auth.Iterations, now); e != nil {
				return e
			}
		}
		if _, e := tx.ExecContext(ctx, "INSERT INTO ai_settings VALUES(1,?,NULL,1)", JSON(cfg)); e != nil {
			return e
		}
		if demo {
			articles := [][3]string{{"API 返回 401 的排查步骤", "401 表示请求未通过身份验证。检查 Authorization: Bearer 中是否放入有效 API key；确认使用的账号、项目与环境一致。密钥可能已经被撤销。不要将密钥公开粘贴到工单。", "API,认证,401"}, {"请求频率与 429", "429 表示请求频率受到限制。降低并发和频率，并按 Retry-After 提示等待。若持续发生，请提供发生时间、请求 ID 和频率，不要提供密钥。", "API,429,限流"}, {"账单核对", "可在账户账单页面核对用量明细和付款记录。如金额存在疑问，请提供账期与订单编号。退款资格需要工作人员根据服务条款审核，不能承诺自动退款。", "账单,费用,退款"}, {"登录与密码", "先确认登录邮箱是否正确，再使用产品提供的改密或找回流程。工作人员不会索取账号密码或验证码。", "登录,密码,账号"}, {"接口连接超时", "检查网络连接、接口地址和客户端超时配置。记录失败时间与请求 ID，先确认请求是否已在服务端执行，避免重复执行可能产生费用的请求。", "超时,网络,API"}}
			for _, a := range articles {
				if _, e := tx.ExecContext(ctx, "INSERT INTO knowledge_articles(title,body,tags_json,created_at,updated_at) VALUES(?,?,?,?,?)", a[0], a[1], JSON(strings.Split(a[2], ",")), now, now); e != nil {
					return e
				}
			}
			tickets := [][4]string{{"调用 API 一直返回 401", "今天切换到新的项目后，我的 API 请求返回 401。接口地址已核对，希望帮忙检查认证步骤。", "technical", "high"}, {"本月账单金额有疑问", "我想核对本月的 API 账单和用量，账期为本月，还没有提供订单编号。", "billing", "normal"}, {"高并发时遇到 429", "在提高并发之后请求常常返回 429，请告诉我如何调整请求频率。", "technical", "normal"}, {"无法登录账号", "我更换了设备后无法登录，希望了解如何检查邮箱和密码。", "account", "normal"}}
			for _, t := range tickets {
				if _, e := tx.ExecContext(ctx, "INSERT INTO tickets(title,description,requester_id,assignee_id,category,priority,status,created_at,updated_at) VALUES(?,?,3,2,?,?,'open',?,?)", t[0], t[1], t[2], t[3], now, now); e != nil {
					return e
				}
			}
		}
		_, e := tx.ExecContext(ctx, "INSERT INTO workspace_meta VALUES(1,?,?,?)", w, s.Profile, now)
		return e
	})
	if e == nil {
		s.WorkspaceID = w
	}
	return e
}
func (s *Store) RecoverJobs(ctx context.Context) error {
	return s.Tx(ctx, func(tx *sql.Tx) error {
		now := Now()
		if _, e := tx.ExecContext(ctx, "INSERT INTO audit_events(ticket_id,actor_id,action,details_json,created_at) SELECT ticket_id,actor_id,'job_interrupted',json_object('job_id',CAST(id AS TEXT),'error_code','RESTART_INTERRUPTED'),? FROM jobs WHERE status='running'", now); e != nil {
			return e
		}
		if _, e := tx.ExecContext(ctx, "UPDATE model_calls SET call_json=json_set(call_json,'$.status','interrupted_unknown') WHERE json_extract(call_json,'$.status')='dispatch_pending'"); e != nil {
			return e
		}
		if _, e := tx.ExecContext(ctx, "UPDATE jobs SET status='failed',error_code='RESTART_INTERRUPTED',error_stage='execution',finished_at=?,encrypted_secret=NULL WHERE status='running'", now); e != nil {
			return e
		}
		return ClearTerminatedActive(ctx, tx)
	})
}
func ClearTerminatedActive(ctx context.Context, q Executor) error {
	_, e := q.ExecContext(ctx, "UPDATE tickets SET active_job_id=NULL,revision=revision+1,updated_at=?,status=CASE WHEN status='resolved' THEN 'resolved' WHEN EXISTS(SELECT 1 FROM drafts d LEFT JOIN reviews r ON r.draft_id=d.id WHERE d.ticket_id=tickets.id AND d.content_version=tickets.content_version AND d.conversation_version=tickets.conversation_version AND d.cycle_no=tickets.cycle_no AND r.id IS NULL) THEN 'awaiting_review' ELSE 'in_progress' END WHERE active_job_id IN (SELECT id FROM jobs WHERE status NOT IN ('queued','running'))", Now())
	return e
}
func (s *Store) Probe(ctx context.Context) error {
	return s.Tx(ctx, func(tx *sql.Tx) error {
		_, e := tx.ExecContext(ctx, "UPDATE runtime_probe SET probe_seq=probe_seq+1 WHERE singleton_id=1")
		return e
	})
}
