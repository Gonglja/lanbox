// Package mobile 是 Android 端（gomobile bind）的唯一入口。
//
// 约束：
//   - 只用标准库，不 import golang.org/x/mobile（保持服务端零第三方依赖）；
//   - 导出函数只出现 string/bool/error，避免 gomobile 的结构体与回调绑定限制；
//   - 不处理信号与进程自重启，这些由 Kotlin 的前台 Service 负责。
package mobile

import (
	"errors"
	"strings"
	"sync"

	"lanbox"
	"lanbox/internal/app"
)

// 手机上的强制配置：端口与数据目录由 App 决定，网页改不动。
const (
	appDataSubdir  = "lanbox"
	minFreeDiskMB  = "512"
	maxFileSizeMB  = "512"
	defaultListen  = ":8443"
	defaultMDNS    = "lanbox"
)

var (
	mu   sync.Mutex
	opts struct {
		dataDir  string
		listen   string
		hostname string
		version  string
		lanIP    string
	}
	cur           *app.App
	adminPassword string
)

// Configure 设置启动参数；每次 Start 前调用（App 侧在 Service 启动时调用）。
// listen 形如 ":8443"；dataDir 是 App 私有目录；mdnsHostname 例如 "lanbox-a1b2"；
// lanIP 是 Kotlin 读到的 WiFi IPv4（Android 上 Go 无法自己枚举网卡），可为空。
func Configure(dataDir, listen, mdnsHostname, version, lanIP string) {
	mu.Lock()
	defer mu.Unlock()
	opts.dataDir = dataDir
	opts.listen = listen
	opts.hostname = mdnsHostname
	opts.version = version
	opts.lanIP = lanIP
	if strings.TrimSpace(opts.listen) == "" {
		opts.listen = defaultListen
	}
	if strings.TrimSpace(opts.hostname) == "" {
		opts.hostname = defaultMDNS
	}
}

// Start 启动服务；重复调用幂等。返回的错误可直接展示给用户。
func Start() error {
	mu.Lock()
	defer mu.Unlock()
	if opts.dataDir == "" {
		return errors.New("尚未 Configure 数据目录")
	}
	if cur != nil {
		return cur.Start()
	}
	dataDir := opts.dataDir + "/" + appDataSubdir
	a, err := app.New(app.Options{
		CfgPath: dataDir + "/config.json",
		Web:     lanbox.WebFS(),
		Version: opts.version,
		Log:     nil, // 只进环形缓冲，不写 stdout（Android 上没有终端）
		Locked: map[string]string{
			"listen":            opts.listen,
			"data_dir":          dataDir,
			"min_free_disk_mb":  minFreeDiskMB,
			"max_file_size_mb":  maxFileSizeMB,
			"mdns.hostname":     opts.hostname,
		},
		AllowLoopbackLogin: true,
		OnRestart:          nil, // 手机不支持进程自重启
		LanIPs:             lanIPs(),
	})
	if err != nil {
		return err
	}
	cur = a
	if pw := a.AdminPassword(); pw != "" {
		adminPassword = pw
	}
	return a.Start()
}

// lanIPs 把 Kotlin 传来的 IPv4 拆成切片（容忍逗号/换行分隔）。
func lanIPs() []string {
	var out []string
	for _, part := range strings.FieldsFunc(opts.lanIP, func(r rune) bool {
		return r == ',' || r == '\n' || r == ' ' || r == ';'
	}) {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}

// Stop 优雅关闭服务（保留实例，可再次 Start）。
func Stop() {
	mu.Lock()
	defer mu.Unlock()
	if cur == nil {
		return
	}
	_ = cur.Shutdown()
}

// Running 报告服务是否在运行。
func Running() bool {
	mu.Lock()
	defer mu.Unlock()
	return cur != nil && cur.Running()
}

// Addrs 返回可访问地址，每行一个，首行固定是 127.0.0.1。
func Addrs() string {
	mu.Lock()
	defer mu.Unlock()
	if cur == nil {
		return ""
	}
	info := cur.Info()
	return strings.Join(info.Addrs, "\n")
}

// CertPin 返回自签证书叶证书 DER 的 sha256（小写 hex），供 WebView 指纹固定。
func CertPin() string {
	mu.Lock()
	defer mu.Unlock()
	if cur == nil {
		return ""
	}
	return cur.Info().CertPin
}

// LoopbackToken 返回本次启动的回环引导令牌（WebView 用它免密登录）。
func LoopbackToken() string {
	mu.Lock()
	defer mu.Unlock()
	if cur == nil {
		return ""
	}
	return cur.Info().LoopbackToken
}

// Logs 返回最近 200 行日志。
func Logs() string {
	mu.Lock()
	defer mu.Unlock()
	if cur == nil {
		return ""
	}
	return cur.Logs()
}

// AdminPassword 返回首次启动时创建 admin 的明文密码；已存在账号时为空。
func AdminPassword() string {
	mu.Lock()
	defer mu.Unlock()
	return adminPassword
}

// Version 返回 App 传入的版本号。
func Version() string {
	mu.Lock()
	defer mu.Unlock()
	return opts.version
}
