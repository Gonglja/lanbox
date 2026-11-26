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
	cfg, err := config.Load(*cfgPath)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(cfg.DataDir, 0o750); err != nil {
		return fmt.Errorf("创建数据目录 %s 失败: %w", cfg.DataDir, err)
	}

	am, err := auth.Open(filepath.Join(cfg.DataDir, "users.json"), cfg.SessionTTL())
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

	st, err := store.Open(filepath.Join(cfg.DataDir, "items"), store.Options{
		MaxFileBytes: cfg.MaxFileBytes(),
		MinFreeBytes: cfg.MinFreeBytes(),
		MaxTextBytes: cfg.MaxTextBytes,
	})
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

	srv := httpapi.New(httpapi.Options{Cfg: cfg, Store: st, Auth: am, Web: lanbox.WebFS(), Log: logger})
	httpSrv := &http.Server{
		Addr:              cfg.Listen,
		Handler:           srv,
		ReadHeaderTimeout: 20 * time.Second,
		IdleTimeout:       120 * time.Second,
		TLSConfig:         &tls.Config{MinVersion: tls.VersionTLS12},
	}

	printAccessURLs(cfg.Listen, logger)

	errCh := make(chan error, 1)
	go func() {
		if err := httpSrv.ListenAndServeTLS(certPath, keyPath); err != nil && err != http.ErrServerClosed {
			errCh <- err
			return
		}
		errCh <- nil
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	select {
	case err := <-errCh:
		return err
	case sig := <-sigCh:
		logger.Printf("info: 收到信号 %v，开始优雅退出", sig)
	}

	srv.Close() // 先断 SSE，避免 Shutdown 等待长连接
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := httpSrv.Shutdown(ctx); err != nil {
		return fmt.Errorf("关闭服务超时: %w", err)
	}
	logger.Println("info: 已退出")
	return nil
}

func printPasswordBox(title, password string) {
	line := strings.Repeat("=", 46)
	fmt.Printf("\n%s\n  %s\n  用户名: admin\n  密  码: %s\n  请立即保存，此密码只显示这一次。\n  忘记可用 -reset-admin-password 重置。\n%s\n\n",
		line, title, password, line)
}

func printAccessURLs(listen string, logger *log.Logger) {
	_, port, err := net.SplitHostPort(listen)
	if err != nil {
		port = "8443"
	}
	ips, err := tlsutil.LocalIPs()
	if err != nil || len(ips) == 0 {
		logger.Printf("info: 监听 %s（HTTPS）", listen)
		return
	}
	logger.Printf("info: 监听 %s（HTTPS），可用地址：", listen)
	printed := map[string]bool{}
	for _, ip := range ips {
		parsed := net.ParseIP(ip)
		if parsed == nil {
			continue
		}
		// 链路本地地址（fe80::/169.254）在同一网段外不可用，略过
		if parsed.IsLinkLocalUnicast() {
			continue
		}
		host := ip
		if strings.Contains(ip, ":") {
			host = "[" + ip + "]"
		}
		url := "https://" + host + ":" + port
		if printed[url] {
			continue
		}
		printed[url] = true
		logger.Printf("  %s", url)
	}
}
