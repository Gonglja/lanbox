// Package httpapi 提供 lanbox 的 HTTP 接口与内嵌前端。
package httpapi

import (
	"encoding/json"
	"io/fs"
	"log"
	"net/http"
	"sync"
	"time"

	"lanbox/internal/auth"
	"lanbox/internal/config"
	"lanbox/internal/store"
)

// Options 是构建 Server 所需的依赖。
type Options struct {
	Cfg        config.Config
	Store      *store.Store
	Auth       *auth.Manager
	Web        fs.FS
	Log        *log.Logger
	Live       *config.Live
	MDNSStatus func() MDNSStatus
	Discover   DiscoverFunc
	StartedAt  time.Time
	Version    string

	// AllowLoopbackLogin 打开回环引导登录（只有 Android App 会设）。
	AllowLoopbackLogin bool
	LoopbackToken      string
	// RestartDisabled 为 true 时 /api/config/restart 返回 501（手机上没有进程重启）。
	RestartDisabled bool
}

// Server 是 lanbox 的 HTTP 处理入口。
type Server struct {
	cfgMu      sync.RWMutex
	cfg        config.Config
	store      *store.Store
	auth       *auth.Manager
	hub        *Hub
	web        fs.FS
	log        *log.Logger
	mux        *http.ServeMux
	live       *config.Live
	mdnsStatus func() MDNSStatus
	startedAt  time.Time
	version    string

	// discover 由 main 注入（内部用 internal/mdns）；结果缓存 discoverCacheTTL。
	discover      DiscoverFunc
	allowLoopback   bool
	loopbackToken   string
	restartDisabled  bool
	loopbackLimiter *auth.Limiter
	discoverMu    sync.Mutex
	discoverCache []DiscoveredServer
	discoverAt    time.Time
}

// New 组装路由与中间件。
func New(opts Options) *Server {
	if opts.Log == nil {
		opts.Log = log.Default()
	}
	if opts.StartedAt.IsZero() {
		opts.StartedAt = time.Now()
	}
	if opts.Version == "" {
		opts.Version = "dev"
	}
	s := &Server{
		cfg:        opts.Cfg,
		store:      opts.Store,
		auth:       opts.Auth,
		hub:        NewHub(),
		web:        opts.Web,
		log:        opts.Log,
		mux:        http.NewServeMux(),
		live:       opts.Live,
		mdnsStatus: opts.MDNSStatus,
		discover:   opts.Discover,
		allowLoopback:   opts.AllowLoopbackLogin,
		loopbackToken:   opts.LoopbackToken,
		restartDisabled:  opts.RestartDisabled,
		loopbackLimiter: auth.NewLimiter(time.Minute, 10, time.Minute),
		startedAt:  opts.StartedAt,
		version:    opts.Version,
	}
	s.routes()
	return s
}

// SetConfig 替换当前配置快照（热更后调用）。
func (s *Server) SetConfig(c config.Config) {
	s.cfgMu.Lock()
	s.cfg = c
	s.cfgMu.Unlock()
}

// current 返回当前配置快照。
func (s *Server) current() config.Config {
	s.cfgMu.RLock()
	defer s.cfgMu.RUnlock()
	return s.cfg
}

func (s *Server) routes() {
	s.mux.HandleFunc("GET /api/healthz", s.handleHealthz)
	s.mux.HandleFunc("GET /api/config", s.withAdmin(s.handleGetConfig))
	s.mux.HandleFunc("PUT /api/config", s.withAdmin(s.handlePutConfig))
	s.mux.HandleFunc("POST /api/config/restart", s.withAdmin(s.handleRestart))

	s.mux.HandleFunc("POST /api/login", s.handleLogin)
	s.mux.HandleFunc("POST /api/logout", s.withAuth(s.handleLogout))
	s.mux.HandleFunc("GET /api/me", s.withAuth(s.handleMe))
	s.mux.HandleFunc("GET /api/stats", s.withAuth(s.handleStats))
	s.mux.HandleFunc("POST /api/password", s.withAuth(s.handleChangePassword))

	// 连接信息与二维码（发给用户自己的手机/另一台电脑），以及局域网发现
	s.mux.HandleFunc("GET /api/connect", s.withAuth(s.handleConnect))
	s.mux.HandleFunc("GET /api/connect/qr.svg", s.withAuth(s.handleConnectQR))
	s.mux.HandleFunc("GET /api/discover", s.withAuth(s.handleDiscover))

	s.mux.HandleFunc("GET /api/items", s.withAuth(s.handleListItems))
	s.mux.HandleFunc("POST /api/items", s.withAuth(s.handleCreateItem))
	s.mux.HandleFunc("GET /api/items/{id}", s.withAuth(s.handleGetItem))
	s.mux.HandleFunc("PATCH /api/items/{id}", s.withAuth(s.handlePatchItem))
	s.mux.HandleFunc("DELETE /api/items/{id}", s.withAuth(s.handleDeleteItem))
	s.mux.HandleFunc("DELETE /api/items/{id}/files/{fid}", s.withAuth(s.handleDeleteFile))
	s.mux.HandleFunc("GET /api/items/{id}/files/{fid}", s.withAuth(s.handleDownload))
	s.mux.HandleFunc("GET /api/items/{id}/zip", s.withAuth(s.handleDownloadZip))
	s.mux.HandleFunc("GET /api/events", s.withAuth(s.handleEvents))

	s.mux.HandleFunc("GET /api/users", s.withAdmin(s.handleListUsers))
	s.mux.HandleFunc("POST /api/users", s.withAdmin(s.handleCreateUser))
	s.mux.HandleFunc("POST /api/users/{name}/password", s.withAdmin(s.handleSetUserPassword))
	s.mux.HandleFunc("DELETE /api/users/{name}", s.withAdmin(s.handleDeleteUser))

	s.mux.Handle("GET /", s.staticHandler())
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.chain(s.mux).ServeHTTP(w, r)
}

// Close 关闭所有 SSE 连接，供优雅退出调用。
func (s *Server) Close() { s.hub.Close() }

// staticHandler 提供内嵌前端资源。
func (s *Server) staticHandler() http.Handler {
	fileServer := http.FileServerFS(s.web)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache")
		// 首页直接读出 index.html，避免 FileServer 对 "/index.html" 做 "./" 重定向
		if r.URL.Path == "/" || r.URL.Path == "/index.html" {
			page, err := fs.ReadFile(s.web, "index.html")
			if err != nil {
				writeError(w, http.StatusInternalServerError, "internal", "前端资源缺失")
				return
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Write(page)
			return
		}
		fileServer.ServeHTTP(w, r)
	})
}

// broadcast 序列化并推送事件。
func (s *Server) broadcast(kind string, payload any) {
	if s.hub.Count() == 0 {
		return
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		s.log.Printf("warn: 事件序列化失败: %v", err)
		return
	}
	s.hub.Broadcast(Event{Type: kind, Data: string(raw)})
}
