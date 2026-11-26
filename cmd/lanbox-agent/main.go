//go:build windows

// Command lanbox-agent 是 lanbox 的 Windows 桌面客户端：
// 托盘常驻、剪贴板自动上传、mDNS 发现与证书指纹固定。
package main

import (
	"context"
	"embed"
	"io/fs"
	"log"
	"os"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// version 由构建脚本通过 -X main.version 注入。
var version = "dev"

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	if !acquireSingleInstance() {
		// 已有一个实例在跑：静默退出，避免两份剪贴板监听重复上传。
		return
	}
	logger, closeLog := setupLog()
	defer closeLog()
	logger.Printf("lanbox-agent %s 启动", version)

	app := NewApp(version, logger)
	sub, err := fs.Sub(assets, "frontend/dist")
	if err != nil {
		logger.Printf("前端资源缺失: %v", err)
		os.Exit(1)
	}

	err = wails.Run(&options.App{
		Title: "lanbox",
		// 固定尺寸窗口：禁用缩放后系统只保留最小化与关闭按钮。
		Width:            456,
		Height:           720,
		DisableResize:    true,
		BackgroundColour: &options.RGBA{R: 250, G: 250, B: 250, A: 1},
		AssetServer:      &assetserver.Options{Assets: sub},
		OnStartup: func(ctx context.Context) {
			// 拖拽落点回调只能在这里注册。
			wailsruntime.OnFileDrop(ctx, func(x, y int, paths []string) {
				app.onFileDrop(x, y, paths)
			})
			app.startup(ctx)
			app.tray = startTray(app)
			logger.Printf("窗口与托盘已就绪")
		},
		OnBeforeClose: func(ctx context.Context) bool {
			return app.hideOnClose()
		},
		OnShutdown: app.shutdown,
		Bind:       []any{app},
		DragAndDrop: &options.DragAndDrop{
			EnableFileDrop:     true,
			DisableWebViewDrop: true,
		},
	})
	if err != nil {
		logger.Printf("Wails 退出: %v", err)
		log.Printf("lanbox-agent 退出: %v", err)
	}
}
