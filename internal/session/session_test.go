package session_test

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"lanbox/internal/client"
	"lanbox/internal/session"
)

func TestRoundTrip(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	want := &client.Session{URL: "https://192.168.1.9", Username: "admin", SID: "abc", PinFP: "deadbeef"}
	if err := session.Save(want); err != nil {
		t.Fatalf("保存会话失败: %v", err)
	}
	got, err := session.Load()
	if err != nil {
		t.Fatalf("读取会话失败: %v", err)
	}
	if got == nil || got.SID != "abc" || got.PinFP != "deadbeef" || got.URL != want.URL {
		t.Fatalf("会话内容不一致: %+v", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "lanbox", "session.json.tmp")); !os.IsNotExist(err) {
		t.Fatal("临时文件应已被 rename，不应残留")
	}
}

func TestLoadMissingIsNil(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	got, err := session.Load()
	if err != nil || got != nil {
		t.Fatalf("缺少会话文件应返回 (nil, nil)，得到 (%+v, %v)", got, err)
	}
}

func TestLoadRejectsIncomplete(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	path := filepath.Join(dir, "lanbox", "session.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"url":"https://x","sid":""}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := session.Load(); err == nil {
		t.Fatal("不完整的会话应报错")
	}
}

func TestSaveMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows 没有 Unix 权限位")
	}
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	if err := session.Save(&client.Session{URL: "https://x", SID: "s"}); err != nil {
		t.Fatal(err)
	}
	path, err := session.Path()
	if err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Fatalf("会话文件权限应为 0600，得到 %o", st.Mode().Perm())
	}
	dirSt, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if dirSt.Mode().Perm() != 0o700 {
		t.Fatalf("配置目录权限应为 0700，得到 %o", dirSt.Mode().Perm())
	}
}

func TestRemoveMissingIsNil(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if err := session.Remove(); err != nil {
		t.Fatalf("删除不存在的会话应成功: %v", err)
	}
}
