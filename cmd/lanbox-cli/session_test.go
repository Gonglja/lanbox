package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"lanbox/internal/client"
)

func TestSessionRoundTripAndMode(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	want := &client.Session{URL: "https://192.168.1.9:8443", Username: "admin", SID: "abc"}
	if err := saveSession(want); err != nil {
		t.Fatalf("保存会话失败: %v", err)
	}
	path := filepath.Join(dir, "lanbox", "session.json")
	st, err := os.Stat(path)
	if err != nil {
		t.Fatalf("会话文件不存在: %v", err)
	}
	// Windows 没有 Unix 权限位（Go 报 0666），文件/目录收紧只在类 Unix 平台断言。
	if runtime.GOOS != "windows" {
		if st.Mode().Perm() != 0o600 {
			t.Fatalf("会话文件权限应为 0600，得到 %o", st.Mode().Perm())
		}
		if dirSt, err := os.Stat(filepath.Dir(path)); err != nil || dirSt.Mode().Perm() != 0o700 {
			t.Fatalf("配置目录权限应为 0700: %v", err)
		}
	}

	got, err := loadSession()
	if err != nil {
		t.Fatalf("读取会话失败: %v", err)
	}
	if got.URL != want.URL || got.Username != want.Username || got.SID != want.SID {
		t.Fatalf("会话内容不一致: %+v", got)
	}

	if err := removeSession(); err != nil {
		t.Fatalf("删除会话失败: %v", err)
	}
	if _, err := loadSession(); err == nil {
		t.Fatal("删除后应读不到会话")
	}
}

func TestRunWithoutCommandIsUsageError(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := run(nil, &out, &errOut); code != exitUsage {
		t.Fatalf("无参数应返回 exitUsage(%d)，得到 %d", exitUsage, code)
	}
	if !strings.Contains(errOut.String(), "用法") {
		t.Fatalf("应打印用法，得到 %q", errOut.String())
	}
}

func TestHTTPClientInsecureAndCA(t *testing.T) {
	var errBuf bytes.Buffer
	g := &globals{stderr: &errBuf, insecure: true}
	if _, err := httpClient(g); err != nil {
		t.Fatalf("insecure 客户端构造失败: %v", err)
	}
	if !strings.Contains(errBuf.String(), "警告") {
		t.Fatalf("应打印警告，得到 %q", errBuf.String())
	}

	bad := filepath.Join(t.TempDir(), "bad.pem")
	if err := os.WriteFile(bad, []byte("not a pem"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := httpClient(&globals{stderr: &errBuf, caFile: bad}); err == nil {
		t.Fatal("非法 PEM 应报错")
	}
}

// TestSaveSessionTightensExistingDir 断言已存在的 0755 配置目录会被收紧为 0700。
func TestSaveSessionTightensExistingDir(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows 没有 Unix 权限位，目录收紧不适用")
	}
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	cfg := filepath.Join(dir, "lanbox")
	if err := os.MkdirAll(cfg, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(cfg, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := saveSession(&client.Session{URL: "https://x:8443", Username: "u", SID: "s"}); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o700 {
		t.Fatalf("已存在的配置目录应被收紧为 0700，得到 %o", st.Mode().Perm())
	}
}

// TestSaveSessionIsAtomic 断言会话文件通过临时文件 rename 写入，不残留 .tmp。
func TestSaveSessionIsAtomic(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	if err := saveSession(&client.Session{URL: "https://x:8443", Username: "u", SID: "s1"}); err != nil {
		t.Fatal(err)
	}
	if err := saveSession(&client.Session{URL: "https://x:8443", Username: "u", SID: "s2"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "lanbox", "session.json.tmp")); !os.IsNotExist(err) {
		t.Fatal("临时文件应已被 rename，不应残留")
	}
	got, err := loadSession()
	if err != nil {
		t.Fatal(err)
	}
	if got.SID != "s2" {
		t.Fatalf("应读到新会话，得到 %q", got.SID)
	}
}

// TestLoadSessionMissingIsAuthError 断言缺少会话文件属于认证问题。
func TestLoadSessionMissingIsAuthError(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	_, err := loadSession()
	if !errors.Is(err, errAuth) {
		t.Fatalf("应返回 errAuth，得到 %v", err)
	}
}
