// Command bt-traffic-guard 监控 qBittorrent / Transmission 的上传流量，
// 并在自定义周期内达到阈值时自动限速。
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/btguard/bt-traffic-guard/internal/client"
	"github.com/btguard/bt-traffic-guard/internal/config"
	"github.com/btguard/bt-traffic-guard/internal/notify"
	"github.com/btguard/bt-traffic-guard/internal/policy"
	"github.com/btguard/bt-traffic-guard/internal/traffic"
	"github.com/btguard/bt-traffic-guard/internal/web"
)

// version 由构建时通过 -ldflags 注入。
var version = "dev"

// ---------------------------------------------------------------------------
// App：把各部分串起来的运行时容器
// ---------------------------------------------------------------------------

// App 持有全部运行时组件，并支持在不重启进程的情况下热重载配置。
type App struct {
	configPath string

	mu       sync.RWMutex
	cfg      *config.Config
	registry *client.Registry
	monitor  *traffic.Monitor
	engine   *policy.Engine
	notifier *notify.Sender
	loc      *time.Location

	log  *slog.Logger
	logf func(level, format string, args ...any)

	// cfgModTime 用于文件变更检测。
	cfgModTime time.Time
	// lastCfgErr 记录最近一次自动重载失败的原因，避免刷屏。
	lastCfgErr string
}

func main() {
	var (
		configPath = flag.String("config", envOr("BTG_CONFIG", "/config/config.yaml"), "配置文件路径")
		showVer    = flag.Bool("version", false, "打印版本号后退出")
	)
	flag.Parse()

	if *showVer {
		fmt.Println("bt-traffic-guard", version)
		return
	}

	logger := newLogger(envOr("BTG_LOG_LEVEL", "info"))

	app := &App{configPath: *configPath, log: logger}
	app.logf = func(level, format string, args ...any) {
		msg := fmt.Sprintf(format, args...)
		switch level {
		case "debug":
			logger.Debug(msg)
		case "info":
			logger.Info(msg)
		case "warn":
			logger.Warn(msg)
		case "error":
			logger.Error(msg)
		default:
			logger.Info(msg)
		}
	}

	if err := app.Bootstrap(); err != nil {
		logger.Error("启动失败", "err", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	app.Run(ctx)
	logger.Info("已停止")
}

// Bootstrap 加载配置、构造组件并写入初始配置（若不存在）。
func (a *App) Bootstrap() error {
	cfg, created, err := config.Load(a.configPath)
	if err != nil {
		return err
	}
	if created {
		if err := os.MkdirAll(filepath.Dir(a.configPath), 0o755); err != nil {
			return fmt.Errorf("创建配置目录失败: %w", err)
		}
		if err := config.Save(a.configPath, cfg); err != nil {
			return fmt.Errorf("写入初始配置失败: %w", err)
		}
		a.log.Info("已生成默认配置文件，请按需修改", "path", a.configPath)
	}
	a.applyConfig(cfg, true)

	if err := os.MkdirAll(cfg.DataDir, 0o755); err != nil {
		a.log.Warn("创建数据目录失败", "dir", cfg.DataDir, "err", err)
	}
	if fi, err := os.Stat(a.configPath); err == nil {
		a.cfgModTime = fi.ModTime()
	}
	return nil
}

// applyConfig 用新配置重建运行时组件。
// first 为 true 时表示首次启动。
func (a *App) applyConfig(cfg *config.Config, first bool) {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.cfg = cfg
	loc := cfg.Location()
	a.loc = loc

	// --- 重建客户端注册表 ---
	reg := client.NewRegistry()
	for _, cc := range cfg.Clients {
		if !cc.IsEnabled() {
			a.logf("info", "客户端 %s 已禁用，跳过", cc.Name)
			continue
		}
		c, err := client.New(client.Options{
			Name:               cc.Name,
			Type:               cc.Type,
			URL:                cc.URL,
			Username:           cc.Username,
			Password:           cc.Password,
			InsecureSkipVerify: cc.InsecureSkipVerify,
			Timeout:            cc.Timeout.Std(),
			UploadLimitIPv4:    cc.UploadLimitIPv4.Int64(),
			UploadLimitIPv6:    cc.UploadLimitIPv6.Int64(),
		})
		if err != nil {
			a.logf("error", "构造客户端 %s 失败: %v", cc.Name, err)
			continue
		}
		reg.Add(client.NewManaged(c))
		a.logf("info", "已注册客户端 %s (%s) → %s", cc.Name, cc.Type, cc.URL)
	}
	a.registry = reg

	// --- 监控器 ---
	prevMonitor := a.monitor
	a.monitor = traffic.NewMonitor(reg, cfg.PollInterval.Std(), cfg.HistoryPoints)
	if prevMonitor != nil && !first {
		a.monitor.AdoptStateFrom(prevMonitor)
	}

	// --- 通知 ---
	host, _ := os.Hostname()
	a.notifier = notify.New(cfg.Notify, host, a.logf)

	// --- 策略引擎 ---
	//
	// 热重载时保留旧的运行状态（周期进度、限速状态、事件记录），
	// 否则用户每改一次配置都会把累计流量清零、把限速解除。
	prevEngine := a.engine
	a.engine = policy.NewEngine(reg, a.monitor, a.notifier, a.logf, loc)
	if prevEngine != nil && !first {
		a.engine.AdoptStateFrom(prevEngine)
	}
	a.engine.Load(cfg.Policies)
	if first {
		a.logf("info", "BT Traffic Guard %s 启动，监听 %s，采样间隔 %s", version, cfg.Listen, cfg.PollInterval)
	}
}

// Run 启动所有后台循环并阻塞直到 ctx 取消。
func (a *App) Run(ctx context.Context) {
	cfg := a.Config()

	// 1. 采样循环
	monCtx, cancelMon := context.WithCancel(ctx)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		a.monitor.Run(monCtx)
	}()

	// 2. 策略求值循环（比采样稍慢，留出累计量更新时间）
	wg.Add(1)
	go func() {
		defer wg.Done()
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				a.engine.Evaluate(ctx)
			}
		}
	}()

	// 3. 配置文件热重载循环
	wg.Add(1)
	go func() {
		defer wg.Done()
		ticker := time.NewTicker(3 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				a.watchConfig()
			}
		}
	}()

	// 4. HTTP 服务
	srv := &http.Server{
		Addr:              cfg.Listen,
		Handler:           a.buildHandler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	go func() {
		a.logf("info", "Web UI 已启动: http://0.0.0.0%s", normalizeListen(cfg.Listen))
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			a.logf("error", "HTTP 服务异常: %v", err)
			cancelMon()
		}
	}()

	<-ctx.Done()
	a.logf("info", "收到退出信号，正在关闭…")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		a.logf("warn", "HTTP 服务关闭超时: %v", err)
	}
	cancelMon()
	wg.Wait()

	// 退出前尽力恢复正常限速，避免把用户的下载器留在限速状态。
	a.restoreAllOnExit()
}

// buildHandler 构造带认证的 HTTP handler。
func (a *App) buildHandler() http.Handler {
	host, _ := os.Hostname()
	server := web.NewServer(web.Deps{
		Registry:   a.registry,
		Monitor:    a.monitor,
		Engine:     a.engine,
		ConfigPath: a.configPath,
		Config:     a.Config,
		Reload:     a.Reload,
		UpdateConfig: func(cfg *config.Config) error {
			if err := config.Save(a.configPath, cfg); err != nil {
				return err
			}
			a.applyConfig(cfg, false)
			a.logf("info", "配置已通过 Web UI 更新并生效")
			return nil
		},
		Version:   version,
		StartedAt: time.Now(),
		Hostname:  host,
	})
	return server
}

// Config 返回当前生效的配置。
func (a *App) Config() *config.Config {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.cfg
}

// Reload 从磁盘重新读取配置并热重载。
func (a *App) Reload() (string, error) {
	cfg, _, err := config.Load(a.configPath)
	if err != nil {
		return "", err
	}
	a.applyConfig(cfg, false)
	return fmt.Sprintf("配置已重载，共 %d 个客户端、%d 条策略", len(cfg.Clients), len(cfg.Policies)), nil
}

// watchConfig 检测配置文件变更并自动重载。
func (a *App) watchConfig() {
	fi, err := os.Stat(a.configPath)
	if err != nil {
		return
	}
	a.mu.RLock()
	last := a.cfgModTime
	a.mu.RUnlock()

	if !fi.ModTime().After(last) {
		return
	}

	a.mu.Lock()
	a.cfgModTime = fi.ModTime()
	a.mu.Unlock()

	cfg, _, err := config.Load(a.configPath)
	if err != nil {
		a.mu.Lock()
		if a.lastCfgErr != err.Error() {
			a.lastCfgErr = err.Error()
			a.mu.Unlock()
			a.logf("error", "配置文件有误，保持原配置运行: %v", err)
			return
		}
		a.mu.Unlock()
		return
	}

	a.mu.Lock()
	a.lastCfgErr = ""
	a.mu.Unlock()

	a.applyConfig(cfg, false)
	a.logf("info", "检测到配置文件变更，已热重载（%d 个客户端，%d 条策略）", len(cfg.Clients), len(cfg.Policies))
}

// restoreAllOnExit 在进程退出时把处于限速状态的客户端恢复为不限速。
func (a *App) restoreAllOnExit() {
	a.mu.RLock()
	engine := a.engine
	reg := a.registry
	a.mu.RUnlock()
	if engine == nil || reg == nil {
		return
	}

	states := engine.States()
	limited := []string{}
	for _, s := range states {
		if s.Limited {
			limited = append(limited, s.Name)
		}
	}
	if len(limited) == 0 {
		return
	}

	a.logf("info", "检测到 %d 条策略处于限速状态，退出前尝试恢复", len(limited))
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	for _, name := range limited {
		if err := engine.ForceResume(ctx, name); err != nil {
			a.logf("warn", "恢复策略 %s 失败: %v", name, err)
		}
	}
}

// ---------------------------------------------------------------------------
// 助手
// ---------------------------------------------------------------------------

// newLogger 构造 slog 日志器。
func newLogger(level string) *slog.Logger {
	var lv slog.Level
	switch strings.ToLower(level) {
	case "debug":
		lv = slog.LevelDebug
	case "warn":
		lv = slog.LevelWarn
	case "error":
		lv = slog.LevelError
	default:
		lv = slog.LevelInfo
	}
	return slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: lv,
		ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
			if a.Key == slog.TimeKey {
				a.Value = slog.StringValue(time.Now().Format("2006-01-02 15:04:05"))
			}
			return a
		},
	}))
}

// envOr 返回环境变量值，为空时返回默认值。
func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// normalizeListen 把 ":8080" 转成 ":8080" 形式用于展示。
func normalizeListen(addr string) string {
	if strings.HasPrefix(addr, ":") {
		return addr
	}
	if i := strings.LastIndex(addr, ":"); i >= 0 {
		return addr[i:]
	}
	return ":" + addr
}
