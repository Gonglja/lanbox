package store

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAddFileStreamsAndHashes(t *testing.T) {
	s := newStore(t, Options{})
	it, _ := s.Create(CreateInput{Text: "看附件", CreatedBy: "alice"})
	body := []byte(strings.Repeat("lanbox-", 1000))

	got, err := s.AddFile(it.ID, "报告 2026.pdf", int64(len(body)), bytes.NewReader(body))
	if err != nil {
		t.Fatalf("写附件失败: %v", err)
	}
	if len(got.Files) != 1 {
		t.Fatalf("应有 1 个附件: %+v", got.Files)
	}
	sum := sha256.Sum256(body)
	if got.Files[0].SHA256 != hex.EncodeToString(sum[:]) {
		t.Fatal("sha256 不正确")
	}
	if got.Files[0].Size != int64(len(body)) || got.Files[0].Name != "报告 2026.pdf" {
		t.Fatalf("附件元数据不对: %+v", got.Files[0])
	}

	path, f, err := s.FilePath(it.ID, got.Files[0].ID)
	if err != nil {
		t.Fatalf("解析附件路径失败: %v", err)
	}
	onDisk, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("附件未落盘: %v", err)
	}
	if !bytes.Equal(onDisk, body) {
		t.Fatal("磁盘内容与上传内容不一致")
	}
	if !strings.HasPrefix(f.ID, "f") {
		t.Fatalf("附件 ID 格式不对: %q", f.ID)
	}
	if !strings.Contains(filepath.Base(path), "__") {
		t.Fatalf("磁盘名应为 <文件ID>__<名字>: %s", path)
	}
}

func TestAddFileRejectsOversize(t *testing.T) {
	s := newStore(t, Options{MaxFileBytes: 10})
	it, _ := s.Create(CreateInput{Text: "x", CreatedBy: "u"})
	if _, err := s.AddFile(it.ID, "big.bin", 100, bytes.NewReader(make([]byte, 100))); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("应返回 ErrTooLarge，得到 %v", err)
	}
	if _, err := s.AddFile(it.ID, "lie.bin", 1, bytes.NewReader(make([]byte, 100))); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("声明大小撒谎时也应按实际字节拒绝，得到 %v", err)
	}
	it2, _ := s.Get(it.ID)
	if len(it2.Files) != 0 {
		t.Fatalf("失败不应留下附件记录: %+v", it2.Files)
	}
	entries, _ := os.ReadDir(s.filesDir(it.ID))
	if len(entries) != 0 {
		t.Fatalf("失败不应留下磁盘文件: %v", entries)
	}
}

func TestAddFileDiskGuard(t *testing.T) {
	s := newStore(t, Options{
		MaxFileBytes: 1 << 20,
		MinFreeBytes: 500 << 20,
		FreeSpace:    func(string) (int64, error) { return 100 << 20, nil },
	})
	it, _ := s.Create(CreateInput{Text: "x", CreatedBy: "u"})
	if _, err := s.AddFile(it.ID, "a.bin", 1, bytes.NewReader([]byte("a"))); !errors.Is(err, ErrNoSpace) {
		t.Fatalf("剩余空间低于下限应拒绝，得到 %v", err)
	}
}

func TestAddFileLimitPerItem(t *testing.T) {
	s := newStore(t, Options{})
	it, _ := s.Create(CreateInput{Text: "x", CreatedBy: "u"})
	for i := 0; i < MaxFilesPerItem; i++ {
		if _, err := s.AddFile(it.ID, "f.bin", 1, bytes.NewReader([]byte("a"))); err != nil {
			t.Fatalf("第 %d 个附件失败: %v", i, err)
		}
	}
	if _, err := s.AddFile(it.ID, "f.bin", 1, bytes.NewReader([]byte("a"))); !errors.Is(err, ErrTooManyFiles) {
		t.Fatalf("超过上限应报 ErrTooManyFiles，得到 %v", err)
	}
}

func TestDeleteFileAndDownloads(t *testing.T) {
	s := newStore(t, Options{})
	it, _ := s.Create(CreateInput{Text: "x", CreatedBy: "u"})
	got, _ := s.AddFile(it.ID, "a.txt", 3, bytes.NewReader([]byte("abc")))
	fid := got.Files[0].ID
	path, _, _ := s.FilePath(it.ID, fid)

	if err := s.IncDownloads(it.ID, fid); err != nil {
		t.Fatalf("计数失败: %v", err)
	}
	after, _ := s.Get(it.ID)
	if after.Files[0].Downloads != 1 {
		t.Fatalf("下载计数应为 1，得到 %d", after.Files[0].Downloads)
	}

	if _, err := s.DeleteFile(it.ID, fid); err != nil {
		t.Fatalf("删除附件失败: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("附件文件应已删除")
	}
	reloaded, _ := s.Get(it.ID)
	if len(reloaded.Files) != 0 {
		t.Fatalf("附件记录应已移除: %+v", reloaded.Files)
	}
	if _, err := s.DeleteFile(it.ID, fid); !errors.Is(err, ErrFileNotFound) {
		t.Fatalf("重复删除应报 ErrFileNotFound，得到 %v", err)
	}
}

func TestFilePathRejectsUnknownAndTraversal(t *testing.T) {
	s := newStore(t, Options{})
	it, _ := s.Create(CreateInput{Text: "x", CreatedBy: "u"})
	for _, bad := range []string{"", "..", "../../etc/passwd", "f../../x", "/etc/passwd"} {
		if _, _, err := s.FilePath(it.ID, bad); !errors.Is(err, ErrFileNotFound) {
			t.Fatalf("非法文件 ID %q 应报 ErrFileNotFound，得到 %v", bad, err)
		}
	}
	if _, _, err := s.FilePath("../../etc", "f1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("非法内容 ID 应报 ErrNotFound，得到 %v", err)
	}
}

func TestSanitizeName(t *testing.T) {
	cases := map[string]string{
		"report.pdf":          "report.pdf",
		"../../etc/passwd":    "passwd",
		"a/b\\c.txt":          "c.txt",
		"..":                  "未命名文件",
		".":                   "未命名文件",
		"":                    "未命名文件",
		".hidden":             "hidden",
		"na\x00me\x1f.txt":    "name.txt",
		"名字 带空格.md":           "名字 带空格.md",
		"con:*?\"<>|trol.txt": "control.txt",
	}
	for in, want := range cases {
		if got := SanitizeName(in); got != want {
			t.Fatalf("SanitizeName(%q) = %q，期望 %q", in, got, want)
		}
	}
	long := strings.Repeat("字", 300) + ".txt"
	got := SanitizeName(long)
	if len([]rune(got)) > 124 || !strings.HasSuffix(got, ".txt") {
		t.Fatalf("超长名字应被截断并保留扩展名: %q", got)
	}
}

func TestFileRelPathFallsBackToName(t *testing.T) {
	f := File{ID: "f1", Name: "a.txt"}
	if got := f.RelPath(); got != "a.txt" {
		t.Fatalf("无 path 时应回退文件名，得到 %q", got)
	}
	f.Path = "photos/a.txt"
	if got := f.RelPath(); got != "photos/a.txt" {
		t.Fatalf("有 path 时应返回路径，得到 %q", got)
	}
}

func TestOpenLegacyMetaWithoutPath(t *testing.T) {
	root := filepath.Join(t.TempDir(), "items")
	dir := filepath.Join(root, "0000000000001-abc")
	if err := os.MkdirAll(filepath.Join(dir, "files"), 0o750); err != nil {
		t.Fatal(err)
	}
	legacy := `{"id":"0000000000001-abc","created_at":"2026-01-01T00:00:00Z","created_by":"u","files":[{"id":"f1","name":"旧 文件.txt","size":3,"sha256":"abc"}]}`
	if err := os.WriteFile(filepath.Join(dir, "meta.json"), []byte(legacy), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "files", "f1__旧 文件.txt"), []byte("abc"), 0o640); err != nil {
		t.Fatal(err)
	}

	s, err := Open(root, Options{})
	if err != nil {
		t.Fatalf("打开存储失败: %v", err)
	}
	it, err := s.Get("0000000000001-abc")
	if err != nil {
		t.Fatalf("读取旧内容失败: %v", err)
	}
	if it.Files[0].Path != "" || it.Files[0].RelPath() != "旧 文件.txt" {
		t.Fatalf("旧数据应按 name 回退: %+v", it.Files[0])
	}
	disk, _, err := s.FilePath(it.ID, "f1")
	if err != nil {
		t.Fatalf("旧数据附件路径解析失败: %v", err)
	}
	if _, err := os.Stat(disk); err != nil {
		t.Fatalf("旧附件应能定位到磁盘: %v", err)
	}
}
