// Package app 承担 lanbox 服务端的装配与生命周期，供桌面进程（cmd/lanbox）与
// Android（mobile，经 gomobile 绑定）共用。它不认识命令行参数、信号与进程自重启，
// 这些留在各宿主的 main 里。
package app

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"lanbox/internal/auth"
	"lanbox/internal/config"
	"lanbox/internal/httpapi"
	"lanbox/internal/store"
	"lanbox/internal/tlsutil"
	"lanbox/internal/updater"
)

// 错误。
var (
	// ErrNoWeb 表示没有提供内嵌前端资源。
	ErrNoWeb = errors.New("app: 未提供 Web 资源")
)

// Options 是装配 App 所需的输入。
type Options struct {
	CfgPath            string            // config.json 路径（可以不存在）
	Web                fs.FS             // 内嵌前端资源（lanbox.WebFS()）
	Version            string            // 版本号，只用于展示
	Log                *log.Logger       // 可为 nil：日志只进环形缓冲
	Locked             map[string]string // App 固定字段，如 listen/data_dir（桌面端留空）
	AllowLoopbackLogin bool              // 是否启用回环引导登录（只有 Android 打开）
	// LanIPs 是宿主额外告知的局域网地址。Android 上 SELinux 禁止 netlink，
	// Go 的 InterfaceAddrs() 只能看到回环，所以由 Kotlin 读 WiFi IPv4 传进来。
	LanIPs    []string
	OnRestart func() // nil 表示宿主不支持进程自重启（Android）
	// EnableUpdate 打开在线更新（桌面端与服务端传 true；Android 不传）。
	// 只有同时给了 OnRestart 才允许真正升级。
	EnableUpdate bool
}

// Info 是运行期对外信息快照。
type Info struct {
	Listen        string
	Addrs         []string // https://host:port，127.0.0.1 排最前
	CertPath      string
	CertPin       string // 叶证书 DER 的 sha256 hex
	MDNSEnabled   bool
	MDNSHostname  string
	LoopbackToken string
	Version       string
}

// App 是一个装配好、可 Start/Shutdown 的服务端实例。
type App struct {
	opts Options
	log  *log.Logger
	ring *Ring

	live *config.Live
	st   *store.Store
	am   *auth.Manager
	srv  *httpapi.Server
	// 以下字段在 Start 时创建
	httpSrv *http.Server
	ln      net.Listener
	mdns    *MDNSRunner

	certPath          string
	keyPath           string
	startedAt         time.Time
	loopbackToken     string
	bootstrapPassword string

	mu       sync.Mutex
	running  bool
	serveErr chan error

	// 在线更新：update 为 nil 表示不支持。
	update       *updater.Manager
	updateMu     sync.Mutex
	updateCancel context.CancelFunc

	// infoCache 给 Info() 加 3 秒缓存：Android 上每 1.5 秒刷新界面会反复枚举网卡，
	// 触发 SELinux 的 netlink 拒绝日志（不影响功能，但会刷屏）。
	infoMu    sync.Mutex
	infoCache Info
	infoAt    time.Time
}

// New 载入配置并装配用户库、存储、证书与 HTTP 处理器，但不监听端口。
func New(opts Options) (*App, error) {
	if opts.Web == nil {
		return nil, ErrNoWeb
	}
	if opts.Log == nil {
		opts.Log = log.New(io.Discard, "", log.LstdFlags)
	}
	a := &App{opts: opts, ring: NewRing(200), startedAt: time.Now(), serveErr: make(chan error, 1)}
	a.log = log.New(io.MultiWriter(opts.Log.Writer(), a.ring), "", log.LstdFlags)
	if opts.AllowLoopbackLogin {
		secret := make([]byte, 32)
		if _, err := rand.Read(secret); err != nil {
			return nil, fmt.Errorf("生成引导令牌失败: %w", err)
		}
		a.loopbackToken = hex.EncodeToString(secret)
	}

	live, err := config.OpenLive(opts.CfgPath, config.Hooks{
		Apply: func(prev, next config.Config) error {
			if a.st != nil {
				a.st.SetLimits(storeOpts(next))
			}
			if a.am != nil {
				a.am.SetSessionTTL(next.SessionTTL())
			}
			if a.mdns != nil {
				a.mdns.Apply(next)
			}
			return nil
		},
		Restart: func() {
			if opts.OnRestart != nil {
				opts.OnRestart()
			}
		},
	})
	if err != nil {
		return nil, err
	}

	// 更新源的 URL / 令牌每次请求时从当前配置读取，网页改完立即生效。
	if opts.EnableUpdate {
		a.update = buildUpdater(opts.Version, opts.OnRestart, a.log, func() config.Update {
			return live.Current().Update
		})
	}
	if err != nil {
		return nil, err
	}
	if len(opts.Locked) > 0 {
		live.Lock(opts.Locked)
	}
	a.live = live
	cfg := live.Current()

	if err := os.MkdirAll(cfg.DataDir, 0o750); err != nil {
		return nil, fmt.Errorf("创建数据目录 %s 失败: %w", cfg.DataDir, err)
	}

	a.am, err = auth.Open(filepath.Join(cfg.DataDir, "users.json"), cfg.SessionTTL())
	if err != nil {
		return nil, err
	}
	pw, created, err := a.am.BootstrapAdmin()
	if err != nil {
		return nil, err
	}
	if created {
		a.bootstrapPassword = pw
		a.log.Printf("info: 已创建管理员账号 admin（密码请从宿主界面查看）")
	}

	a.st, err = store.Open(filepath.Join(cfg.DataDir, "items"), storeOpts(cfg))
	if err != nil {
		return nil, err
	}

	hosts := tlsutil.Hosts(cfg.Listen)
	hosts = append(hosts, opts.LanIPs...)
	if cfg.MDNS.Hostname != "" {
		hosts = append(hosts, cfg.MDNS.Hostname+".local")
	}
	certPath, keyPath, generated, err := tlsutil.Ensure(cfg.DataDir, cfg.TLS.CertFile, cfg.TLS.KeyFile, hosts)
	if err != nil {
		return nil, err
	}
	a.certPath = certPath
	a.keyPath = keyPath
	if generated {
		a.log.Printf("info: 已生成自签证书 %s（浏览器首次访问需手动信任）", certPath)
	}

	a.mdns = &MDNSRunner{Log: a.log, CertPath: certPath}
	a.srv = httpapi.New(httpapi.Options{
		Cfg:                cfg,
		Store:              a.st,
		Auth:               a.am,
		Web:                opts.Web,
		Log:                a.log,
		Live:               live,
		MDNSStatus:         func() httpapi.MDNSStatus { return a.mdns.Status(live.Current()) },
		Discover:           DiscoverServers(a.mdns, live),
		StartedAt:          a.startedAt,
		Version:            opts.Version,
		AllowLoopbackLogin: opts.AllowLoopbackLogin,
		LoopbackToken:      a.loopbackToken,
		RestartDisabled:    opts.OnRestart == nil,
		IfaceAddrs:         a.localIPs,
		Update: func() httpapi.UpdateManager {
			if a.update == nil {
				return nil
			}
			return a.update
		},
	})
	return a, nil
}

// AdminPassword 返回本次启动时新创建 admin 的明文密码；已存在账号时为空。
func (a *App) AdminPassword() string { return a.bootstrapPassword }

// ResetAdmin 重置 admin 密码并返回新密码（-reset-admin-password 用）。
func (a *App) ResetAdmin() (string, error) {
	pw := auth.GeneratePassword()
	if err := a.am.SetPassword("admin", pw); err != nil {
		return "", err
	}
	return pw, nil
}

// Start 启动 mDNS 通告与 HTTPS 监听；重复调用是幂等的。
func (a *App) Start() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.running {
		return nil
	}

	cfg := a.live.Current()
	a.mdns.Apply(cfg)

	ln, err := net.Listen("tcp", cfg.Listen)
	if err != nil && cfg.Origins["listen"] == "web" && a.opts.Locked["listen"] == "" {
		// 网页设置的 listen 绑不上：回退到文件配置（桌面端启动自愈）。
		a.log.Printf("warn: 网页设置的 listen=%s 绑定失败（%v），回退到文件配置", cfg.Listen, err)
		if _, derr := a.live.DropWebOverride("listen"); derr != nil {
			a.log.Printf("warn: 清除 listen 覆盖失败: %v", derr)
		} else {
			cfg = a.live.Current()
			a.srv.SetConfig(cfg)
		}
		ln, err = net.Listen("tcp", cfg.Listen)
	}
	if err != nil {
		a.mdns.Close()
		return fmt.Errorf("监听 %s 失败: %w", cfg.Listen, err)
	}
	a.ln = ln

	a.httpSrv = &http.Server{
		Handler:           a.srv,
		ReadHeaderTimeout: 20 * time.Second,
		IdleTimeout:       120 * time.Second,
		TLSConfig:         &tls.Config{MinVersion: tls.VersionTLS12},
	}
	a.running = true
	go func() {
		err := a.httpSrv.ServeTLS(ln, a.certPath, a.keyPath)
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			a.mu.Lock()
			a.running = false
			a.mu.Unlock()
			a.log.Printf("error: HTTPS 服务异常退出: %v", err)
			select {
			case a.serveErr <- err:
			default:
			}
		}
	}()
	a.invalidateInfo()
	a.log.Printf("info: 监听 %s（HTTPS）", cfg.Listen)
	a.startUpdateChecks(cfg)
	return nil
}

// ServeErr 在监听意外失败时收到一个错误；正常关闭不会写入。
func (a *App) ServeErr() <-chan error { return a.serveErr }

// Shutdown 先断开 SSE，再优雅关闭监听；可重复调用。
func (a *App) Shutdown() error {
	a.mu.Lock()
	httpSrv, ln, mdnsRunner := a.httpSrv, a.ln, a.mdns
	running := a.running
	a.running = false
	a.httpSrv, a.ln = nil, nil
	a.mu.Unlock()

	a.stopUpdateChecks()
	if mdnsRunner != nil {
		mdnsRunner.Close()
	}
	a.invalidateInfo()
	if !running || httpSrv == nil {
		if ln != nil {
			_ = ln.Close()
		}
		return nil
	}
	a.srv.Close() // 先断 SSE，避免 Shutdown 等待长连接
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := httpSrv.Shutdown(ctx); err != nil {
		return fmt.Errorf("关闭服务超时: %w", err)
	}
	return nil
}

// invalidateInfo 丢弃 Info 缓存（监听状态变化后调用）。
func (a *App) invalidateInfo() {
	a.infoMu.Lock()
	a.infoAt = time.Time{}
	a.infoMu.Unlock()
}

// Running 报告服务是否在运行。
func (a *App) Running() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.running
}

// Logs 返回最近 200 行日志（环形缓冲）。
func (a *App) Logs() string { return a.ring.String() }

// Info 返回运行期信息快照（3 秒内复用上一次结果）。
func (a *App) Info() Info {
	a.infoMu.Lock()
	if !a.infoAt.IsZero() && time.Since(a.infoAt) < 3*time.Second {
		cached := a.infoCache
		a.infoMu.Unlock()
		return cached
	}
	a.infoMu.Unlock()

	info := a.buildInfo()

	a.infoMu.Lock()
	a.infoCache = info
	a.infoAt = time.Now()
	a.infoMu.Unlock()
	return info
}

func (a *App) buildInfo() Info {
	cfg := a.live.Current()
	port := cfg.ListenPort()
	a.mu.Lock()
	if a.ln != nil {
		if _, p, err := net.SplitHostPort(a.ln.Addr().String()); err == nil {
			if n, cerr := strconv.Atoi(p); cerr == nil {
				port = n
			}
		}
	}
	a.mu.Unlock()

	suffix := ""
	if port != 443 {
		suffix = fmt.Sprintf(":%d", port)
	}
	addrs := make([]string, 0, 8)
	seen := map[string]bool{}
	add := func(ip string) {
		host := ip
		if strings.Contains(host, ":") {
			host = "[" + host + "]"
		}
		url := "https://" + host + suffix
		if seen[url] {
			return
		}
		seen[url] = true
		addrs = append(addrs, url)
	}
	add("127.0.0.1")
	for _, ip := range a.opts.LanIPs {
		if ip != "" {
			add(ip)
		}
	}
	ips, err := tlsutil.LocalIPs()
	if err == nil {
		for _, ip := range ips {
			if ip == "127.0.0.1" || ip == "::1" {
				continue
			}
			if parsed := net.ParseIP(ip); parsed != nil && parsed.IsLinkLocalUnicast() {
				continue
			}
			add(ip)
		}
	}
	return Info{
		Listen:        cfg.Listen,
		Addrs:         addrs,
		CertPath:      a.certPath,
		CertPin:       certFingerprint(a.certPath),
		MDNSEnabled:   cfg.MDNS.Enabled,
		MDNSHostname:  cfg.MDNS.Hostname,
		LoopbackToken: a.loopbackToken,
		Version:       a.opts.Version,
	}
}

// localIPs 汇总宿主能提供的非回环 IP：优先用宿主传进来的（Android），
// 再补上网卡枚举结果（桌面端）。Android 上枚举会被 SELinux 拒绝，返回空。
func (a *App) localIPs() []string {
	seen := map[string]bool{}
	var out []string
	add := func(raw string) {
		raw = strings.TrimSpace(raw)
		ip := net.ParseIP(raw)
		if ip == nil || ip.IsLoopback() || ip.IsLinkLocalUnicast() || seen[raw] {
			return
		}
		seen[raw] = true
		out = append(out, raw)
	}
	for _, ip := range a.opts.LanIPs {
		add(ip)
	}
	if ips, err := tlsutil.LocalIPs(); err == nil {
		for _, ip := range ips {
			add(ip)
		}
	}
	return out
}

// storeOpts 把配置映射为 store 限额。
func storeOpts(cfg config.Config) store.Options {
	return store.Options{
		MaxFileBytes: cfg.MaxFileBytes(),
		MinFreeBytes: cfg.MinFreeBytes(),
		MaxTextBytes: cfg.MaxTextBytes,
		MaxFiles:     cfg.MaxFilesPerItem,
		MaxItemBytes: cfg.MaxItemBytes(),
	}
}

// buildUpdater 构造更新管理器；拿不到自身路径或参数不合法时返回 nil（功能关闭）。
func buildUpdater(version string, restart func(), logger *log.Logger, source func() config.Update) *updater.Manager {
	exe, err := os.Executable()
	if err != nil {
		logger.Printf("warn: 拿不到自身路径，在线更新已关闭: %v", err)
		return nil
	}
	up, err := updater.New(updater.Options{
		Current: version,
		Asset:   updater.AssetName("lanbox", runtime.GOOS, runtime.GOARCH),
		Source:  func() updater.Source { return updateSource(source()) },
		Log:     logger,
	})
	if err != nil {
		logger.Printf("warn: 初始化在线更新失败: %v", err)
		return nil
	}
	return updater.NewManager(updater.ManagerOptions{
		Updater: up,
		ExePath: exe,
		Restart: restart,
		Log:     logger,
	})
}

// updateSource 把配置里的 update.url 解释成 github 模式或清单模式。
func updateSource(c config.Update) updater.Source {
	src := updater.Source{Token: c.Token}
	u := strings.TrimSpace(c.URL)
	if strings.HasSuffix(strings.ToLower(u), ".json") {
		src.Manifest = u
	} else {
		src.BaseURL = u
	}
	return src
}

// startUpdateChecks 按配置启动后台检查（先停掉旧的）。
func (a *App) startUpdateChecks(cfg config.Config) {
	if a.update == nil {
		return
	}
	a.stopUpdateChecks()
	ctx, cancel := context.WithCancel(context.Background())
	a.updateMu.Lock()
	a.updateCancel = cancel
	a.updateMu.Unlock()
	a.update.Run(ctx, cfg.Update.Check)
}

// stopUpdateChecks 停止后台检查（可重复调用）。
func (a *App) stopUpdateChecks() {
	a.updateMu.Lock()
	cancel := a.updateCancel
	a.updateCancel = nil
	a.updateMu.Unlock()
	if cancel != nil {
		cancel()
	}
}
