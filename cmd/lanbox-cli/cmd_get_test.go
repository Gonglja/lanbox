package main

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

// TestSafeJoin 断言服务端返回的相对路径不会逃出 -o 目录。
func TestSafeJoin(t *testing.T) {
	if got, err := safeJoin("/tmp/x", "photos/a.jpg"); err != nil || got != filepath.Join("/tmp/x", "photos", "a.jpg") {
		t.Fatalf("正常路径应通过: %q %v", got, err)
	}
	for _, bad := range []string{"../evil", "../../etc/passwd", "/etc/passwd"} {
		if _, err := safeJoin("/tmp/x", bad); err == nil {
			t.Fatalf("%q 应被拒绝", bad)
		}
	}
}

// TestDownloadSummaryError 断言多附件下载失败会被聚合成一个错误。
func TestDownloadSummaryError(t *testing.T) {
	s := &downloadSummary{Failed: []string{"a", "b"}}
	err := s.err()
	if err == nil || !strings.Contains(err.Error(), "2 个文件下载失败") {
		t.Fatalf("应聚合为失败错误，得到 %v", err)
	}
	if s.withNoneFailed() {
		t.Fatal("有失败时不应返回 true")
	}
	ok := &downloadSummary{Downloaded: []downloadedFile{{Path: "a", Size: 1}}}
	if !ok.withNoneFailed() || ok.err() != nil {
		t.Fatalf("全部成功时不应有错误: %v", ok.err())
	}
}

// TestDownloadSummaryErrorMentionsFirstCause 断言聚合错误里带上首个失败路径。
func TestDownloadSummaryErrorMentionsFirstCause(t *testing.T) {
	s := &downloadSummary{Failed: []string{"a/b.txt"}}
	if err := s.err(); err == nil || !strings.Contains(err.Error(), "a/b.txt") {
		t.Fatalf("聚合错误应提到失败路径，得到 %v", err)
	}
	if got := fmt.Sprint(s.err()); !strings.HasPrefix(got, "1 个文件下载失败") {
		t.Fatalf("聚合错误前缀不对: %q", got)
	}
}

// TestGetDestPathKeepsSingleFileDir 断言单附件带目录时不被压平成 basename。
func TestGetDestPathKeepsSingleFileDir(t *testing.T) {
	// 单附件但带目录：保留目录结构。
	dir := t.TempDir()
	got, err := destinationPath(dir, "中文/子目录/报告.txt", true)
	if err != nil || !strings.HasSuffix(got, filepath.Join("中文", "子目录", "报告.txt")) {
		t.Fatalf("单附件带目录时应保留路径，得到 %s %v", got, err)
	}
	if got != filepath.Join(dir, "中文", "子目录", "报告.txt") {
		t.Fatalf("落盘路径应完全等于相对路径拼接，得到 %s", got)
	}
	// 单附件无目录：压平（等价于 basename）。
	got, err = destinationPath(dir, "报告.txt", true)
	if err != nil || got != filepath.Join(dir, "报告.txt") {
		t.Fatalf("单附件无目录时应落到 basename，得到 %s %v", got, err)
	}
	// 多附件：始终保留相对路径。
	got, err = destinationPath(dir, "photos/a.jpg", false)
	if err != nil || got != filepath.Join(dir, "photos", "a.jpg") {
		t.Fatalf("多附件应保留相对路径，得到 %s %v", got, err)
	}
	// 越界路径仍应被拒绝。
	if _, err := destinationPath(dir, "../evil.txt", true); err == nil {
		t.Fatal("越界路径应被拒绝")
	}
}
