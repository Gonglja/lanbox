package main

import (
	"bytes"
	"os"
	"path/filepath"
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
	if st.Mode().Perm() != 0o600 {
		t.Fatalf("会话文件权限应为 0600，得到 %o", st.Mode().Perm())
	}
	if dirSt, err := os.Stat(filepath.Dir(path)); err != nil || dirSt.Mode().Perm() != 0o700 {
		t.Fatalf("配置目录权限应为 0700: %v", err)
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
