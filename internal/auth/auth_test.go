package auth

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newManager(t *testing.T) *Manager {
	t.Helper()
	m, err := Open(filepath.Join(t.TempDir(), "users.json"), time.Hour)
	if err != nil {
		t.Fatalf("打开用户库失败: %v", err)
	}
	m.now = func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) }
	return m
}

func TestPasswordRoundtrip(t *testing.T) {
	salt, hash, iter, err := HashPassword("correct horse battery")
	if err != nil {
		t.Fatalf("哈希失败: %v", err)
	}
	if iter < 100_000 {
		t.Fatalf("迭代次数过低: %d", iter)
	}
	if !VerifyPassword("correct horse battery", salt, hash, iter) {
		t.Fatal("正确密码应校验通过")
	}
	if VerifyPassword("wrong", salt, hash, iter) {
		t.Fatal("错误密码不应通过")
	}
	salt2, hash2, _, _ := HashPassword("correct horse battery")
	if salt2 == salt || hash2 == hash {
		t.Fatal("相同密码两次哈希应因随机盐而不同")
	}
}

func TestGeneratePassword(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 50; i++ {
		p := GeneratePassword()
		if len(p) != 16 {
			t.Fatalf("密码长度应为 16，得到 %d (%q)", len(p), p)
		}
		if seen[p] {
			t.Fatal("生成了重复密码")
		}
		seen[p] = true
		for _, r := range p {
			if r == '0' || r == 'O' || r == 'l' || r == '1' || r == 'I' {
				t.Fatalf("密码不应含易混字符: %q", p)
			}
		}
	}
}

func TestBootstrapAdminOnce(t *testing.T) {
	m := newManager(t)
	pw, created, err := m.BootstrapAdmin()
	if err != nil || !created {
		t.Fatalf("首次应创建管理员: created=%v err=%v", created, err)
	}
	if len(pw) != 16 {
		t.Fatalf("应返回 16 位随机密码，得到 %q", pw)
	}
	if u, ok := m.Verify("admin", pw); !ok || !u.IsAdmin {
		t.Fatal("新管理员应能登录且是管理员")
	}
	pw2, created2, err := m.BootstrapAdmin()
	if err != nil {
		t.Fatal(err)
	}
	if created2 || pw2 != "" {
		t.Fatalf("已有用户时不应再创建: created=%v pw=%q", created2, pw2)
	}
}

func TestCreateUserAndPersist(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "users.json")
	m, err := Open(path, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	m.now = func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) }
	if _, _, err := m.BootstrapAdmin(); err != nil {
		t.Fatal(err)
	}
	if err := m.CreateUser("bob", "bobpassword", false); err != nil {
		t.Fatalf("创建用户失败: %v", err)
	}
	if err := m.CreateUser("bob", "anotherpass", false); !errors.Is(err, ErrUserExists) {
		t.Fatalf("重名应报 ErrUserExists，得到 %v", err)
	}
	if err := m.CreateUser("x", "短", false); !errors.Is(err, ErrWeakPassword) {
		t.Fatalf("过短密码应报 ErrWeakPassword，得到 %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("用户库未落盘: %v", err)
	}
	if len(raw) == 0 || !strings.Contains(string(raw), "bob") {
		t.Fatalf("落盘内容不对: %s", raw)
	}
	if strings.Contains(string(raw), "bobpassword") {
		t.Fatal("用户库不应包含明文密码")
	}

	reopened, err := Open(path, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := reopened.Verify("bob", "bobpassword"); !ok {
		t.Fatal("重开后用户应仍在")
	}
	if len(reopened.List()) != 2 {
		t.Fatalf("应有 2 个用户，得到 %d", len(reopened.List()))
	}
}

func TestDeleteUserGuards(t *testing.T) {
	m := newManager(t)
	if _, _, err := m.BootstrapAdmin(); err != nil {
		t.Fatal(err)
	}
	if err := m.CreateUser("bob", "bobpassword", false); err != nil {
		t.Fatal(err)
	}
	if err := m.DeleteUser("admin", "admin"); !errors.Is(err, ErrSelfDelete) {
		t.Fatalf("不能删除自己，得到 %v", err)
	}
	if err := m.DeleteUser("bob", "admin"); !errors.Is(err, ErrLastAdmin) {
		t.Fatalf("不能删除最后一个管理员，得到 %v", err)
	}
	if err := m.DeleteUser("admin", "bob"); err != nil {
		t.Fatalf("删除普通用户应成功: %v", err)
	}
	if err := m.DeleteUser("admin", "nobody"); !errors.Is(err, ErrUserNotFound) {
		t.Fatalf("删除不存在用户应报 ErrUserNotFound，得到 %v", err)
	}
}

func TestSessionsSlidingExpiry(t *testing.T) {
	m := newManager(t)
	if err := m.CreateUser("admin", "adminpass", true); err != nil {
		t.Fatal(err)
	}
	token, expires, err := m.NewSession("admin")
	if err != nil {
		t.Fatalf("创建会话失败: %v", err)
	}
	if expires.Sub(m.now()) != time.Hour {
		t.Fatalf("过期时间应为 now+ttl，得到 %v", expires.Sub(m.now()))
	}
	u, ok := m.Lookup(token)
	if !ok || u.Username != "admin" {
		t.Fatal("会话应有效")
	}
	if u.Salt != "" || u.Hash != "" {
		t.Fatal("对外返回的用户不应带哈希材料")
	}

	// 推进 40 分钟后仍有效，且过期时间被刷新
	m.mu.Lock()
	m.now = func() time.Time { return time.Date(2026, 1, 1, 0, 40, 0, 0, time.UTC) }
	m.mu.Unlock()
	if _, ok := m.Lookup(token); !ok {
		t.Fatal("40 分钟后会话应仍有效")
	}
	m.mu.RLock()
	exp := m.sessions[token].Expires
	m.mu.RUnlock()
	if exp.Sub(m.now()) != time.Hour {
		t.Fatalf("滑动过期未刷新: %v", exp.Sub(m.now()))
	}

	// 再推进 2 小时：过期
	m.mu.Lock()
	m.now = func() time.Time { return time.Date(2026, 1, 1, 2, 41, 0, 0, time.UTC) }
	m.mu.Unlock()
	if _, ok := m.Lookup(token); ok {
		t.Fatal("超过 ttl 应失效")
	}

	m.Drop(token)
}

func TestSetPasswordAndVerify(t *testing.T) {
	m := newManager(t)
	if err := m.CreateUser("admin", "adminpass", true); err != nil {
		t.Fatal(err)
	}
	token, _, err := m.NewSession("admin")
	if err != nil {
		t.Fatal(err)
	}
	if err := m.SetPassword("admin", "newpassword"); err != nil {
		t.Fatalf("改密失败: %v", err)
	}
	if _, ok := m.Verify("admin", "newpassword"); !ok {
		t.Fatal("新密码应能登录")
	}
	if _, ok := m.Lookup(token); ok {
		t.Fatal("改密后旧会话应失效")
	}
	if err := m.SetPassword("admin", "123"); !errors.Is(err, ErrWeakPassword) {
		t.Fatalf("弱密码应被拒绝，得到 %v", err)
	}
	if err := m.SetPassword("ghost", "newpassword"); !errors.Is(err, ErrUserNotFound) {
		t.Fatalf("不存在用户应报 ErrUserNotFound，得到 %v", err)
	}
}
