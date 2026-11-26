package mobile

import (
	"strings"
	"testing"
)

// resetState 清掉包级单例，避免测试之间互相污染（生产代码不需要）。
func resetState(t *testing.T) {
	t.Helper()
	mu.Lock()
	cur = nil
	adminPassword = ""
	opts.dataDir, opts.listen, opts.hostname, opts.version = "", "", "", ""
	mu.Unlock()
	t.Cleanup(func() {
		mu.Lock()
		cur = nil
		adminPassword = ""
		mu.Unlock()
	})
}

func TestConfigureStartStop(t *testing.T) {
	resetState(t)
	Configure(t.TempDir(), "127.0.0.1:0", "lanbox-test", "test-version")
	if v := Version(); v != "test-version" {
		t.Fatalf("Version 不对: %q", v)
	}
	if err := Start(); err != nil {
		t.Fatalf("Start 失败: %v", err)
	}
	defer Stop()

	if !Running() {
		t.Fatal("Start 后 Running 应为 true")
	}
	addrs := Addrs()
	first := strings.Split(addrs, "\n")[0]
	if !strings.HasPrefix(first, "https://127.0.0.1:") {
		t.Fatalf("首行应是回环地址，得到 %q（全部：%q）", first, addrs)
	}
	if pin := CertPin(); len(pin) != 64 {
		t.Fatalf("CertPin 应是 64 位 hex，得到 %q", pin)
	}
	if tok := LoopbackToken(); len(tok) != 64 {
		t.Fatalf("LoopbackToken 应是 64 位 hex，得到 %q", tok)
	}
	if logs := Logs(); !strings.Contains(logs, "监听") {
		t.Fatalf("日志应包含启动信息:\n%s", logs)
	}
	if AdminPassword() == "" {
		t.Fatal("首次启动应给出 admin 初始密码")
	}

	Stop()
	if Running() {
		t.Fatal("Stop 后 Running 应为 false")
	}
}

func TestStartTwiceIsSafe(t *testing.T) {
	resetState(t)
	Configure(t.TempDir(), "127.0.0.1:0", "lanbox-test", "test")
	if err := Start(); err != nil {
		t.Fatal(err)
	}
	first := Addrs()
	if err := Start(); err != nil {
		t.Fatalf("重复 Start 不应报错: %v", err)
	}
	if got := Addrs(); got != first {
		t.Fatalf("重复 Start 不应换地址：%q -> %q", first, got)
	}
	Stop()
	Stop() // 重复 Stop 也不应 panic
}

func TestStartWithoutConfigure(t *testing.T) {
	resetState(t)
	if err := Start(); err == nil {
		Stop()
		t.Fatal("未 Configure 时应返回错误")
	}
}
