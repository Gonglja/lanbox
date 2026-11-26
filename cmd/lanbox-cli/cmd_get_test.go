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
