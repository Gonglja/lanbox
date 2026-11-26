package store

import (
	"os"
	"path/filepath"
	"testing"
)

// TestDiskFreeRealPath 断言各平台的 diskFree 实现能读到真实目录的可用空间。
// 这是 freespace_unix.go / freespace_windows.go 两个实现唯一的共用契约。
func TestDiskFreeRealPath(t *testing.T) {
	dir := t.TempDir()
	free, err := diskFree(dir)
	if err != nil {
		t.Fatalf("diskFree(%s) 失败: %v", dir, err)
	}
	if free <= 0 {
		t.Fatalf("可用空间应为正数，得到 %d", free)
	}
	// 1GiB 以内都算合理：测试机不可能真的一点空间都没有。
	if free < 1<<30 {
		t.Logf("警告：可用空间偏小: %d 字节", free)
	}
}

// TestDiskFreeMissingPath 断言不存在的路径返回错误而不是 panic。
func TestDiskFreeMissingPath(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "no-such-dir")
	if err := os.MkdirAll(missing, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(missing); err != nil {
		t.Fatal(err)
	}
	if _, err := diskFree(missing); err == nil {
		t.Fatal("不存在的路径应返回错误")
	}
}

// TestFreeBytesUsesInjectedProbe 已由 stats_test.go 覆盖，这里不重复定义。
