// lanbox：文件与消息共享服务。
package main

import (
	"context"
	"crypto/tls"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"lanbox"
	"lanbox/internal/auth"
	"lanbox/internal/config"
	"lanbox/internal/httpapi"
	"lanbox/internal/store"
	"lanbox/internal/tlsutil"
)

// version 由构建时注入：-ldflags "-X main.version=v1.0.0"
var version = "dev"

func main() {
	if err := run(); err != nil {
		log.Fatalf("lanbox 启动失败: %v", err)
	}
}

func run() error {
	cfgPath := flag.String("config", "config.json", "配置文件路径（不存在则使用默认值）")
	resetAdmin := flag.Bool("reset-admin-password", false, "重置 admin 密码并退出")
	showVersion := flag.Bool("version", false, "打印版本号并退出")
	flag.Parse()

	if *showVersion {
		fmt.Printf("lanbox %s\n", version)
		return nil
	}

	logger := log.New(os.Stdout, "", log.LstdFlags)
	restartCh := make(chan struct{}, 1)
	mdnsRun := &mdnsRunner{log: logger}
	var (
		st *store.Store
		am *auth.Manager
	)

	// hooks 在 OpenLive 时绑定；闭包变量 st/am 在后续赋值后才被调用。
	live, err := config.OpenLive(*cfgPath, config.Hooks{
		Apply: func(prev, next config.Config) error {
			if st != nil {
				st.SetLimits(storeOpts(next))
			}
			if am != nil {
				am.SetSessionTTL(next.SessionTTL())
			}
			mdnsRun.apply(next)
			return nil
		},
		Restart: func() {
			select {
			case restartCh <- struct{}{}:
			default:
			}
		},
	})
	if err != nil {
		return err
	}
	cfg := live.Current()

	if err := os.MkdirAll(cfg.DataDir, 0o750); err != nil {
		return fmt.Errorf("创建数据目录 %s 失败: %w", cfg.DataDir, err)
	}

	am, err = auth.Open(filepath.Join(cfg.DataDir, "users.json"), cfg.SessionTTL())
	if err != nil {
		return err
	}

	if *resetAdmin {
		pw := auth.GeneratePassword()
		if err := am.SetPassword("admin", pw); err != nil {
			return err
		}
		printPasswordBox("admin 密码已重置", pw)
		return nil
	}

	pw, created, err := am.BootstrapAdmin()
	if err != nil {
		return err
	}
	if created {
		printPasswordBox("已创建管理员账号 admin", pw)
	}

	st, err = store.Open(filepath.Join(cfg.DataDir, "items"), storeOpts(cfg))
	if err != nil {
		return err
	}

	certPath, keyPath, generated, err := tlsutil.Ensure(cfg.DataDir, cfg.TLS.CertFile, cfg.TLS.KeyFile, tlsutil.Hosts(cfg.Listen))
	if err != nil {
		return err
	}
	if generated {
		logger.Printf("info: 已生成自签证书 %s（浏览器首次访问需手动信任）", certPath)
	}
	mdnsRun.certPath = certPath

	srv := httpapi.New(httpapi.Options{
		Cfg:        cfg,
		Store:      st,
		Auth:       am,
		Web:        lanbox.WebFS(),
		Log:        logger,
		Live:       live,
		MDNSStatus: func() httpapi.MDNSStatus { return mdnsRun.status(live.Current()) },
		Discover:   discoverServers(mdnsRun, live),
		StartedAt:  time.Now(),
		Version:    version,
	})

	mdnsRun.apply(cfg)
	defer mdnsRun.close()

	// 显式绑定，便于"改错 listen"时启动自愈
	ln, err := net.Listen("tcp", cfg.Listen)
	if err != nil && cfg.Origins["listen"] == "web" {
		logger.Printf("warn: 网页设置的 listen=%s 绑定失败（%v），回退到文件配置", cfg.Listen, err)
		if _, derr := live.DropWebOverride("listen"); derr != nil {
			logger.Printf("warn: 清除 listen 覆盖失败: %v", derr)
		} else {
			cfg = live.Current()
			srv.SetConfig(cfg)
		}
		ln, err = net.Listen("tcp", cfg.Listen)
	}
	if err != nil {
		return fmt.Errorf("监听 %s 失败: %w", cfg.Listen, err)
	}

	httpSrv := &http.Server{
		Handler:           srv,
		ReadHeaderTimeout: 20 * time.Second,
		IdleTimeout:       120 * time.Second,
		TLSConfig:         &tls.Config{MinVersion: tls.VersionTLS12},
	}

	printAccessURLs(cfg, logger)

	errCh := make(chan error, 1)
	go func() {
		if err := httpSrv.ServeTLS(ln, certPath, keyPath); err != nil && err != http.ErrServerClosed {
			errCh <- err
			return
		}
		errCh <- nil
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	shutdown := func() error {
		srv.Close() // 先断 SSE，避免 Shutdown 等待长连接
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return httpSrv.Shutdown(ctx)
	}

	select {
	case err := <-errCh:
		mdnsRun.close()
		return err
	case sig := <-sigCh:
		logger.Printf("info: 收到信号 %v，开始优雅退出", sig)
		if err := shutdown(); err != nil {
			return fmt.Errorf("关闭服务超时: %w", err)
		}
		logger.Println("info: 已退出")
		return nil
	case <-restartCh:
		logger.Println("info: 收到网页重启请求，正在重新加载进程")
		time.Sleep(300 * time.Millisecond) // 让 202 响应先写回浏览器
		mdnsRun.close()
		_ = shutdown()
		if err := restartProcess(); err != nil {
			logger.Printf("error: 自重启失败: %v", err)
			return err
		}
		return nil
	}
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

func printPasswordBox(title, password string) {
	line := strings.Repeat("=", 46)
	fmt.Printf("\n%s\n  %s\n  用户名: admin\n  密  码: %s\n  请立即保存，此密码只显示这一次。\n  忘记可用 -reset-admin-password 重置。\n%s\n\n",
		line, title, password, line)
}

func printAccessURLs(cfg config.Config, logger *log.Logger) {
	portText := fmt.Sprintf("%d", cfg.ListenPort())
	if _, p, err := net.SplitHostPort(cfg.Listen); err == nil && p != "" {
		portText = p
	}
	// 443 是 https 默认端口，打印时省略，避免出现 https://host:443。
	addrSuffix := ":" + portText
	if portText == "443" {
		addrSuffix = ""
	}
	ips, err := tlsutil.LocalIPs()
	if err != nil || len(ips) == 0 {
		logger.Printf("info: 监听 %s（HTTPS）", cfg.Listen)
	} else {
		logger.Printf("info: 监听 %s（HTTPS），可用地址：", cfg.Listen)
		printed := map[string]bool{}
		for _, ip := range ips {
			parsed := net.ParseIP(ip)
			if parsed == nil || parsed.IsLinkLocalUnicast() {
				continue
			}
			host := ip
			if strings.Contains(ip, ":") {
				host = "[" + ip + "]"
			}
			url := "https://" + host + addrSuffix
			if printed[url] {
				continue
			}
			printed[url] = true
			logger.Printf("  %s", url)
		}
	}
	if cfg.MDNS.Enabled {
		logger.Printf("  https://%s.local%s （mDNS）", cfg.MDNS.Hostname, addrSuffix)
	}
}
