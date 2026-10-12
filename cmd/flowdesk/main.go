package main

import (
	"bufio"
	"context"
	"database/sql"
	"errors"
	"flag"
	"flowdesk/internal/auth"
	"flowdesk/internal/buildinfo"
	"flowdesk/internal/httpapi"
	"flowdesk/internal/platform"
	"flowdesk/internal/repository"
	"flowdesk/internal/service"
	"flowdesk/internal/worker"
	webassets "flowdesk/web"
	"fmt"
	"golang.org/x/term"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"
)

func main() {
	if e := run(); e != nil {
		writeStartupError(os.Stderr, e)
		os.Exit(1)
	}
}

type startupFailure struct {
	err     error
	dataDir string
}

func (e *startupFailure) Error() string { return e.err.Error() }
func (e *startupFailure) Unwrap() error { return e.err }
func writeStartupError(w io.Writer, err error) {
	code := strings.Split(err.Error(), ":")[0]
	valid := strings.Contains(code, "_")
	for _, ch := range code {
		if !((ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') || ch == '_') {
			valid = false
			break
		}
	}
	if !valid {
		code = "STARTUP_FAILED"
	}
	fmt.Fprintln(w, code+": "+startupGuide(code))
	fmt.Fprintln(w, "程序版本：FlowDesk "+buildinfo.Version)
	var failure *startupFailure
	if errors.As(err, &failure) {
		fmt.Fprintln(w, "数据目录："+failure.dataDir)
		fmt.Fprintln(w, "数据库文件："+filepath.Join(failure.dataDir, "flowdesk.db"))
	}
	var databaseError *repository.DatabaseError
	if errors.As(err, &databaseError) {
		stages := map[string]string{"stat_database": "定位数据库文件", "open_database": "打开数据库", "inspect_schema": "只读检查数据库", "read_migrations": "检查结构版本", "foreign_database": "核对数据库归属与初始化残留", "read_workspace": "读取工作区信息", "inspect_initialization": "检查首次初始化进度", "incomplete_workspace_metadata": "核对工作区信息与已有业务数据", "probe_sqlite": "检查SQLite版本", "enable_wal": "启用数据库日志", "initialize_schema": "首次创建数据库结构"}
		stage := stages[databaseError.Operation]
		if stage == "" {
			stage = "检查数据库"
		}
		fmt.Fprintln(w, "失败步骤："+stage)
		var sqliteError interface{ Code() int }
		if errors.As(databaseError, &sqliteError) {
			fmt.Fprintf(w, "SQLite错误编号：%d\n", sqliteError.Code())
		}
	}
	fmt.Fprintln(w, "诊断：Windows运行“故障诊断.cmd”；命令行使用 --diagnose。处理步骤见 docs/OPERATIONS.md。")
}
func startupGuide(code string) string {
	switch code {
	case "INSTANCE_LOCK_FAILED":
		return "该数据目录已有FlowDesk运行。关闭原进程，或选择不同的数据目录。"
	case "PROFILE_MISMATCH":
		return "此数据目录属于另一运行模式。服务器必须使用独立目录并离线初始化管理员。"
	case "SCHEMA_TOO_NEW":
		return "数据由更新版本创建，请使用匹配的新版本程序，不要覆盖数据库。"
	case "SCHEMA_CHECKSUM_MISMATCH", "SCHEMA_INVALID":
		return "数据库结构或工作区信息无法核验。保留数据库、配套密钥和备份，先查看诊断中的失败步骤；不要删除文件或手工修改结构版本。"
	case "DATABASE_CORRUPT":
		return "文件不是有效SQLite数据库，或数据库已损坏。停止使用该目录，保留原文件，从完整配套备份恢复。"
	case "DATABASE_OPEN_FAILED", "DATABASE_PATH_INVALID":
		return "无法打开数据库文件。检查显示的路径是否正确、flowdesk.db是否为普通文件，以及当前账号是否有读写权限。"
	case "DATABASE_IO_FAILED":
		return "数据库读写失败。检查磁盘剩余空间、目录权限和磁盘状态；保留原文件，恢复条件后再启动。"
	case "DATABASE_BUSY":
		return "数据库正在被其他程序占用。关闭使用该目录的程序，等待写入结束后重试；不要删除数据库或WAL文件。"
	case "DATABASE_QUERY_FAILED":
		return "数据库检查未能完成。查看失败步骤和错误编号，先确认磁盘、权限和文件状态，再使用完整配套备份恢复。"
	case "SERVER_BOOTSTRAP_REQUIRED":
		return "先使用 --profile server --init-admin 在本机初始化管理员。"
	case "WORKSPACE_ALREADY_INITIALIZED":
		return "管理员已初始化；重置密码请使用 --reset-admin-password。"
	case "WORKSPACE_INITIALIZATION_REQUIRED":
		return "个人工作台尚未初始化。使用 --workspace 正常启动并设置管理员，或使用 --workspace --init-admin 离线初始化。"
	case "WORKSPACE_DEMO_ACCOUNTS":
		return "这个目录包含公开演示账号，不能作为个人工作台使用。保留原目录，使用个人工作台默认目录或一个新的 --data-dir。"
	case "WORKSPACE_NOT_FOUND":
		return "该目录没有已初始化的工作区。检查数据目录，不会创建一个空备份。"
	case "KEYSTORE_RECOVERY_INCOMPLETE":
		return "密钥恢复未完成。保留目录和恢复标记，恢复原配套文件后再启动；不要删除数据库。"
	case "SERVER_CONFIG_INVALID":
		return "检查运行模式、监听地址、HTTPS公开地址和模型出口白名单。"
	case "LISTEN_FAILED":
		return "端口被占用或地址无效。关闭占用程序，或显式选择另一个端口。"
	case "PASSWORD_INPUT_INVALID":
		return "两次密码必须完全一致，并满足12–128字符要求。未保存新的密码或改动已有账户。"
	case "VALIDATION_ERROR":
		return "检查管理员邮箱、1–100字符的显示名称及12–128字符的密码。未启动网络服务。"
	case "ADMIN_NOT_FOUND":
		return "输入已有管理员的邮箱。不会自动创建或修改其他账号。"
	default:
		return "无法完成启动。检查数据目录读写权限、可用磁盘和完整备份；保留原数据。"
	}
}
func diagnose(c platform.Config) error {
	fmt.Printf("FlowDesk %s 启动诊断（只读；不创建或修复数据）\n", buildinfo.Version)
	fmt.Fprintln(os.Stdout, "运行模式："+c.Profile)
	fmt.Fprintln(os.Stdout, "数据目录："+c.DataDir)
	w, exists, e := repository.Preflight(context.Background(), filepath.Join(c.DataDir, "flowdesk.db"), c.Profile)
	if e != nil {
		return e
	}
	switch {
	case !exists:
		fmt.Fprintln(os.Stdout, "数据库：尚未创建。正常首次启动会初始化。")
	case w == "":
		fmt.Fprintln(os.Stdout, "数据库：首次初始化尚未完成，已确认没有业务数据。正常启动会安全续建。")
	default:
		fmt.Fprintln(os.Stdout, "数据库：结构版本与当前程序匹配，工作区已初始化。")
	}
	info, e := os.Stat(filepath.Join(c.DataDir, "master.key"))
	switch {
	case os.IsNotExist(e):
		fmt.Fprintln(os.Stdout, "主密钥文件：缺失。未初始化时会创建；已有工作区应保留数据并按恢复说明处理。")
	case e != nil:
		fmt.Fprintln(os.Stdout, "主密钥文件：无法检查，请核对文件权限。")
	case !info.Mode().IsRegular() || info.Size() != 32:
		fmt.Fprintln(os.Stdout, "主密钥文件：文件类型或长度异常，先保留原文件。")
	default:
		fmt.Fprintln(os.Stdout, "主密钥文件：存在、长度正常；与已有加密设置是否配套需在运行时核验。")
	}
	return nil
}
func prompt(reader *bufio.Reader, label string, secret bool) (string, error) {
	fmt.Fprint(os.Stderr, label+": ")
	if secret && term.IsTerminal(int(os.Stdin.Fd())) {
		b, e := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Fprintln(os.Stderr)
		return string(b), e
	}
	if secret {
		fmt.Fprintln(os.Stderr, "当前输入来自管道或非交互终端；请输入者须保护标准输入内容。")
	}
	v, e := reader.ReadString('\n')
	if e == io.EOF && v != "" {
		e = nil
	}
	return strings.TrimSuffix(strings.TrimSuffix(v, "\n"), "\r"), e
}
func credentials(init bool) (repository.Bootstrap, error) {
	r := bufio.NewReader(os.Stdin)
	b := repository.Bootstrap{InitAdmin: init}
	var e error
	if b.Email, e = prompt(r, "管理员邮箱", false); e != nil {
		return b, e
	}
	if init {
		if b.DisplayName, e = prompt(r, "显示名称", false); e != nil {
			return b, e
		}
	}
	if b.Password, e = prompt(r, "新密码（12–128字符）", true); e != nil {
		return b, e
	}
	second, e := prompt(r, "再次输入新密码", true)
	if e != nil {
		return b, e
	}
	if b.Password != second || !auth.PasswordValid(b.Password) {
		return b, fmt.Errorf("PASSWORD_INPUT_INVALID")
	}
	return b, nil
}
func reset(ctx context.Context, s *repository.Store, b repository.Bootstrap) error {
	email, e := auth.Email(b.Email)
	if e != nil {
		return e
	}
	v, e := repository.Credentials(ctx, s.DB, email)
	if e != nil || v.User.Role != "admin" {
		return fmt.Errorf("ADMIN_NOT_FOUND")
	}
	hash, salt, e := auth.Hash(b.Password)
	if e != nil {
		return e
	}
	return s.Tx(ctx, func(tx *sql.Tx) error {
		if _, e := tx.ExecContext(ctx, "UPDATE users SET password_hash=?,password_salt=?,password_iterations=?,credentials_version=credentials_version+1,revision=revision+1 WHERE id=?", hash, salt, auth.Iterations, v.User.ID); e != nil {
			return e
		}
		if _, e = tx.ExecContext(ctx, "DELETE FROM sessions WHERE user_id=?", v.User.ID); e != nil {
			return e
		}
		return repository.Audit(ctx, tx, nil, nil, "admin_password_reset", map[string]any{"user_id": v.User.ID})
	})
}
func run() (runErr error) {
	var c platform.Config
	var init, resetPassword, recoverKey, backup, version, diagnostic bool
	var cidrs, egress, health string
	flag.StringVar(&c.Profile, "profile", "desktop", "desktop or server")
	flag.StringVar(&c.DataDir, "data-dir", os.Getenv("FLOWDESK_DATA_DIR"), "private local data directory")
	flag.StringVar(&c.Listen, "listen", "", "listen address")
	flag.StringVar(&c.PublicOrigin, "public-origin", os.Getenv("FLOWDESK_PUBLIC_ORIGIN"), "fixed server HTTPS origin")
	flag.StringVar(&cidrs, "trusted-proxy-cidrs", "", "explicit trusted reverse proxy CIDRs")
	flag.StringVar(&egress, "model-egress-hosts", "", "allowed server model hostname:port, comma separated")
	flag.BoolVar(&c.LiveEnabled, "live-enabled", false, "enable live models in server profile")
	flag.BoolVar(&c.NoBrowser, "no-browser", false, "skip automatic desktop browser launch")
	flag.BoolVar(&c.Workspace, "workspace", false, "personal desktop workspace without demo users or sample data")
	flag.BoolVar(&init, "init-admin", false, "offline administrator bootstrap via stdin")
	flag.BoolVar(&resetPassword, "reset-admin-password", false, "offline password reset via stdin")
	flag.BoolVar(&recoverKey, "recover-keystore", false, "offline recovery, clears saved model secrets")
	flag.BoolVar(&backup, "backup", false, "offline consistent database + available key + manifest backup")
	flag.StringVar(&health, "healthcheck", "", "process health probe URL")
	flag.BoolVar(&version, "version", false, "print software, source and build versions without opening data")
	flag.BoolVar(&diagnostic, "diagnose", false, "read-only startup diagnosis without creating or modifying data")
	flag.Parse()
	if version {
		fmt.Printf("FlowDesk %s; source=%s; built=%s; %s %s/%s\n", buildinfo.Version, buildinfo.SourceHash, buildinfo.BuiltAt, runtime.Version(), runtime.GOOS, runtime.GOARCH)
		return nil
	}
	if flag.NArg() > 0 {
		return fmt.Errorf("SERVER_CONFIG_INVALID: arguments")
	}
	count := 0
	for _, v := range []bool{init, resetPassword, recoverKey, backup, diagnostic} {
		if v {
			count++
		}
	}
	if count > 1 {
		return fmt.Errorf("SERVER_CONFIG_INVALID: offline command")
	}
	explicitListen := false
	flag.Visit(func(f *flag.Flag) {
		if f.Name == "listen" {
			explicitListen = true
		}
	})
	var e error
	c.TrustedProxies, e = platform.ParseCIDRs(cidrs)
	if e != nil {
		return fmt.Errorf("SERVER_CONFIG_INVALID")
	}
	if egress != "" {
		c.EgressHosts = strings.Split(egress, ",")
	}
	if e = c.Validate(count == 0 && health == ""); e != nil {
		return e
	}
	defer func() {
		if runErr != nil {
			runErr = &startupFailure{err: runErr, dataDir: c.DataDir}
		}
	}()
	if health != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		req, e := http.NewRequestWithContext(ctx, "GET", health, nil)
		if e != nil {
			return e
		}
		if c.PublicOrigin != "" {
			u, _ := url.Parse(c.PublicOrigin)
			req.Host = u.Host
		}
		client := &http.Client{Transport: &http.Transport{Proxy: nil}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		resp, e := client.Do(req)
		if e != nil {
			return e
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			return fmt.Errorf("HEALTHCHECK_FAILED")
		}
		return nil
	}
	if diagnostic {
		return diagnose(c)
	}
	if init && c.Profile != "server" && !c.Workspace {
		return fmt.Errorf("SERVER_CONFIG_INVALID: init-admin requires server or workspace")
	}
	if e = platform.PrivateDir(c.DataDir); e != nil {
		return e
	}
	lock, e := platform.AcquireLock(filepath.Join(c.DataDir, "instance.lock"))
	if e != nil {
		return e
	}
	defer lock.Close()
	ctx := context.Background()
	workspaceID, _, e := repository.Preflight(ctx, filepath.Join(c.DataDir, "flowdesk.db"), c.Profile)
	if e != nil {
		return e
	}
	if (backup || resetPassword || recoverKey) && workspaceID == "" {
		return fmt.Errorf("WORKSPACE_NOT_FOUND")
	}
	if init && workspaceID != "" {
		return fmt.Errorf("WORKSPACE_ALREADY_INITIALIZED")
	}
	var b repository.Bootstrap
	initializePersonal := c.Workspace && workspaceID == "" && count == 0
	if initializePersonal {
		fmt.Fprintln(os.Stdout, "首次建立个人工作台，请设置自己的管理员账号。不会创建演示账号或示例数据。")
	}
	if init || resetPassword || initializePersonal {
		b, e = credentials(init || initializePersonal)
		if e != nil {
			return e
		}
	}
	if workspaceID == "" {
		if c.Profile == "server" && !init {
			return fmt.Errorf("SERVER_BOOTSTRAP_REQUIRED")
		}
		if _, e = platform.LoadKey(c.DataDir, true); e != nil {
			return e
		}
	}
	s, e := repository.Open(ctx, c, b)
	if e != nil {
		return e
	}
	defer s.DB.Close()
	_, markerErr := os.Stat(filepath.Join(c.DataDir, "keystore-recovery.json"))
	if recoverKey || markerErr == nil {
		if e = s.RecoverKeystore(ctx, nil); e != nil {
			return e
		}
	}
	if recoverKey {
		fmt.Fprintln(os.Stdout, "密钥恢复完成；已保存的接口密钥已清除，工单与历史保留。")
		return nil
	}
	if backup {
		path, e := s.Backup(ctx, false)
		if e != nil {
			return e
		}
		fmt.Fprintln(os.Stdout, "备份完成："+path)
		return nil
	}
	if init {
		fmt.Fprintln(os.Stdout, "管理员初始化完成；未启动网络服务。")
		return nil
	}
	if resetPassword {
		if e = reset(ctx, s, b); e != nil {
			return e
		}
		fmt.Fprintln(os.Stdout, "管理员密码已重置；旧会话已撤销。")
		return nil
	}
	keys, keyErr := platform.LoadKey(c.DataDir, false)
	logger, logFile, e := platform.NewLog(c.DataDir)
	if e != nil {
		return e
	}
	defer logFile.Close()
	if keyErr != nil {
		logger.Warn("keystore_degraded", "code", "KEYSTORE_UNAVAILABLE")
	}
	if e = s.RecoverJobs(ctx); e != nil {
		return e
	}
	var listener net.Listener
	if c.Profile == "desktop" && !explicitListen {
		for port := 8787; port <= 8797; port++ {
			listener, e = net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
			if e == nil {
				break
			}
		}
	} else {
		listener, e = net.Listen("tcp", c.Listen)
	}
	if e != nil {
		return fmt.Errorf("LISTEN_FAILED")
	}
	defer listener.Close()
	if c.Profile == "desktop" {
		c.PublicOrigin = "http://" + listener.Addr().String()
	}
	svc := service.New(s, c, keys)
	manager := worker.New(svc)
	api := httpapi.New(svc, manager, webassets.FS(), logger)
	server := &http.Server{Handler: api, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 * 1024}
	manager.Start()
	errch := make(chan error, 1)
	go func() { errch <- server.Serve(listener) }()
	fmt.Fprintln(os.Stdout, "FlowDesk "+httpapi.Version+" — "+c.PublicOrigin)
	logger.Info("started", "version", httpapi.Version, "profile", c.Profile, "sqlite_version", s.SQLiteVersion)
	if c.Profile == "desktop" && !c.NoBrowser {
		openBrowser(c.PublicOrigin)
	}
	sigCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	select {
	case <-sigCtx.Done():
	case e = <-errch:
		if !errors.Is(e, http.ErrServerClosed) {
			svc.Stopping.Store(true)
			if !manager.Stop(8 * time.Second) {
				logger.Error("forced_exit", "code", "SHUTDOWN_INTERRUPTED")
				os.Exit(2)
			}
			return e
		}
	}
	svc.Stopping.Store(true)
	shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	done := make(chan bool, 1)
	go func() { done <- manager.Stop(9 * time.Second) }()
	_ = server.Shutdown(shutdown)
	select {
	case joined := <-done:
		if !joined {
			logger.Error("forced_exit", "code", "SHUTDOWN_INTERRUPTED")
			os.Exit(2)
		}
	case <-shutdown.Done():
		logger.Error("forced_exit", "code", "SHUTDOWN_INTERRUPTED")
		os.Exit(2)
	}
	logger.Info("stopped", "pending_results_lost", manager.PendingCount())
	return nil
}
func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	case "darwin":
		cmd = exec.Command("open", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	if e := cmd.Start(); e == nil {
		go func() { _ = cmd.Wait() }()
	}
}
