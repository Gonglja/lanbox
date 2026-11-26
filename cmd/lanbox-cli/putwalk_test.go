package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCollectUploadsFileAndDir(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "photos", "2024")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "a.jpg"), []byte("aaa"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "photos", "b.txt"), []byte("bb"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "photos", "empty"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "photos", "b.txt"), filepath.Join(root, "photos", "link.txt")); err != nil {
		t.Skip("当前环境不支持符号链接")
	}

	ups, err := collectUploads([]string{filepath.Join(root, "photos")}, true)
	if err != nil {
		t.Fatalf("展开目录失败: %v", err)
	}
	if len(ups) != 2 {
		t.Fatalf("应展开 2 个文件（跳过空目录与符号链接），得到 %d: %+v", len(ups), ups)
	}
	if ups[0].Path != "photos/2024/a.jpg" || ups[1].Path != "photos/b.txt" {
		t.Fatalf("相对路径不对: %+v", ups)
	}
	if ups[0].Size != 3 {
		t.Fatalf("大小不对: %+v", ups[0])
	}
	rc, err := ups[0].Open()
	if err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 3)
	if _, err := rc.Read(buf); err != nil {
		t.Fatal(err)
	}
	rc.Close()
	if string(buf) != "aaa" {
		t.Fatalf("内容不对: %q", buf)
	}
}

func TestCollectUploadsFileKeepsBasename(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "报告 2026.pdf")
	if err := os.WriteFile(path, []byte("pdf"), 0o640); err != nil {
		t.Fatal(err)
	}
	ups, err := collectUploads([]string{path}, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(ups) != 1 || ups[0].Path != "报告 2026.pdf" {
		t.Fatalf("单文件应只保留文件名: %+v", ups)
	}
}

func TestCollectUploadsDirWithoutRecursiveFlag(t *testing.T) {
	dir := t.TempDir()
	if _, err := collectUploads([]string{dir}, false); err == nil {
		t.Fatal("目录未加 -r 应报错")
	}
}
