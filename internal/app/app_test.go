package app

import (
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"lanbox/internal/config"
)

// newTestApp 在临时目录里装配一个只监听回环的实例。
func newTestApp(t *testing.T, cfgJSON string) *App {
	t.Helper()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	if cfgJSON == "" {
		cfgJSON = `{"mdns":{"enabled":false}}`
	}
	if err := os.WriteFile(cfgPath, []byte(cfgJSON), 0o600); err != nil {
		t.Fatal(err)
	}
	web := fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("<html>ok</html>")}}
	a, err := New(Options{
		CfgPath: cfgPath,
		Web:     web,
		Version: "test",
		Log:     log.New(io.Discard, "", 0),
		Locked: map[string]string{
			"listen":   "127.0.0.1:0",
			"data_dir": filepath.Join(dir, "app-data"),
		},
		AllowLoopbackLogin: true,
	})
	if err != nil {
		t.Fatalf("New 失败: %v", err)
	}
	t.Cleanup(func() { _ = a.Shutdown() })
	return a
}

func TestStartStopReleasesPort(t *testing.T) {
	a := newTestApp(t, "")
	if err := a.Start(); err != nil {
		t.Fatalf("Start 失败: %v", err)
	}
	info := a.Info()
	if len(info.Addrs) == 0 || !strings.HasPrefix(info.Addrs[0], "https://127.0.0.1:") {
		t.Fatalf("Addrs 首行应是回环地址: %v", info.Addrs)
	}
	addr := strings.TrimPrefix(info.Addrs[0], "https://")
	if err := a.Shutdown(); err != nil {
		t.Fatalf("Shutdown 失败: %v", err)
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatalf("Shutdown 后端口仍被占用: %v", err)
	}
	_ = ln.Close()
	if a.Running() {
		t.Fatal("Shutdown 后 Running 应为 false")
	}
}

func TestStartTwiceIsIdempotent(t *testing.T) {
	a := newTestApp(t, "")
	if err := a.Start(); err != nil {
		t.Fatal(err)
	}
	first := a.Info().Addrs
	if err := a.Start(); err != nil {
		t.Fatalf("重复 Start 不应报错: %v", err)
	}
	if got := a.Info().Addrs; len(got) != len(first) || got[0] != first[0] {
		t.Fatalf("重复 Start 不应换端口: %v -> %v", first, got)
	}
	if err := a.Shutdown(); err != nil {
		t.Fatal(err)
	}
	if err := a.Shutdown(); err != nil {
		t.Fatalf("重复 Shutdown 不应报错: %v", err)
	}
}

func TestLockedFieldsWin(t *testing.T) {
	a := newTestApp(t, `{"listen":":9443","data_dir":"./x","mdns":{"enabled":false}}`)
	info := a.Info()
	if info.Listen != "127.0.0.1:0" {
		t.Fatalf("listen 应被 Lock 覆盖，得到 %q", info.Listen)
	}
	if err := a.Start(); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(a.Info().Addrs[0], "https://127.0.0.1:") {
		t.Fatalf("实际监听地址不对: %v", a.Info().Addrs)
	}
}

func TestCertPinAndLogs(t *testing.T) {
	a := newTestApp(t, "")
	if err := a.Start(); err != nil {
		t.Fatal(err)
	}
	info := a.Info()
	if len(info.CertPin) != 64 {
		t.Fatalf("CertPin 应是 64 位 hex: %q", info.CertPin)
	}
	if len(info.LoopbackToken) != 64 {
		t.Fatalf("引导令牌应是 64 位 hex: %q", info.LoopbackToken)
	}
	logs := a.Logs()
	if !strings.Contains(logs, "监听") {
		t.Fatalf("日志应包含启动信息:\n%s", logs)
	}
	if strings.Contains(logs, info.LoopbackToken) {
		t.Fatal("引导令牌不应出现在日志里")
	}
}

func TestAdminPasswordOnlyOnFirstRun(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	if err := os.WriteFile(cfgPath, []byte(`{"mdns":{"enabled":false}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	opts := Options{
		CfgPath: cfgPath,
		Web:     fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("ok")}},
		Log:     log.New(io.Discard, "", 0),
		Locked:  map[string]string{"data_dir": filepath.Join(dir, "data")},
	}
	a, err := New(opts)
	if err != nil {
		t.Fatal(err)
	}
	if a.AdminPassword() == "" {
		t.Fatal("首次装配应返回 admin 初始密码")
	}
	_ = a.Shutdown()

	b, err := New(opts)
	if err != nil {
		t.Fatal(err)
	}
	if got := b.AdminPassword(); got != "" {
		t.Fatalf("再次装配不应返回密码: %q", got)
	}
	_ = b.Shutdown()
}

func TestRingKeepsLastLines(t *testing.T) {
	r := NewRing(3)
	for _, line := range []string{"a\n", "b\n", "c\n", "d\n"} {
		if _, err := r.Write([]byte(line)); err != nil {
			t.Fatal(err)
		}
	}
	if got := r.String(); got != "b\nc\nd" {
		t.Fatalf("环形缓冲不对: %q", got)
	}
}

func TestUpdateSource(t *testing.T) {
	cases := []struct {
		url      string
		manifest string
		base     string
	}{
		{"", "", ""},
		{"https://github.com/Gonglja/lanbox", "", "https://github.com/Gonglja/lanbox"},
		{"https://example.com/up/latest.json", "https://example.com/up/latest.json", ""},
		{" https://example.com/LATEST.JSON ", "https://example.com/LATEST.JSON", ""},
	}
	for _, c := range cases {
		src := updateSource(config.Update{URL: c.url, Token: "tok"})
		if src.Manifest != c.manifest || src.BaseURL != c.base || src.Token != "tok" {
			t.Fatalf("updateSource(%q) = %+v", c.url, src)
		}
	}
}

func TestBuildUpdater(t *testing.T) {
	logger := log.New(io.Discard, "", 0)
	m := buildUpdater("1.2.3", func() {}, logger, func() config.Update { return config.Update{} })
	if m == nil {
		t.Fatal("正常情况下应该能构造更新管理器")
	}
	st := m.State()
	if !st.Supported || st.Current != "1.2.3" {
		t.Fatalf("状态不对: %+v", st)
	}
}
