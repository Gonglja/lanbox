// lanbox：文件与消息共享服务（桌面 / 小主机入口）。
package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"lanbox"
	"lanbox/internal/app"
	"lanbox/internal/updater"
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
	rollback := flag.Bool("rollback", false, "把上一次在线升级前的版本换回来并退出（服务起不来时的补救）")
	flag.Parse()

	if *showVersion {
		fmt.Printf("lanbox %s\n", version)
		return nil
	}
	if *rollback {
		return rollbackSelf()
	}

	logger := log.New(os.Stdout, "", log.LstdFlags)
	restartCh := make(chan struct{}, 1)

	a, err := app.New(app.Options{
		CfgPath:      *cfgPath,
		Web:          lanbox.WebFS(),
		Version:      version,
		EnableUpdate: true,
		Log:          logger,
		OnRestart: func() {
			select {
			case restartCh <- struct{}{}:
			default:
			}
		},
	})
	if err != nil {
		return err
	}

	if *resetAdmin {
		pw, err := a.ResetAdmin()
		if err != nil {
			return err
		}
		printPasswordBox("admin 密码已重置", pw)
		return nil
	}

	if pw := a.AdminPassword(); pw != "" {
		printPasswordBox("已创建管理员账号 admin", pw)
	}

	if err := a.Start(); err != nil {
		return err
	}
	printAccessURLs(a.Info(), logger)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	select {
	case err := <-a.ServeErr():
		_ = a.Shutdown()
		return err
	case sig := <-sigCh:
		logger.Printf("info: 收到信号 %v，开始优雅退出", sig)
		if err := a.Shutdown(); err != nil {
			return err
		}
		logger.Println("info: 已退出")
		return nil
	case <-restartCh:
		logger.Println("info: 收到网页重启请求，正在重新加载进程")
		time.Sleep(300 * time.Millisecond) // 让 202 响应先写回浏览器
		_ = a.Shutdown()
		if err := restartProcess(); err != nil {
			logger.Printf("error: 自重启失败: %v", err)
			return err
		}
		return nil
	}
}

func printPasswordBox(title, password string) {
	line := strings.Repeat("=", 46)
	fmt.Printf("\n%s\n  %s\n  用户名: admin\n  密  码: %s\n  请立即保存，此密码只显示这一次。\n  忘记可用 -reset-admin-password 重置。\n%s\n\n",
		line, title, password, line)
}

func printAccessURLs(info app.Info, logger *log.Logger) {
	portText := ""
	if info.Listen != "" {
		if _, p, err := net.SplitHostPort(info.Listen); err == nil && p != "" {
			portText = p
		}
	}
	if portText == "" {
		portText = "10725"
	}
	addrSuffix := ":" + portText
	if portText == "443" {
		addrSuffix = ""
	}
	if len(info.Addrs) > 0 {
		logger.Printf("info: 可用地址：")
		for _, url := range info.Addrs {
			logger.Printf("  %s", url)
		}
	}
	if info.MDNSEnabled {
		logger.Printf("  https://%s.local%s （mDNS）", info.MDNSHostname, addrSuffix)
	}
}

// rollbackSelf 把 lanbox.exe.old 换回来；不需要服务能启动，专治升级后起不来。
func rollbackSelf() error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("拿不到自身路径: %w", err)
	}
	failed, err := updater.Rollback(exe)
	if err != nil {
		return err
	}
	fmt.Printf("已回滚到上一版本（有问题的那个留在 %s）。请重新启动 lanbox。\n", failed)
	return nil
}
