package updater

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// tempExe 在临时目录里造一个「可执行文件 + 待替换文件」。
func tempExe(t *testing.T, oldBody, newBody string) (exe, next string) {
	t.Helper()
	dir := t.TempDir()
	exe = filepath.Join(dir, "lanbox")
	next = filepath.Join(dir, tempName("1.2.4"))
	if err := os.WriteFile(exe, []byte(oldBody), 0o755); err != nil {
		t.Fatalf("写旧程序: %v", err)
	}
	if err := os.WriteFile(next, []byte(newBody), 0o755); err != nil {
		t.Fatalf("写新程序: %v", err)
	}
	return exe, next
}

func mustRead(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取 %s: %v", path, err)
	}
	return string(b)
}

func TestStageAndRollback(t *testing.T) {
	exe, next := tempExe(t, "旧版本", "新版本")

	if HasBackup(exe) {
		t.Fatal("还没升级就不该有备份")
	}
	backup, err := Stage(exe, next)
	if err != nil {
		t.Fatalf("Stage: %v", err)
	}
	if backup != BackupPath(exe) {
		t.Fatalf("备份路径 = %q", backup)
	}
	if got := mustRead(t, exe); got != "新版本" {
		t.Fatalf("替换后内容 = %q", got)
	}
	if got := mustRead(t, backup); got != "旧版本" {
		t.Fatalf("备份内容 = %q", got)
	}
	if !HasBackup(exe) {
		t.Fatal("升级后应该有备份")
	}
	if runtime.GOOS != "windows" {
		if st, err := os.Stat(exe); err != nil || st.Mode().Perm()&0o100 == 0 {
			t.Fatalf("替换后可执行位丢失: %v %v", st.Mode(), err)
		}
	}

	failed, err := Rollback(exe)
	if err != nil {
		t.Fatalf("Rollback: %v", err)
	}
	if got := mustRead(t, exe); got != "旧版本" {
		t.Fatalf("回滚后内容 = %q", got)
	}
	if got := mustRead(t, failed); got != "新版本" {
		t.Fatalf(".failed 内容 = %q", got)
	}
	if HasBackup(exe) {
		t.Fatal("回滚后备份应该已被消费")
	}
}

func TestRollbackWithoutBackup(t *testing.T) {
	exe, _ := tempExe(t, "旧版本", "新版本")
	if _, err := Rollback(exe); !errors.Is(err, ErrNoBackup) {
		t.Fatalf("期望 ErrNoBackup，得到 %v", err)
	}
	if got := mustRead(t, exe); got != "旧版本" {
		t.Fatalf("没有备份时不该动原文件，得到 %q", got)
	}
}

func TestStageTwiceReplacesBackup(t *testing.T) {
	exe, next := tempExe(t, "v1", "v2")
	if _, err := Stage(exe, next); err != nil {
		t.Fatalf("第一次 Stage: %v", err)
	}
	next2 := filepath.Join(filepath.Dir(exe), tempName("1.3.0"))
	if err := os.WriteFile(next2, []byte("v3"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := Stage(exe, next2); err != nil {
		t.Fatalf("第二次 Stage: %v", err)
	}
	if got := mustRead(t, exe); got != "v3" {
		t.Fatalf("内容 = %q", got)
	}
	if got := mustRead(t, BackupPath(exe)); got != "v2" {
		t.Fatalf("备份应该只保留上一版，得到 %q", got)
	}
}

func TestStageRejectsSameFile(t *testing.T) {
	exe, _ := tempExe(t, "同一份", "x")
	if _, err := Stage(exe, exe); err == nil {
		t.Fatal("新文件就是自己时必须拒绝")
	}
	if got := mustRead(t, exe); got != "同一份" {
		t.Fatalf("拒绝后原文件不该变，得到 %q", got)
	}
}

func TestStageReadOnlyDir(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows 上 chmod 不能阻止写入，跳过只读目录用例")
	}
	if os.Geteuid() == 0 {
		t.Skip("root 不受只读目录限制")
	}
	exe, next := tempExe(t, "旧版本", "新版本")
	dir := filepath.Dir(exe)
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatalf("改权限: %v", err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0o700) })

	_, err := Stage(exe, next)
	if err == nil {
		t.Fatal("只读目录里应该失败")
	}
	var ue *Error
	if !errors.As(err, &ue) || ue.Stage != "permission" {
		t.Fatalf("期望 permission 阶段，得到 %v", err)
	}
	if got := mustRead(t, exe); got != "旧版本" {
		t.Fatalf("失败后原文件不该变，得到 %q", got)
	}
}

func TestCleanupTemps(t *testing.T) {
	dir := t.TempDir()
	stale := []string{
		filepath.Join(dir, tempName("1.2.4")),
		filepath.Join(dir, tempName("1.2.5")),
	}
	for _, p := range stale {
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	keep := filepath.Join(dir, "lanbox")
	if err := os.WriteFile(keep, []byte("keep"), 0o755); err != nil {
		t.Fatal(err)
	}
	if n := CleanupTemps(dir); n != 2 {
		t.Fatalf("清理了 %d 个，期望 2", n)
	}
	for _, p := range stale {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Fatalf("%s 没被清理", p)
		}
	}
	if _, err := os.Stat(keep); err != nil {
		t.Fatal("不该删掉正常文件")
	}
}

func TestStageMissingNewFile(t *testing.T) {
	exe, _ := tempExe(t, "旧版本", "x")
	_, err := Stage(exe, filepath.Join(filepath.Dir(exe), "nope"))
	if err == nil || !strings.Contains(err.Error(), "把新版本写入") {
		t.Fatalf("期望替换失败，得到 %v", err)
	}
	if got := mustRead(t, exe); got != "旧版本" {
		t.Fatalf("失败后原文件必须还原，得到 %q", got)
	}
}
