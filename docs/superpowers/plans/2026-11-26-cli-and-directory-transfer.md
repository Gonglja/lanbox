# lanbox CLI 与目录传输实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 新增 `lanbox-cli` 命令行客户端，并让服务端与 CLI 都能上传、下载保留相对路径的目录树（含流式 zip 打包）。

**Architecture:** 服务端在 `store` 增加「相对路径」元数据与清洗/去重/限额逻辑，`httpapi` 用 `files:<相对路径>` 字段名接收（Go 的 `Part.FileName()` 会丢弃目录信息，这是唯一可用通道）并新增 `GET /api/items/{id}/zip` 流式打包。客户端拆成 `internal/client`（线上契约 DTO、会话、上传、下载）与 `cmd/lanbox-cli`（flag、渲染、退出码）。网页端改字段名、加目录入口与 zip 按钮。

**Tech Stack:** Go 1.26（**仅标准库**：`mime/multipart`、`archive/zip`、`crypto/sha256`、`syscall` ioctl）、原生 JS。

**规格来源：** `docs/superpowers/specs/2026-11-26-cli-and-directory-transfer-design.md`（实现时逐节对照）

---

## 文件结构

| 文件 | 责任 |
|---|---|
| `internal/store/path.go` | 新增：`SanitizeRelPath`、路径常量、`cleanSegment`、`isWindowsAbs` |
| `internal/store/path_test.go` | 新增：路径清洗表驱动测试 |
| `internal/store/store.go` | 修改：`File.Path`、`File.RelPath()`、`DefaultMaxFiles`/`DefaultMaxItemBytes`、新错误、`Options.MaxFiles/MaxItemBytes` |
| `internal/store/files.go` | 修改：`AddFile` 接受 `relPath`、撞名去重、单条总大小限额、`FilePath` 兜底 |
| `internal/store/files_test.go` | 修改：适配新签名，新增路径/撞名/限额测试 |
| `internal/config/config.go` + `config_test.go` | 修改：`max_files_per_item`、`max_item_size_mb` |
| `internal/httpapi/items_handlers.go` | 修改：`files:<relpath>` 字段解析 |
| `internal/httpapi/helpers.go` | 修改：`uploadPath`、新错误码映射、`too_many_files` 用配置值 |
| `internal/httpapi/zip_handlers.go` | 新增：流式 zip 下载 |
| `internal/httpapi/server.go` | 修改：注册 `/zip` 路由 |
| `internal/httpapi/api_test.go` | 修改：目录上传、zip、旧字段兼容测试 |
| `internal/client/dto.go` | 新增：`Item`/`File`/`User`/`ListResult`/`Error` |
| `internal/client/client.go` | 新增：请求封装、`Login`/`Logout`/`Me`/`List`/`GetItem`/`Taken`/`Delete*` |
| `internal/client/put.go` | 新增：multipart 上传（`files:<path>` + 进度） |
| `internal/client/get.go` | 新增：单文件下载（Range/续传/sha256/`.part`）、zip 下载 |
| `internal/client/client_test.go` | 新增：`httptest` 挂真实 `httpapi.Server` 的集成测试 |
| `cmd/lanbox-cli/main.go` | 新增：全局 flag、子命令分发、退出码 |
| `cmd/lanbox-cli/session.go` | 新增：会话文件读写（0600） |
| `cmd/lanbox-cli/password.go` | 新增：密码来源（env / 管道 / TTY） |
| `cmd/lanbox-cli/term_linux.go`、`term_other.go` | 新增：无回显读取（build tag 分开） |
| `cmd/lanbox-cli/putwalk.go` | 新增：目录展开为 `[]client.Upload` |
| `cmd/lanbox-cli/render.go` | 新增：表格 / JSON 输出（纯函数） |
| `cmd/lanbox-cli/cmd_*.go` | 新增：各子命令实现 |
| `cmd/lanbox-cli/*_test.go` | 新增：渲染、目录展开、会话权限测试 |
| `web/api.js`、`web/main.js`、`web/render.js`、`web/index.html`、`web/style.css` | 修改：目录上传、树状展示、zip 按钮 |
| `Makefile`、`README.md` | 修改：CLI 构建产物、文档 |

**边界规则（不得违反）：**
- `store` 仍不导入 `net/http`，只接受 `io.Reader` 与字符串路径。
- `internal/client` **不导入** `internal/store`；只认线上 JSON 契约。
- `httpapi` 不拼磁盘路径，一律经 `store` 方法。
- 每个文件单一职责；`cmd/lanbox-cli/main.go` 只做分发，超过 ~150 行就拆。

---

## Task 1: 相对路径清洗 `SanitizeRelPath`

**Files:**
- Create: `internal/store/path.go`
- Test: `internal/store/path_test.go`
- Modify: `internal/store/store.go`（错误变量）

- [ ] **Step 1: 写失败测试**

创建 `internal/store/path_test.go`：

```go
package store

import (
	"errors"
	"strings"
	"testing"
)

func TestSanitizeRelPath(t *testing.T) {
	deep := strings.Repeat("d/", MaxPathDepth+1) + "f"
	long := strings.Repeat(strings.Repeat("a", maxSegmentRunes)+"/", 8) + strings.Repeat("a", maxSegmentRunes)

	cases := []struct {
		in   string
		want string
		bad  bool
	}{
		{"a.txt", "a.txt", false},
		{"photos/2024/a.jpg", "photos/2024/a.jpg", false},
		{`photos\2024\a.jpg`, "photos/2024/a.jpg", false},
		{"a//b", "a/b", false},
		{"a/./b", "a/b", false},
		{"a/", "a", false},
		{".env", ".env", false},
		{"dir/.gitignore", "dir/.gitignore", false},
		{"  name.txt  ", "name.txt", false},
		{"a:b.txt", "ab.txt", false},
		{"a\x00b.txt", "ab.txt", false},
		{"", "", true},
		{"/etc/passwd", "", true},
		{"../x", "", true},
		{"a/../../b", "", true},
		{`..\x`, "", true},
		{`C:\Windows\x`, "", true},
		{"///", "", true},
		{" ..", "", true},
		{".\x00./x", "", true},
		{"a/\t../b", "", true},
		{strings.Repeat("a", maxSegmentRunes+1), "", true},
		{deep, "", true},
		{long, "", true},
	}
	for _, c := range cases {
		got, err := SanitizeRelPath(c.in)
		if c.bad {
			if !errors.Is(err, ErrBadPath) {
				t.Fatalf("%q 应报 ErrBadPath，得到 %q / %v", c.in, got, err)
			}
			continue
		}
		if err != nil {
			t.Fatalf("%q 不应报错: %v", c.in, err)
		}
		if got != c.want {
			t.Fatalf("%q 清洗为 %q，期望 %q", c.in, got, c.want)
		}
	}
}

func TestSanitizeRelPathDepthBoundary(t *testing.T) {
	ok := strings.Repeat("d/", MaxPathDepth-1) + "f"
	if _, err := SanitizeRelPath(ok); err != nil {
		t.Fatalf("%d 层应通过: %v", MaxPathDepth, err)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/store -run TestSanitizeRelPath -v`
Expected: 编译失败，`undefined: SanitizeRelPath`

- [ ] **Step 3: 实现**

创建 `internal/store/path.go`：

```go
package store

import "strings"

const (
	// MaxPathDepth 是附件相对路径的最大层数。
	MaxPathDepth = 16
	// MaxPathBytes 是附件相对路径的最大字节数。
	MaxPathBytes = 1024
	// maxSegmentRunes 是路径单段的最大 rune 数，超出即报错（不截断，避免撞名）。
	maxSegmentRunes = 120
)

// SanitizeRelPath 清洗用户提交的相对路径并保留目录结构。
// 拒绝绝对路径、盘符、任何 ".." 段、超深与超长路径。
// 与 SanitizeName 的唯一差异：保留前导点，使 .env / .gitignore 能正常传输。
func SanitizeRelPath(raw string) (string, error) {
	if raw == "" {
		return "", ErrBadPath
	}
	p := strings.ReplaceAll(raw, "\\", "/")
	if strings.HasPrefix(p, "/") || isWindowsAbs(p) {
		return "", ErrBadPath
	}
	segs := make([]string, 0, 8)
	for _, seg := range strings.Split(p, "/") {
		// 先清洗再判定：否则 " .."、".\x00." 这类输入清洗后会重组出 ".."。
		seg = cleanSegment(seg)
		switch seg {
		case "", ".":
			continue
		case "..":
			return "", ErrBadPath
		}
		if len([]rune(seg)) > maxSegmentRunes {
			return "", ErrBadPath
		}
		segs = append(segs, seg)
	}
	if len(segs) == 0 || len(segs) > MaxPathDepth {
		return "", ErrBadPath
	}
	out := strings.Join(segs, "/")
	if len(out) > MaxPathBytes {
		return "", ErrBadPath
	}
	return out, nil
}

// cleanSegment 去掉段内的控制字符与文件系统保留字符。
func cleanSegment(seg string) string {
	var b strings.Builder
	for _, r := range seg {
		if r < 0x20 || r == 0x7f {
			continue
		}
		if strings.ContainsRune(`/\:*?"<>|`, r) {
			continue
		}
		b.WriteRune(r)
	}
	return strings.TrimSpace(b.String())
}

// isWindowsAbs 判断是否是盘符形式的绝对路径（C:/x、c:/x）。
// 盘符后必须紧跟分隔符：否则 "a:b.txt" 这类普通文件名会被误判为绝对路径。
func isWindowsAbs(p string) bool {
	if len(p) < 3 || p[1] != ':' || p[2] != '/' {
		return false
	}
	c := p[0]
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}
```

在 `internal/store/store.go` 的错误块里追加：

```go
	ErrBadPath      = errors.New("附件路径不合法")
	ErrItemTooLarge = errors.New("内容总大小超过上限")
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/store -run TestSanitizeRelPath -v`
Expected: PASS（两个测试）

- [ ] **Step 5: 提交**

```bash
git add internal/store/path.go internal/store/path_test.go internal/store/store.go
git commit -m "feat(store): 相对路径清洗 SanitizeRelPath"
```

---

## Task 2: `File.Path` 与旧数据兼容

**Files:**
- Modify: `internal/store/store.go:34-40`（`File` 结构体）
- Modify: `internal/store/files.go:110-140`（`FilePath`）
- Test: `internal/store/files_test.go`

- [ ] **Step 1: 写失败测试**

在 `internal/store/files_test.go` 末尾追加：

```go
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
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/store -run 'TestFileRelPath|TestOpenLegacy' -v`
Expected: 编译失败，`f.RelPath undefined`

- [ ] **Step 3: 实现**

`internal/store/store.go` 的 `File` 改为：

```go
// File 是一个附件。
type File struct {
	ID        string `json:"id"`
	Path      string `json:"path"` // 相对路径，如 "photos/2024/a.jpg"
	Name      string `json:"name"` // basename，兼容旧前端与磁盘后缀
	Size      int64  `json:"size"`
	SHA256    string `json:"sha256"`
	Downloads int64  `json:"downloads"`
}

// RelPath 返回附件在内容内的相对路径；旧数据没有 path 时回退到文件名。
func (f File) RelPath() string {
	if f.Path != "" {
		return f.Path
	}
	if f.Name != "" {
		return f.Name
	}
	return f.ID
}
```

`internal/store/files.go` 的 `FilePath` 里把磁盘名拼装改成兜底形式：

```go
	name := found.Name
	if name == "" {
		name = path.Base(found.RelPath())
	}
	p := filepath.Join(s.filesDir(itemID), found.ID+"__"+name)
	return p, found, nil
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/store -v`
Expected: 全部 PASS

- [ ] **Step 5: 提交**

```bash
git add internal/store/store.go internal/store/files.go internal/store/files_test.go
git commit -m "feat(store): 附件记录相对路径并兼容旧数据"
```

---

## Task 3a: 附件磁盘名单一真源与按路径搜索

**Files:**
- Modify: `internal/store/files.go`（新增 `diskName`，`AddFile`/`FilePath`/`DeleteFile` 改用它）
- Modify: `internal/store/store.go`（`Item.match`）
- Test: `internal/store/files_test.go`

> 为什么先做这一步：`AddFile`（写）、`FilePath`（读）、`DeleteFile`（删）各自拼 `<fid>__<name>`，其中 `DeleteFile` 没有空 `Name` 兜底，导致「`path` 有值、`name` 为空」的条目读得到、删不掉（孤儿文件静默泄漏）。`AddFile` 在 Task 3 要整段重写，先收敛真源最省事。

- [ ] **Step 1: 写失败测试**

在 `internal/store/files_test.go` 末尾追加：

```go
func TestDiskName(t *testing.T) {
	cases := []struct {
		f    File
		want string
	}{
		{File{ID: "f1", Name: "a.txt", Path: "photos/a.txt"}, "f1__a.txt"},
		{File{ID: "f2", Path: "photos/a.jpg"}, "f2__a.jpg"},
		{File{ID: "f3"}, "f3__f3"},
	}
	for _, c := range cases {
		if got := diskName(c.f); got != c.want {
			t.Fatalf("diskName(%+v)=%q，期望 %q", c.f, got, c.want)
		}
	}
}

func TestRelPathFallsBackToID(t *testing.T) {
	if got := (File{ID: "f0"}).RelPath(); got != "f0" {
		t.Fatalf("双空时应回退 ID，得到 %q", got)
	}
}

func TestFilePathUsesBasenameWhenNameEmpty(t *testing.T) {
	root := filepath.Join(t.TempDir(), "items")
	dir := filepath.Join(root, "0000000000002-def")
	if err := os.MkdirAll(filepath.Join(dir, "files"), 0o750); err != nil {
		t.Fatal(err)
	}
	meta := `{"id":"0000000000002-def","created_at":"2026-01-01T00:00:00Z","created_by":"u","files":[{"id":"f9","path":"photos/2024/a.jpg","size":3}]}`
	if err := os.WriteFile(filepath.Join(dir, "meta.json"), []byte(meta), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "files", "f9__a.jpg"), []byte("abc"), 0o640); err != nil {
		t.Fatal(err)
	}

	s, err := Open(root, Options{})
	if err != nil {
		t.Fatalf("打开存储失败: %v", err)
	}
	disk, f, err := s.FilePath("0000000000002-def", "f9")
	if err != nil {
		t.Fatalf("解析附件路径失败: %v", err)
	}
	if filepath.Base(disk) != "f9__a.jpg" {
		t.Fatalf("应回退到 path 的 basename: %s", disk)
	}
	if f.RelPath() != "photos/2024/a.jpg" {
		t.Fatalf("RelPath 应返回 path，得到 %q", f.RelPath())
	}

	// 删除也要走同一套磁盘名：没有兜底时会删 <fid>__ 而静默留下孤儿文件。
	if _, err := s.DeleteFile("0000000000002-def", "f9"); err != nil {
		t.Fatalf("删除附件失败: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "files", "f9__a.jpg")); !os.IsNotExist(err) {
		t.Fatal("附件文件应已被删除")
	}
}

func TestMatchMatchesNestedPath(t *testing.T) {
	it := &Item{Files: []File{{ID: "f1", Path: "photos/2024/a.jpg", Name: "a.jpg"}}}
	for _, needle := range []string{"photos", "photos/2024/a.jpg", "2024", "a.jpg"} {
		if !it.match(strings.ToLower(needle)) {
			t.Fatalf("%q 应命中路径", needle)
		}
	}
	if it.match("videos") {
		t.Fatal("不相关的关键词不应命中")
	}

	legacy := &Item{Files: []File{{ID: "f2", Name: "a.txt"}}}
	if !legacy.match("a.txt") {
		t.Fatal("旧数据（无 path）仍应按文件名命中")
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/store -run 'TestDiskName|TestRelPathFallsBackToID|TestFilePathUsesBasename|TestMatchMatches' -v`
Expected: 编译失败，`undefined: diskName`

- [ ] **Step 3: 实现**

`internal/store/files.go` 新增（放在 `AddFile` 之前）：

```go
// diskName 返回附件在 files/ 下的磁盘文件名：<文件ID>__<basename>。
// 写（AddFile）、读（FilePath）、删（DeleteFile）共用，避免三处各自拼装后走偏。
// 极端情况下 Path 与 Name 都为空（外部写入的 meta），退化为 <fid>__<fid>。
func diskName(f File) string {
	name := f.Name
	if name == "" {
		name = path.Base(f.RelPath())
	}
	return f.ID + "__" + name
}
```

`FilePath` 收敛为：

```go
	p := filepath.Join(s.filesDir(itemID), diskName(*found))
	return p, found, nil
```

`DeleteFile` 里的磁盘名同样改用 `diskName`：

```go
	diskPath := filepath.Join(s.filesDir(itemID), diskName(cp.Files[idx]))
```

`internal/store/store.go` 的 `Item.match` 改为（保留 `Name` 比较，旧数据行为完全等价）：

```go
func (it *Item) match(needleLower string) bool {
	if strings.Contains(strings.ToLower(it.Text), needleLower) {
		return true
	}
	for _, f := range it.Files {
		// RelPath 覆盖完整路径，目录名也能被搜到；Name 兜底防 Path 与 Name 不一致。
		if strings.Contains(strings.ToLower(f.RelPath()), needleLower) ||
			strings.Contains(strings.ToLower(f.Name), needleLower) {
			return true
		}
	}
	return false
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/store -v && go test ./... -count=1 && go vet ./... && gofmt -l .`
Expected: 全绿、无输出

- [ ] **Step 5: 提交**

```bash
git add internal/store/files.go internal/store/store.go internal/store/files_test.go
git commit -m "refactor(store): 磁盘名收敛为 diskName，搜索支持相对路径"
```

---

## Task 3: `AddFile` 支持路径、撞名去重与单条总大小

**Files:**
- Modify: `internal/store/store.go`（`Options`、常量）
- Modify: `internal/store/files.go:19-100`（`AddFile`）
- Modify: `internal/store/files_test.go`
- Modify: `internal/httpapi/items_handlers.go:100`（签名适配，先编译通过）

- [ ] **Step 1: 写失败测试**

在 `internal/store/files_test.go` 末尾追加：

```go
func TestAddFileKeepsRelativePath(t *testing.T) {
	s := newStore(t, Options{})
	it, _ := s.Create(CreateInput{Text: "x", CreatedBy: "u"})
	got, err := s.AddFile(it.ID, `photos\2024/a.jpg`, 3, bytes.NewReader([]byte("abc")))
	if err != nil {
		t.Fatalf("写附件失败: %v", err)
	}
	f := got.Files[0]
	if f.Path != "photos/2024/a.jpg" || f.Name != "a.jpg" {
		t.Fatalf("路径/名字不对: %+v", f)
	}
	disk, _, err := s.FilePath(it.ID, f.ID)
	if err != nil {
		t.Fatalf("定位附件失败: %v", err)
	}
	if filepath.Base(disk) != f.ID+"__a.jpg" {
		t.Fatalf("磁盘名应保持扁平: %s", disk)
	}
}

func TestAddFileDedupesSamePath(t *testing.T) {
	s := newStore(t, Options{})
	it, _ := s.Create(CreateInput{Text: "x", CreatedBy: "u"})
	if _, err := s.AddFile(it.ID, "A.txt", 1, bytes.NewReader([]byte("1"))); err != nil {
		t.Fatal(err)
	}
	got, err := s.AddFile(it.ID, "a.txt", 1, bytes.NewReader([]byte("2")))
	if err != nil {
		t.Fatal(err)
	}
	if got.Files[1].Path != "a~2.txt" {
		t.Fatalf("大小写不同的同名应去重为 a~2.txt，得到 %q", got.Files[1].Path)
	}
	got, err = s.AddFile(it.ID, "a.txt", 1, bytes.NewReader([]byte("3")))
	if err != nil {
		t.Fatal(err)
	}
	if got.Files[2].Path != "a~3.txt" {
		t.Fatalf("第三次应为 a~3.txt，得到 %q", got.Files[2].Path)
	}
}

func TestAddFileRejectsBadPath(t *testing.T) {
	s := newStore(t, Options{})
	it, _ := s.Create(CreateInput{Text: "x", CreatedBy: "u"})
	if _, err := s.AddFile(it.ID, "../escape.txt", 1, bytes.NewReader([]byte("x"))); !errors.Is(err, ErrBadPath) {
		t.Fatalf("应报 ErrBadPath，得到 %v", err)
	}
	after, _ := s.Get(it.ID)
	if len(after.Files) != 0 {
		t.Fatalf("失败不应留下附件记录: %+v", after.Files)
	}
}

func TestAddFileRejectsItemOversize(t *testing.T) {
	s := newStore(t, Options{MaxItemBytes: 5})
	it, _ := s.Create(CreateInput{Text: "x", CreatedBy: "u"})
	if _, err := s.AddFile(it.ID, "a.bin", 5, bytes.NewReader([]byte("12345"))); err != nil {
		t.Fatalf("刚好到上限应成功: %v", err)
	}
	if _, err := s.AddFile(it.ID, "b.bin", 1, bytes.NewReader([]byte("6"))); !errors.Is(err, ErrItemTooLarge) {
		t.Fatalf("超过单条上限应报 ErrItemTooLarge，得到 %v", err)
	}
	after, _ := s.Get(it.ID)
	if len(after.Files) != 1 || after.Files[0].Path != "a.bin" {
		t.Fatalf("失败后不应追加附件: %+v", after.Files)
	}
}

func TestAddFileMaxFilesOption(t *testing.T) {
	s := newStore(t, Options{MaxFiles: 2})
	it, _ := s.Create(CreateInput{Text: "x", CreatedBy: "u"})
	for i := 0; i < 2; i++ {
		if _, err := s.AddFile(it.ID, "f.bin", 1, bytes.NewReader([]byte("a"))); err != nil {
			t.Fatalf("第 %d 个附件失败: %v", i, err)
		}
	}
	if _, err := s.AddFile(it.ID, "f.bin", 1, bytes.NewReader([]byte("a"))); !errors.Is(err, ErrTooManyFiles) {
		t.Fatalf("超过 MaxFiles 应报 ErrTooManyFiles，得到 %v", err)
	}
}
```

并把已有的 `TestAddFileLimitPerItem` 改为使用明确上限，避免写 500 个文件：

```go
func TestAddFileLimitPerItem(t *testing.T) {
	s := newStore(t, Options{MaxFiles: 3})
	it, _ := s.Create(CreateInput{Text: "x", CreatedBy: "u"})
	for i := 0; i < 3; i++ {
		if _, err := s.AddFile(it.ID, "f.bin", 1, bytes.NewReader([]byte("a"))); err != nil {
			t.Fatalf("第 %d 个附件失败: %v", i, err)
		}
	}
	if _, err := s.AddFile(it.ID, "f.bin", 1, bytes.NewReader([]byte("a"))); !errors.Is(err, ErrTooManyFiles) {
		t.Fatalf("超过上限应报 ErrTooManyFiles，得到 %v", err)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/store -run TestAddFile -v`
Expected: 编译失败（`Options.MaxFiles`/`MaxItemBytes` 不存在、`AddFile` 参数不匹配）

- [ ] **Step 3: 实现**

`internal/store/store.go`：把 `MaxFilesPerItem` 常量替换为默认值常量，并给 `Options` 加字段：

```go
const (
	// DefaultMaxFiles 是单条内容附件数量的默认上限。
	DefaultMaxFiles = 500
	// DefaultMaxItemBytes 是单条内容附件总大小的默认上限（2 GiB）。
	DefaultMaxItemBytes = 2 << 30
)

// Options 是存储的可调参数。FreeSpace 为空时使用真实磁盘查询。
type Options struct {
	MaxFileBytes int64
	MinFreeBytes int64
	MaxTextBytes int
	MaxFiles     int   // 单条内容的附件数量上限，0 表示 DefaultMaxFiles
	MaxItemBytes int64 // 单条内容的附件总字节上限，0 表示不限
	FreeSpace    func(path string) (int64, error)
}
```

`internal/store/files.go`：整段替换 `AddFile`，并新增两个小工具函数（放在 `AddFile` 之前）。注意 `files.go` 的 import 需要补 `strconv`（`uniqueRelPath` 使用）：

```go
// maxFiles 返回生效的附件数量上限。
func (s *Store) maxFiles() int {
	if s.opts.MaxFiles > 0 {
		return s.opts.MaxFiles
	}
	return DefaultMaxFiles
}

// newFileID 生成附件 ID。
func newFileID() string {
	var b [4]byte
	for i := range b {
		b[i] = byte(rand.UintN(256))
	}
	return "f" + hex.EncodeToString(b[:])
}

// uniqueRelPath 在已占用路径集合里为 p 找一个不冲突的路径。
// 比较大小写不敏感：Windows/macOS 解压时 A.txt 与 a.txt 会互相覆盖。
func uniqueRelPath(taken map[string]bool, p string) string {
	if !taken[strings.ToLower(p)] {
		return p
	}
	dir, base := path.Split(p)
	ext := path.Ext(base)
	stem := strings.TrimSuffix(base, ext)
	for i := 2; i <= 1000; i++ {
		cand := dir + stem + "~" + strconv.Itoa(i) + ext
		if !taken[strings.ToLower(cand)] {
			return cand
		}
	}
	return ""
}
```

```go
// AddFile 将 r 流式写入该内容的 files 目录，同时计算 sha256。
// relPath 是内容内的相对路径，写入前经 SanitizeRelPath 清洗；
// 磁盘布局仍是 files/<fid>__<basename>，与旧数据保持一致。
// 调用约定：同一 item 的 AddFile 必须串行调用。
func (s *Store) AddFile(itemID, relPath string, declaredSize int64, r io.Reader) (*Item, error) {
	cleanPath, err := SanitizeRelPath(relPath)
	if err != nil {
		return nil, err
	}

	s.mu.RLock()
	cur, ok := s.index[itemID]
	var count int
	var used int64
	taken := map[string]bool{}
	if ok {
		count = len(cur.Files)
		for _, f := range cur.Files {
			used += f.Size
			taken[strings.ToLower(f.RelPath())] = true
		}
	}
	s.mu.RUnlock()
	if !ok {
		return nil, ErrNotFound
	}
	if count >= s.maxFiles() {
		return nil, ErrTooManyFiles
	}

	hasItemLimit := s.opts.MaxItemBytes > 0
	var remaining int64
	if hasItemLimit {
		remaining = s.opts.MaxItemBytes - used
		if remaining <= 0 {
			return nil, ErrItemTooLarge
		}
	}

	finalPath := uniqueRelPath(taken, cleanPath)
	if finalPath == "" {
		return nil, fmt.Errorf("同名附件过多，无法为 %s 分配路径", cleanPath)
	}
	base := path.Base(finalPath)

	if s.opts.FreeSpace != nil {
		free, err := s.opts.FreeSpace(s.itemsRoot)
		if err != nil {
			return nil, fmt.Errorf("检查磁盘空间失败: %w", err)
		}
		need := s.opts.MinFreeBytes
		if declaredSize > 0 {
			need += declaredSize
		}
		if free < need {
			return nil, ErrNoSpace
		}
	}

	fid := newFileID()
	diskPath := filepath.Join(s.filesDir(itemID), diskName(File{ID: fid, Path: finalPath, Name: base}))

	f, err := os.OpenFile(diskPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o640)
	if err != nil {
		return nil, fmt.Errorf("创建附件文件失败: %w", err)
	}
	hasher := sha256.New()

	// 读取上限取单文件上限与单条剩余额度的较小值，超限时立即中止。
	limit := s.opts.MaxFileBytes
	if hasItemLimit && (limit <= 0 || remaining < limit) {
		limit = remaining
	}
	var src io.Reader = r
	if limit > 0 {
		src = io.LimitReader(r, limit+1)
	}

	n, copyErr := io.Copy(io.MultiWriter(f, hasher), src)
	closeErr := f.Close()
	if copyErr != nil {
		os.Remove(diskPath)
		return nil, fmt.Errorf("写入附件失败: %w", copyErr)
	}
	if closeErr != nil {
		os.Remove(diskPath)
		return nil, fmt.Errorf("保存附件失败: %w", closeErr)
	}
	if s.opts.MaxFileBytes > 0 && n > s.opts.MaxFileBytes {
		os.Remove(diskPath)
		return nil, ErrTooLarge
	}
	if hasItemLimit && n > remaining {
		os.Remove(diskPath)
		return nil, ErrItemTooLarge
	}

	file := File{
		ID:     fid,
		Path:   finalPath,
		Name:   base,
		Size:   n,
		SHA256: hex.EncodeToString(hasher.Sum(nil)),
	}

	s.mu.Lock()
	it, ok := s.index[itemID]
	if !ok {
		s.mu.Unlock()
		os.Remove(diskPath)
		return nil, ErrNotFound
	}
	cp := it.clone()
	cp.Files = append(cp.Files, file)
	if err := s.saveMeta(cp); err != nil {
		s.mu.Unlock()
		os.Remove(diskPath)
		return nil, err
	}
	s.index[itemID] = cp
	s.mu.Unlock()
	return cp.clone(), nil
}
```

`internal/httpapi/helpers.go` 与 `items_handlers.go` 暂时把 `store.MaxFilesPerItem` / `AddFile` 调用改成新签名（完整解析在 Task 5 做，这里只为编译通过）：

- `helpers.go`：`fmt.Sprintf("附件数量不能超过 %d 个", store.DefaultMaxFiles)`
- `items_handlers.go`：`s.store.AddFile(item.ID, name, -1, part)`

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/store -v && go build ./...`
Expected: 全部 PASS，编译通过

- [ ] **Step 5: 提交**

```bash
git add internal/store internal/httpapi/helpers.go internal/httpapi/items_handlers.go
git commit -m "feat(store): 附件相对路径、撞名去重与单条总大小限额"
```

---

## Task 4: 配置项 `max_files_per_item` 与 `max_item_size_mb`

**Files:**
- Modify: `internal/config/config.go`
- Test: `internal/config/config_test.go`
- Modify: `cmd/lanbox/main.go`（把新值传给 store）

- [ ] **Step 1: 写失败测试**

在 `internal/config/config_test.go` 末尾追加：

```go
func TestDirectoryLimitsDefaults(t *testing.T) {
	c := Defaults()
	if c.MaxFilesPerItem != 500 {
		t.Fatalf("默认 max_files_per_item 应为 500，得到 %d", c.MaxFilesPerItem)
	}
	if c.MaxItemSizeMB != 2048 {
		t.Fatalf("默认 max_item_size_mb 应为 2048，得到 %d", c.MaxItemSizeMB)
	}
	if c.MaxItemBytes() != 2048<<20 {
		t.Fatalf("MaxItemBytes 换算错误: %d", c.MaxItemBytes())
	}
}

func TestDirectoryLimitsValidation(t *testing.T) {
	c := Defaults()
	c.MaxFilesPerItem = 0
	if err := c.Validate(); err == nil {
		t.Fatal("max_files_per_item 为 0 应报错")
	}
	c = Defaults()
	c.MaxFilesPerItem = 10001
	if err := c.Validate(); err == nil {
		t.Fatal("max_files_per_item 超过 10000 应报错")
	}
	c = Defaults()
	c.MaxItemSizeMB = 0
	if err := c.Validate(); err == nil {
		t.Fatal("max_item_size_mb 为 0 应报错")
	}
}

func TestDirectoryLimitsEnvOverride(t *testing.T) {
	t.Setenv("LANBOX_MAX_FILES_PER_ITEM", "42")
	t.Setenv("LANBOX_MAX_ITEM_SIZE_MB", "64")
	c, err := Load(filepath.Join(t.TempDir(), "missing.json"))
	if err != nil {
		t.Fatalf("加载配置失败: %v", err)
	}
	if c.MaxFilesPerItem != 42 || c.MaxItemSizeMB != 64 {
		t.Fatalf("环境变量未生效: %+v", c)
	}
}
```

若 `config_test.go` 尚未导入 `path/filepath`，在 import 块补上。

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/config -run TestDirectoryLimits -v`
Expected: 编译失败，`MaxFilesPerItem undefined`

- [ ] **Step 3: 实现**

`internal/config/config.go`：

```go
type Config struct {
	Listen          string `json:"listen"`
	DataDir         string `json:"data_dir"`
	TLS             TLS    `json:"tls"`
	MaxFileSizeMB   int64  `json:"max_file_size_mb"`
	MaxItemSizeMB   int64  `json:"max_item_size_mb"`
	MinFreeDiskMB   int64  `json:"min_free_disk_mb"`
	MaxFilesPerItem int    `json:"max_files_per_item"`
	MaxTextBytes    int    `json:"max_text_bytes"`
	SessionTTLHours int    `json:"session_ttl_hours"`
}

func Defaults() Config {
	return Config{
		Listen:          ":8443",
		DataDir:         "./data",
		MaxFileSizeMB:   1024,
		MaxItemSizeMB:   2048,
		MinFreeDiskMB:   1024,
		MaxFilesPerItem: 500,
		MaxTextBytes:    65536,
		SessionTTLHours: 168,
	}
}
```

环境变量（`applyEnv`）：

```go
	if v, ok := os.LookupEnv("LANBOX_MAX_FILES_PER_ITEM"); ok {
		n, err := strconv.Atoi(v)
		if err != nil {
			return fmt.Errorf("环境变量 LANBOX_MAX_FILES_PER_ITEM 不是整数: %q", v)
		}
		c.MaxFilesPerItem = n
	}
```

并把 `LANBOX_MAX_ITEM_SIZE_MB` 加进已有的 `ints` 表（`{"LANBOX_MAX_ITEM_SIZE_MB", &c.MaxItemSizeMB}`）。

校验（`Validate`）追加：

```go
	if c.MaxItemSizeMB <= 0 {
		return errors.New("max_item_size_mb 必须大于 0")
	}
	if c.MaxFilesPerItem < 1 || c.MaxFilesPerItem > 10000 {
		return errors.New("max_files_per_item 必须在 1..10000 之间")
	}
```

单位换算追加：

```go
// MaxItemBytes 是单条内容附件总大小上限（字节）。
func (c Config) MaxItemBytes() int64 { return c.MaxItemSizeMB << 20 }
```

`cmd/lanbox/main.go` 的 `store.Open` 调用补两个字段：

```go
	st, err := store.Open(filepath.Join(cfg.DataDir, "items"), store.Options{
		MaxFileBytes: cfg.MaxFileBytes(),
		MinFreeBytes: cfg.MinFreeBytes(),
		MaxTextBytes: cfg.MaxTextBytes,
		MaxFiles:     cfg.MaxFilesPerItem,
		MaxItemBytes: cfg.MaxItemBytes(),
	})
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/config -v && go build ./...`
Expected: 全部 PASS

- [ ] **Step 5: 提交**

```bash
git add internal/config cmd/lanbox/main.go
git commit -m "feat(config): 附件数量与单条总大小可配置"
```

---

## Task 5: 上传接口接受 `files:<相对路径>`

**Files:**
- Modify: `internal/httpapi/helpers.go`（`uploadPath`、错误映射）
- Modify: `internal/httpapi/items_handlers.go:100-115`
- Test: `internal/httpapi/api_test.go`

- [ ] **Step 1: 写失败测试**

在 `internal/httpapi/api_test.go` 末尾追加：

```go
func TestUploadKeepsRelativePaths(t *testing.T) {
	e := newEnv(t)
	e.loginAdmin(t)

	it := e.uploadParts(t, []uploadPart{
		{field: "files:photos/2024/a.jpg", filename: "a.jpg", body: "jpeg-bytes"},
		{field: "files", filename: "b.txt", body: "hello"},
	})
	if len(it.Files) != 2 {
		t.Fatalf("应有 2 个附件: %+v", it.Files)
	}
	if it.Files[0].Path != "photos/2024/a.jpg" || it.Files[0].Name != "a.jpg" {
		t.Fatalf("路径字段不对: %+v", it.Files[0])
	}
	if it.Files[1].Path != "b.txt" {
		t.Fatalf("旧字段 files 应按文件名回退，得到 %+v", it.Files[1])
	}
}

func TestUploadRejectsBadPath(t *testing.T) {
	e := newEnv(t)
	e.loginAdmin(t)

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	part, err := mw.CreateFormFile("files:../../etc/passwd", "passwd")
	if err != nil {
		t.Fatal(err)
	}
	part.Write([]byte("x"))
	mw.Close()

	req, err := http.NewRequest(http.MethodPost, e.srv.URL+"/api/items", &buf)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, body := e.do(t, req)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("非法路径应 400，得到 %d %s", resp.StatusCode, body)
	}
	var eb struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &eb); err != nil {
		t.Fatal(err)
	}
	if eb.Error.Code != "bad_path" {
		t.Fatalf("错误码应为 bad_path，得到 %q", eb.Error.Code)
	}

	_, body = e.do(t, e.newJSONReq(t, http.MethodGet, "/api/items", nil))
	var list struct {
		Items []store.Item `json:"items"`
	}
	if err := json.Unmarshal(body, &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Items) != 0 {
		t.Fatalf("失败的上传不应留下内容: %+v", list.Items)
	}
}

// uploadPart 描述一次 multipart 文件 part。
type uploadPart struct {
	field    string // 字段名，如 "files:photos/a.jpg"
	filename string
	body     string
}

// uploadParts 用自定义字段名上传并返回解析后的内容。
func (e *env) uploadParts(t *testing.T, parts []uploadPart) store.Item {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for _, p := range parts {
		w, err := mw.CreateFormFile(p.field, p.filename)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(p.body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPost, e.srv.URL+"/api/items", &buf)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, body := e.do(t, req)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("上传应 201，得到 %d %s", resp.StatusCode, body)
	}
	return decodeItem(t, body)
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/httpapi -run TestUploadKeeps -v`
Expected: FAIL，`Path` 为空（服务端仍按 `FileName()` 处理）

- [ ] **Step 3: 实现**

`internal/httpapi/helpers.go` 追加：

```go
// uploadPath 从 multipart 字段名解析附件相对路径。
// 新前端/CLI 用 "files:<相对路径>"；旧前端用 "files" + filename。
// 返回 isFile=false 表示这不是文件字段。
func uploadPath(field, filename string) (path string, isFile bool) {
	if rel, ok := strings.CutPrefix(field, "files:"); ok {
		return rel, true // rel 为空也交给 store 判错
	}
	if filename != "" {
		return filename, true
	}
	return "", false
}
```

`writeStoreErr` 的两条分支改为：

```go
	case errors.Is(err, store.ErrTooManyFiles):
		writeError(w, http.StatusBadRequest, "too_many_files",
			fmt.Sprintf("附件数量不能超过 %d 个", s.cfg.MaxFilesPerItem))
	case errors.Is(err, store.ErrBadPath):
		writeError(w, http.StatusBadRequest, "bad_path", "附件路径不合法")
	case errors.Is(err, store.ErrItemTooLarge):
		writeError(w, http.StatusRequestEntityTooLarge, "item_too_large",
			fmt.Sprintf("单条内容超过 %dMB 上限", s.cfg.MaxItemSizeMB))
```

若 `api_test` 的 env 里 `cfg.MaxItemSizeMB` 为 0（因为它手工构造 `config.Config`），在 `newEnv` 里补 `cfg.MaxItemSizeMB = 16`、`cfg.MaxFilesPerItem = 50`。同时把新选项传给 store：

```go
	st, err := store.Open(filepath.Join(cfg.DataDir, "items"), store.Options{
		MaxFileBytes: cfg.MaxFileBytes(),
		MinFreeBytes: 0,
		MaxTextBytes: cfg.MaxTextBytes,
		MaxFiles:     cfg.MaxFilesPerItem,
		MaxItemBytes: cfg.MaxItemBytes(),
	})
```

`internal/httpapi/items_handlers.go` 的循环体改为：

```go
	for {
		part, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			cleanup()
			writeError(w, http.StatusBadRequest, "bad_request", "读取上传数据失败: "+err.Error())
			return
		}
		if part.FormName() == "text" {
			// ……保持原有逻辑不变……
			continue
		}
		relPath, isFile := uploadPath(part.FormName(), part.FileName())
		if !isFile {
			part.Close()
			continue
		}
		if item == nil {
			created, err := s.store.Create(store.CreateInput{CreatedBy: user.Username, SourceIP: ip})
			if err != nil {
				part.Close()
				s.writeStoreErr(w, err)
				return
			}
			item = created
		}
		updated, err := s.store.AddFile(item.ID, relPath, -1, part)
		part.Close()
		if err != nil {
			cleanup()
			s.writeStoreErr(w, err)
			return
		}
		item = updated
		files++
	}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/httpapi -v && go test ./... -count=1`
Expected: 全部 PASS

- [ ] **Step 5: 提交**

```bash
git add internal/httpapi
git commit -m "feat(httpapi): 上传支持 files:<相对路径> 字段"
```

---

## Task 6: 流式 zip 下载

**Files:**
- Create: `internal/httpapi/zip_handlers.go`
- Modify: `internal/httpapi/server.go:63-70`（路由）
- Test: `internal/httpapi/api_test.go`

- [ ] **Step 1: 写失败测试**

在 `internal/httpapi/api_test.go` 末尾追加：

```go
func TestDownloadZip(t *testing.T) {
	e := newEnv(t)
	e.loginAdmin(t)
	it := e.uploadParts(t, []uploadPart{
		{field: "files:photos/2024/a.jpg", filename: "a.jpg", body: "aaa"},
		{field: "files", filename: "notes.txt", body: "hello"},
	})

	resp, body := e.do(t, e.newJSONReq(t, http.MethodGet, "/api/items/"+it.ID+"/zip", nil))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("zip 应 200，得到 %d %s", resp.StatusCode, body)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/zip" {
		t.Fatalf("Content-Type 应为 application/zip，得到 %q", ct)
	}
	if cd := resp.Header.Get("Content-Disposition"); !strings.Contains(cd, "lanbox-"+it.ID+".zip") {
		t.Fatalf("Content-Disposition 不对: %q", cd)
	}

	zr, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		t.Fatalf("解析 zip 失败: %v", err)
	}
	got := map[string]string{}
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		raw, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			t.Fatal(err)
		}
		got[f.Name] = string(raw)
	}
	if got["photos/2024/a.jpg"] != "aaa" || got["notes.txt"] != "hello" {
		t.Fatalf("zip 内容不对: %+v", got)
	}
}

func TestDownloadZipWithoutFiles(t *testing.T) {
	e := newEnv(t)
	e.loginAdmin(t)
	it := e.uploadParts(t, []uploadPart{{field: "text", filename: "", body: "只有文字"}})
	resp, body := e.do(t, e.newJSONReq(t, http.MethodGet, "/api/items/"+it.ID+"/zip", nil))
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("无附件应 400，得到 %d %s", resp.StatusCode, body)
	}
}

func TestDownloadZipRequiresAuth(t *testing.T) {
	e := newEnv(t)
	resp, body := e.doWith(t, e.clientNoJar, e.newJSONReq(t, http.MethodGet, "/api/items/anything/zip", nil))
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("未登录应 401，得到 %d %s", resp.StatusCode, body)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/httpapi -run TestDownloadZip -v`
Expected: FAIL，404（路由不存在）

- [ ] **Step 3: 实现**

创建 `internal/httpapi/zip_handlers.go`：

```go
package httpapi

import (
	"archive/zip"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"strings"
)

// handleDownloadZip 把一条内容的全部附件流式打包成 zip。
// 用 Store 方式（不压缩）：ARM 小主机省 CPU，附件多为已压缩媒体。
func (s *Server) handleDownloadZip(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	it, err := s.store.Get(id)
	if err != nil {
		s.writeStoreErr(w, err)
		return
	}
	if len(it.Files) == 0 {
		writeError(w, http.StatusBadRequest, "no_files", "这条内容没有附件")
		return
	}

	h := w.Header()
	h.Set("Content-Type", "application/zip")
	h.Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", "lanbox-"+id+".zip"))
	h.Set("Cache-Control", "no-store")

	zw := zip.NewWriter(w)
	for _, f := range it.Files {
		disk, meta, err := s.store.FilePath(id, f.ID)
		if err != nil {
			s.log.Printf("warn: 打包时定位附件 %s/%s 失败: %v", id, f.ID, err)
			return
		}
		fh, err := os.Open(disk)
		if err != nil {
			s.log.Printf("warn: 打包时打开附件 %s 失败: %v", disk, err)
			return
		}
		name := meta.RelPath()
		if !safeZipName(name) {
			name = meta.Name
		}
		dst, err := zw.CreateHeader(&zip.FileHeader{
			Name:     name,
			Method:   zip.Store,
			Modified: it.CreatedAt,
		})
		if err != nil {
			fh.Close()
			s.log.Printf("warn: 写入 zip 条目 %s 失败: %v", name, err)
			return
		}
		if _, err := io.Copy(dst, fh); err != nil {
			fh.Close()
			s.log.Printf("warn: 复制附件 %s 到 zip 失败: %v", name, err)
			return
		}
		fh.Close()
	}
	if err := zw.Close(); err != nil {
		s.log.Printf("warn: 关闭 zip 失败: %v", err)
	}
	s.log.Printf("info: %s 打包下载内容 %s（%d 个附件）", userFrom(r).Username, id, len(it.Files))
}

// safeZipName 拒绝空名、绝对路径与含 ".." 段的条目名。
func safeZipName(name string) bool {
	if name == "" || strings.HasPrefix(name, "/") || strings.HasPrefix(name, `\`) {
		return false
	}
	for _, seg := range strings.Split(strings.ReplaceAll(name, `\`, "/"), "/") {
		if seg == ".." {
			return false
		}
	}
	return name != "" && name != path.Clean("/"+name)[1:]
}
```

> 最后一行是防御性检查：`path.Clean("/"+name)[1:]` 若与原名不同（例如含 `//` 或 `.` 段），视为不安全，回退到 `Name`。

`internal/httpapi/server.go` 的路由表在下载路由旁追加：

```go
	s.mux.HandleFunc("GET /api/items/{id}/zip", s.withAuth(s.handleDownloadZip))
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/httpapi -run TestDownloadZip -v`
Expected: PASS（3 个测试）

- [ ] **Step 5: 提交**

```bash
git add internal/httpapi/zip_handlers.go internal/httpapi/server.go internal/httpapi/api_test.go
git commit -m "feat(httpapi): 内容附件流式 zip 下载"
```

---

## Task 7: `internal/client` 基础（DTO、错误、登录、列表、取走、删除）

**Files:**
- Create: `internal/client/dto.go`
- Create: `internal/client/client.go`
- Test: `internal/client/client_test.go`

- [ ] **Step 1: 写失败测试**

创建 `internal/client/client_test.go`：

```go
package client_test

import (
	"context"
	"io"
	"log"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lanbox"
	"lanbox/internal/auth"
	"lanbox/internal/client"
	"lanbox/internal/config"
	"lanbox/internal/httpapi"
	"lanbox/internal/store"
)

type testEnv struct {
	url   string
	https *httptest.Server
	pw    string
	store *store.Store
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	cfg := config.Defaults()
	cfg.DataDir = t.TempDir()
	cfg.MaxFileSizeMB = 8
	cfg.MinFreeDiskMB = 0
	cfg.MaxFilesPerItem = 50
	cfg.MaxItemSizeMB = 64

	st, err := store.Open(filepath.Join(cfg.DataDir, "items"), store.Options{
		MaxFileBytes: cfg.MaxFileBytes(),
		MinFreeBytes: cfg.MinFreeBytes(),
		MaxTextBytes: cfg.MaxTextBytes,
		MaxFiles:     cfg.MaxFilesPerItem,
		MaxItemBytes: cfg.MaxItemBytes(),
	})
	if err != nil {
		t.Fatalf("打开 store 失败: %v", err)
	}
	am, err := auth.Open(filepath.Join(cfg.DataDir, "users.json"), time.Hour)
	if err != nil {
		t.Fatalf("打开 auth 失败: %v", err)
	}
	pw, _, err := am.BootstrapAdmin()
	if err != nil {
		t.Fatalf("初始化管理员失败: %v", err)
	}
	srv := httptest.NewTLSServer(httpapi.New(httpapi.Options{
		Cfg: cfg, Store: st, Auth: am, Web: lanbox.WebFS(), Log: log.New(io.Discard, "", 0),
	}))
	t.Cleanup(srv.Close)
	return &testEnv{url: srv.URL, https: srv, pw: pw, store: st}
}

func TestLoginAndMe(t *testing.T) {
	e := newTestEnv(t)
	httpc := e.https.Client()
	sess, err := client.Login(context.Background(), e.url, "admin", e.pw, httpc)
	if err != nil {
		t.Fatalf("登录失败: %v", err)
	}
	if sess.SID == "" || sess.Username != "admin" {
		t.Fatalf("会话内容不对: %+v", sess)
	}
	c, err := client.New(client.Options{BaseURL: sess.URL, SID: sess.SID, HTTP: httpc})
	if err != nil {
		t.Fatal(err)
	}
	me, err := c.Me(context.Background())
	if err != nil {
		t.Fatalf("获取身份失败: %v", err)
	}
	if me.Username != "admin" || !me.IsAdmin {
		t.Fatalf("身份不对: %+v", me)
	}
}

func TestLoginWrongPassword(t *testing.T) {
	e := newTestEnv(t)
	_, err := client.Login(context.Background(), e.url, "admin", "wrong-password", e.https.Client())
	var ae *client.Error
	if !errors.As(err, &ae) || ae.Status != 401 || ae.Code != "bad_credentials" {
		t.Fatalf("应返回 401 bad_credentials，得到 %v", err)
	}
}

func TestListGetTakenAndDelete(t *testing.T) {
	e := newTestEnv(t)
	c := loginClient(t, e)
	ctx := context.Background()

	seeded, err := e.store.Create(store.CreateInput{Text: "内网素材", CreatedBy: "admin"})
	if err != nil {
		t.Fatal(err)
	}
	seeded, err = e.store.AddFile(seeded.ID, "photos/a.jpg", 3, strings.NewReader("aaa"))
	if err != nil {
		t.Fatal(err)
	}

	res, err := c.List(ctx, client.ListQuery{Limit: 10})
	if err != nil {
		t.Fatalf("列表失败: %v", err)
	}
	if len(res.Items) != 1 || res.Items[0].ID != seeded.ID || res.Items[0].Text != "内网素材" {
		t.Fatalf("列表内容不对: %+v", res.Items)
	}
	if res.Items[0].TotalSize() != 3 {
		t.Fatalf("总大小应为 3，得到 %d", res.Items[0].TotalSize())
	}

	got, err := c.GetItem(ctx, seeded.ID)
	if err != nil {
		t.Fatalf("读取内容失败: %v", err)
	}
	if len(got.Files) != 1 || got.Files[0].Path != "photos/a.jpg" {
		t.Fatalf("附件路径不对: %+v", got.Files)
	}

	marked, err := c.Taken(ctx, seeded.ID, true)
	if err != nil {
		t.Fatalf("标记已取走失败: %v", err)
	}
	if !marked.Taken || marked.TakenBy != "admin" {
		t.Fatalf("标记结果不对: %+v", marked)
	}

	if err := c.DeleteFile(ctx, seeded.ID, seeded.Files[0].ID); err != nil {
		t.Fatalf("删除附件失败: %v", err)
	}
	if err := c.DeleteItem(ctx, seeded.ID); err != nil {
		t.Fatalf("删除失败: %v", err)
	}
	after, err := c.List(ctx, client.ListQuery{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(after.Items) != 0 {
		t.Fatalf("删除后列表应为空: %+v", after.Items)
	}
}

func loginClient(t *testing.T, e *testEnv) *client.Client {
	t.Helper()
	httpc := e.https.Client()
	sess, err := client.Login(context.Background(), e.url, "admin", e.pw, httpc)
	if err != nil {
		t.Fatalf("登录失败: %v", err)
	}
	c, err := client.New(client.Options{BaseURL: sess.URL, SID: sess.SID, HTTP: httpc})
	if err != nil {
		t.Fatal(err)
	}
	return c
}
```

> 本任务的测试直接用 `e.store` 造数据，不依赖 Task 8 的 `Put`；`loginClient` 用 `e.https.Client()`（它已信任 httptest 的证书）。

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/client -v`
Expected: 编译失败，`package client is not in std`

- [ ] **Step 3: 实现**

创建 `internal/client/dto.go`：

```go
// Package client 是 lanbox 服务的命令行客户端实现：
// 只认线上 JSON 契约，不依赖 internal/store 或 internal/httpapi。
package client

import "time"

// File 是一个附件的线上表示。
type File struct {
	ID        string `json:"id"`
	Path      string `json:"path"`
	Name      string `json:"name"`
	Size      int64  `json:"size"`
	SHA256    string `json:"sha256"`
	Downloads int64  `json:"downloads"`
}

// RelPath 返回附件的相对路径，兼容旧服务端（只有 name）。
func (f File) RelPath() string {
	if f.Path != "" {
		return f.Path
	}
	return f.Name
}

// Item 是一条内容。
type Item struct {
	ID        string     `json:"id"`
	CreatedAt time.Time  `json:"created_at"`
	CreatedBy string     `json:"created_by"`
	SourceIP  string     `json:"source_ip"`
	Text      string     `json:"text"`
	Taken     bool       `json:"taken"`
	TakenAt   *time.Time `json:"taken_at"`
	TakenBy   string     `json:"taken_by"`
	Files     []File     `json:"files"`
}

// User 是当前登录身份。
type User struct {
	Username string `json:"username"`
	IsAdmin  bool   `json:"is_admin"`
}

// ListResult 是列表分页结果。
type ListResult struct {
	Items      []Item `json:"items"`
	NextCursor string `json:"next_cursor"`
}

// TotalSize 返回全部附件字节数。
func (it Item) TotalSize() int64 {
	var n int64
	for _, f := range it.Files {
		n += f.Size
	}
	return n
}

// Session 是登录成功后本地保存的会话。
type Session struct {
	URL      string    `json:"url"`
	Username string    `json:"username"`
	SID      string    `json:"sid"`
	SavedAt  time.Time `json:"saved_at"`
}

// Error 是服务端返回的结构化错误。
type Error struct {
	Status  int
	Code    string
	Message string
}

func (e *Error) Error() string { return e.Message }
```

创建 `internal/client/client.go`：

```go
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// Options 构造一个已登录的客户端。
type Options struct {
	BaseURL string
	SID     string
	HTTP    *http.Client // 为空时用 http.DefaultClient
}

// Client 是 lanbox 的 HTTP 客户端。
type Client struct {
	base  *url.URL
	sid   string
	httpc *http.Client
}

// New 校验地址并构造客户端。
func New(o Options) (*Client, error) {
	base, err := url.Parse(strings.TrimRight(o.BaseURL, "/"))
	if err != nil || base.Scheme == "" || base.Host == "" {
		return nil, fmt.Errorf("服务器地址无效: %q", o.BaseURL)
	}
	if base.Scheme != "https" && base.Scheme != "http" {
		return nil, fmt.Errorf("服务器地址必须以 http:// 或 https:// 开头: %q", o.BaseURL)
	}
	if o.HTTP == nil {
		o.HTTP = http.DefaultClient
	}
	return &Client{base: base, sid: o.SID, httpc: o.HTTP}, nil
}

// BaseURL 返回规范化后的服务地址。
func (c *Client) BaseURL() string { return c.base.String() }

func (c *Client) do(ctx context.Context, method, p string, body io.Reader, ctype string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.base.String()+p, body)
	if err != nil {
		return nil, err
	}
	if c.sid != "" {
		req.Header.Set("Cookie", "sid="+c.sid)
	}
	if ctype != "" {
		req.Header.Set("Content-Type", ctype)
	}
	req.Header.Set("Accept", "application/json")
	resp, err := c.httpc.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		defer resp.Body.Close()
		return nil, parseError(resp)
	}
	return resp, nil
}

func parseError(resp *http.Response) error {
	var body struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	_ = json.Unmarshal(raw, &body)
	msg := body.Error.Message
	if msg == "" {
		msg = strings.TrimSpace(string(raw))
	}
	if msg == "" {
		msg = resp.Status
	}
	return &Error{Status: resp.StatusCode, Code: body.Error.Code, Message: msg}
}

// Login 用用户名密码换取会话令牌。
func Login(ctx context.Context, baseURL, username, password string, httpc *http.Client) (*Session, error) {
	if httpc == nil {
		httpc = http.DefaultClient
	}
	body, err := json.Marshal(map[string]string{"username": username, "password": password})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimRight(baseURL, "/")+"/api/login", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := httpc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, parseError(resp)
	}
	var me User
	if err := json.NewDecoder(resp.Body).Decode(&me); err != nil {
		return nil, fmt.Errorf("解析登录响应失败: %w", err)
	}
	sid := ""
	for _, ck := range resp.Cookies() {
		if ck.Name == "sid" {
			sid = ck.Value
		}
	}
	if sid == "" {
		return nil, fmt.Errorf("登录成功但服务端没有下发会话令牌")
	}
	return &Session{
		URL:      strings.TrimRight(baseURL, "/"),
		Username: me.Username,
		SID:      sid,
		SavedAt:  timeNow(),
	}, nil
}

// Me 返回当前登录身份。
func (c *Client) Me(ctx context.Context) (*User, error) {
	resp, err := c.do(ctx, http.MethodGet, "/api/me", nil, "")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var u User
	if err := json.NewDecoder(resp.Body).Decode(&u); err != nil {
		return nil, fmt.Errorf("解析身份响应失败: %w", err)
	}
	return &u, nil
}

// Logout 注销当前会话。
func (c *Client) Logout(ctx context.Context) error {
	resp, err := c.do(ctx, http.MethodPost, "/api/logout", nil, "")
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

// ListQuery 是列表参数。
type ListQuery struct {
	Limit  int
	Before string
	Search string
	Todo   bool
}

// List 列出内容。
func (c *Client) List(ctx context.Context, q ListQuery) (*ListResult, error) {
	v := url.Values{}
	if q.Limit > 0 {
		v.Set("limit", strconv.Itoa(q.Limit))
	}
	if q.Before != "" {
		v.Set("before", q.Before)
	}
	if q.Search != "" {
		v.Set("q", q.Search)
	}
	if q.Todo {
		v.Set("filter", "todo")
	} else {
		v.Set("filter", "all")
	}
	resp, err := c.do(ctx, http.MethodGet, "/api/items?"+v.Encode(), nil, "")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var res ListResult
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return nil, fmt.Errorf("解析列表响应失败: %w", err)
	}
	return &res, nil
}

// GetItem 读取单条内容。
func (c *Client) GetItem(ctx context.Context, id string) (*Item, error) {
	resp, err := c.do(ctx, http.MethodGet, "/api/items/"+url.PathEscape(id), nil, "")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var it Item
	if err := json.NewDecoder(resp.Body).Decode(&it); err != nil {
		return nil, fmt.Errorf("解析内容响应失败: %w", err)
	}
	return &it, nil
}

// Taken 设置或取消"已取走"标记。
func (c *Client) Taken(ctx context.Context, id string, taken bool) (*Item, error) {
	body, err := json.Marshal(map[string]bool{"taken": taken})
	if err != nil {
		return nil, err
	}
	resp, err := c.do(ctx, http.MethodPatch, "/api/items/"+url.PathEscape(id), bytes.NewReader(body), "application/json")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var it Item
	if err := json.NewDecoder(resp.Body).Decode(&it); err != nil {
		return nil, fmt.Errorf("解析内容响应失败: %w", err)
	}
	return &it, nil
}

// DeleteItem 删除整条内容。
func (c *Client) DeleteItem(ctx context.Context, id string) error {
	resp, err := c.do(ctx, http.MethodDelete, "/api/items/"+url.PathEscape(id), nil, "")
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

// DeleteFile 删除单个附件。
func (c *Client) DeleteFile(ctx context.Context, itemID, fileID string) error {
	resp, err := c.do(ctx, http.MethodDelete,
		"/api/items/"+url.PathEscape(itemID)+"/files/"+url.PathEscape(fileID), nil, "")
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}
```

在 `internal/client/client.go` 末尾加时间钩子（便于测试）：

```go
// timeNow 是便于测试替换的时间源。
var timeNow = time.Now
```

并在 import 里加上 `"time"`。

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/client -v`
Expected: PASS（`TestLoginAndMe`、`TestLoginWrongPassword`、`TestListGetTakenAndDelete`）

- [ ] **Step 5: 提交**

```bash
git add internal/client
git commit -m "feat(client): 登录/身份/列表/取走/删除"
```

---

## Task 8: 客户端上传 `Put`（`files:<路径>` + 进度）

**Files:**
- Create: `internal/client/put.go`
- Test: `internal/client/client_test.go`

- [ ] **Step 1: 写失败测试**

在 `internal/client/client_test.go` 末尾追加（Task 7 里的 `TestListGetTakenAndDelete` 保留，本任务新增以下用例）：

```go
func TestPutListTakenAndDelete(t *testing.T) {
	e := newTestEnv(t)
	c := loginClient(t, e)
	ctx := context.Background()

	var sent, total int64
	it, err := c.Put(ctx, "内网素材", []client.Upload{
		{Path: "photos/2024/a.jpg", Size: 3, Open: readerFunc("aaa")},
		{Path: "notes.txt", Size: 5, Open: readerFunc("hello")},
	}, func(s, t int64) { sent, total = s, t })
	if err != nil {
		t.Fatalf("上传失败: %v", err)
	}
	if it.Text != "内网素材" || len(it.Files) != 2 {
		t.Fatalf("返回内容不对: %+v", it)
	}
	if it.Files[0].Path != "photos/2024/a.jpg" || it.Files[1].Path != "notes.txt" {
		t.Fatalf("路径应保留: %+v", it.Files)
	}
	if sent != 8 || total <= 0 {
		t.Fatalf("进度回调不对: sent=%d total=%d", sent, total)
	}

	got, err := c.List(ctx, client.ListQuery{Limit: 10})
	if err != nil {
		t.Fatalf("列表失败: %v", err)
	}
	if len(got.Items) != 1 || got.Items[0].ID != it.ID {
		t.Fatalf("列表内容不对: %+v", got.Items)
	}
	if got.Items[0].TotalSize() != 8 {
		t.Fatalf("总大小应为 8，得到 %d", got.Items[0].TotalSize())
	}

	marked, err := c.Taken(ctx, it.ID, true)
	if err != nil {
		t.Fatalf("标记已取走失败: %v", err)
	}
	if !marked.Taken || marked.TakenBy != "admin" {
		t.Fatalf("标记结果不对: %+v", marked)
	}

	if err := c.DeleteItem(ctx, it.ID); err != nil {
		t.Fatalf("删除失败: %v", err)
	}
	after, err := c.List(ctx, client.ListQuery{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(after.Items) != 0 {
		t.Fatalf("删除后列表应为空: %+v", after.Items)
	}
}

func TestPutTextOnly(t *testing.T) {
	e := newTestEnv(t)
	c := loginClient(t, e)
	it, err := c.Put(context.Background(), "只有文字", nil, nil)
	if err != nil {
		t.Fatalf("上传失败: %v", err)
	}
	if it.Text != "只有文字" || len(it.Files) != 0 {
		t.Fatalf("返回内容不对: %+v", it)
	}
}

func TestPutEmptyIsRejected(t *testing.T) {
	e := newTestEnv(t)
	c := loginClient(t, e)
	_, err := c.Put(context.Background(), "  ", nil, nil)
	var ae *client.Error
	if !errors.As(err, &ae) || ae.Code != "empty" {
		t.Fatalf("空内容应返回 empty 错误，得到 %v", err)
	}
}

func readerFunc(s string) func() (io.ReadCloser, error) {
	return func() (io.ReadCloser, error) {
		return io.NopCloser(strings.NewReader(s)), nil
	}
}
```

同时在测试文件 import 中补 `"errors"`、`"strings"`。

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/client -run TestPut -v`
Expected: 编译失败，`c.Put undefined`

- [ ] **Step 3: 实现**

创建 `internal/client/put.go`：

```go
package client

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"path"
)

// Upload 是一个待上传的文件。
type Upload struct {
	Path string // 内容内的相对路径，如 "photos/a.jpg"
	Size int64
	Open func() (io.ReadCloser, error)
}

// Put 上传文字与文件，返回服务端创建的内容。
// onProgress 可为 nil；sent 只统计文件字节，total 是文件字节数 + 文本字节数。
func (c *Client) Put(ctx context.Context, text string, files []Upload, onProgress func(sent, total int64)) (*Item, error) {
	var total int64
	for _, f := range files {
		total += f.Size
	}
	total += int64(len(text))

	pr, pw := io.Pipe()
	mw := multipart.NewWriter(pw)
	go func() {
		err := writeMultipart(mw, text, files, onProgress, total)
		if cerr := mw.Close(); err == nil {
			err = cerr
		}
		pw.CloseWithError(err)
	}()

	resp, err := c.do(ctx, http.MethodPost, "/api/items", pr, mw.FormDataContentType())
	if err != nil {
		pr.CloseWithError(err)
		return nil, err
	}
	defer resp.Body.Close()
	var it Item
	if err := json.NewDecoder(resp.Body).Decode(&it); err != nil {
		return nil, fmt.Errorf("解析上传响应失败: %w", err)
	}
	return &it, nil
}

func writeMultipart(mw *multipart.Writer, text string, files []Upload, onProgress func(int64, int64), total int64) error {
	if text != "" {
		w, err := mw.CreateFormField("text")
		if err != nil {
			return err
		}
		if _, err := io.WriteString(w, text); err != nil {
			return err
		}
	}
	var sent int64
	buf := make([]byte, 64<<10)
	for _, f := range files {
		// Go 的 Part.FileName() 会丢弃目录信息，路径只能放在字段名里。
		w, err := mw.CreateFormFile("files:"+f.Path, path.Base(f.Path))
		if err != nil {
			return err
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		for {
			n, rerr := rc.Read(buf)
			if n > 0 {
				if _, werr := w.Write(buf[:n]); werr != nil {
					rc.Close()
					return werr
				}
				sent += int64(n)
				if onProgress != nil {
					onProgress(sent, total)
				}
			}
			if rerr == io.EOF {
				break
			}
			if rerr != nil {
				rc.Close()
				return rerr
			}
		}
		rc.Close()
	}
	return nil
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/client -v`
Expected: 全部 PASS

- [ ] **Step 5: 提交**

```bash
git add internal/client
git commit -m "feat(client): multipart 上传与进度回调"
```

---

## Task 9: 客户端下载（Range 续传、sha256、zip）

**Files:**
- Create: `internal/client/get.go`
- Test: `internal/client/client_test.go`

- [ ] **Step 1: 写失败测试**

在 `internal/client/client_test.go` 末尾追加：

```go
func TestDownloadFileRestoresPathAndVerifies(t *testing.T) {
	e := newTestEnv(t)
	c := loginClient(t, e)
	ctx := context.Background()
	it, err := c.Put(ctx, "", []client.Upload{
		{Path: "photos/a.txt", Size: 5, Open: readerFunc("hello")},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(t.TempDir(), "photos", "a.txt")
	if err := os.MkdirAll(filepath.Dir(dest), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := c.DownloadFile(ctx, it.ID, it.Files[0], client.DownloadOptions{Dest: dest}); err != nil {
		t.Fatalf("下载失败: %v", err)
	}
	raw, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != "hello" {
		t.Fatalf("内容不对: %q", raw)
	}
	if _, err := os.Stat(dest + ".part"); !os.IsNotExist(err) {
		t.Fatal("临时文件应已清理")
	}
}

func TestDownloadFileRefusesExistingWithoutForce(t *testing.T) {
	e := newTestEnv(t)
	c := loginClient(t, e)
	ctx := context.Background()
	it, err := c.Put(ctx, "", []client.Upload{{Path: "a.txt", Size: 1, Open: readerFunc("a")}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(t.TempDir(), "a.txt")
	if err := os.WriteFile(dest, []byte("old"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := c.DownloadFile(ctx, it.ID, it.Files[0], client.DownloadOptions{Dest: dest}); !errors.Is(err, client.ErrExists) {
		t.Fatalf("应返回 ErrExists，得到 %v", err)
	}
	if err := c.DownloadFile(ctx, it.ID, it.Files[0], client.DownloadOptions{Dest: dest, Force: true}); err != nil {
		t.Fatalf("force 覆盖失败: %v", err)
	}
	raw, _ := os.ReadFile(dest)
	if string(raw) != "a" {
		t.Fatalf("覆盖后内容不对: %q", raw)
	}
}

func TestDownloadFileResume(t *testing.T) {
	e := newTestEnv(t)
	c := loginClient(t, e)
	ctx := context.Background()
	body := strings.Repeat("0123456789", 100)
	it, err := c.Put(ctx, "", []client.Upload{
		{Path: "big.bin", Size: int64(len(body)), Open: readerFunc(body)},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(t.TempDir(), "big.bin")
	part := dest + ".part"
	if err := os.WriteFile(part, []byte(body[:400]), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := c.DownloadFile(ctx, it.ID, it.Files[0], client.DownloadOptions{Dest: dest, Resume: true}); err != nil {
		t.Fatalf("续传下载失败: %v", err)
	}
	raw, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != body {
		t.Fatalf("续传后内容长度 %d，期望 %d", len(raw), len(body))
	}
}

func TestDownloadZip(t *testing.T) {
	e := newTestEnv(t)
	c := loginClient(t, e)
	ctx := context.Background()
	it, err := c.Put(ctx, "", []client.Upload{
		{Path: "d/a.txt", Size: 1, Open: readerFunc("a")},
		{Path: "b.txt", Size: 1, Open: readerFunc("b")},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(t.TempDir(), "out.zip")
	if err := c.DownloadZip(ctx, it.ID, dest); err != nil {
		t.Fatalf("下载 zip 失败: %v", err)
	}
	zr, err := zip.OpenReader(dest)
	if err != nil {
		t.Fatalf("zip 损坏: %v", err)
	}
	defer zr.Close()
	names := []string{}
	for _, f := range zr.File {
		names = append(names, f.Name)
	}
	if len(names) != 2 || names[0] != "d/a.txt" || names[1] != "b.txt" {
		t.Fatalf("zip 条目不对: %+v", names)
	}
}
```

测试文件 import 补：`"archive/zip"`、`"os"`。

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/client -run TestDownload -v`
Expected: 编译失败，`undefined: ErrExists`、`c.DownloadFile undefined`

- [ ] **Step 3: 实现**

创建 `internal/client/get.go`：

```go
package client

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
)

// ErrExists 表示目标文件已存在且未指定 Force。
var ErrExists = errors.New("目标文件已存在")

// DownloadOptions 控制下载行为。
type DownloadOptions struct {
	Dest       string // 目标文件绝对或相对路径
	Force      bool   // 覆盖已存在的目标
	Resume     bool   // 用 Range 从 .part 的已有字节续传
	OnProgress func(done, total int64)
}

// DownloadFile 下载单个附件：先写 <dest>.part，校验 sha256 后 rename 到 dest。
func (c *Client) DownloadFile(ctx context.Context, itemID string, f File, o DownloadOptions) error {
	if !o.Force && !o.Resume {
		if _, err := os.Stat(o.Dest); err == nil {
			return ErrExists
		}
	}
	tmp := o.Dest + ".part"
	var offset int64
	if o.Resume {
		if st, err := os.Stat(tmp); err == nil {
			offset = st.Size()
		}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		c.base.String()+"/api/items/"+itemID+"/files/"+f.ID, nil)
	if err != nil {
		return err
	}
	if c.sid != "" {
		req.Header.Set("Cookie", "sid="+c.sid)
	}
	if offset > 0 {
		req.Header.Set("Range", "bytes="+strconv.FormatInt(offset, 10)+"-")
	}
	resp, err := c.httpc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		offset = 0 // 服务端不支持 Range，从头写
	case http.StatusPartialContent:
		// 继续追加
	case http.StatusRequestedRangeNotSatisfiable:
		// 已有内容长度不合法，从头开始
		offset = 0
		if err := os.Remove(tmp); err != nil && !os.IsNotExist(err) {
			return err
		}
	default:
		return parseError(resp)
	}

	flags := os.O_WRONLY | os.O_CREATE
	if offset > 0 {
		flags |= os.O_APPEND
	} else {
		flags |= os.O_TRUNC
	}
	if err := os.MkdirAll(filepath.Dir(o.Dest), 0o750); err != nil {
		return err
	}
	out, err := os.OpenFile(tmp, flags, 0o640)
	if err != nil {
		return err
	}
	hasher := sha256.New()
	// 续传时先喂入已有字节，才能算出全文件哈希。
	if offset > 0 {
		existing, err := os.Open(tmp)
		if err != nil {
			out.Close()
			return err
		}
		if _, err := io.Copy(hasher, existing); err != nil {
			existing.Close()
			out.Close()
			return err
		}
		existing.Close()
	}
	written, copyErr := io.Copy(io.MultiWriter(out, hasher), &progressReader{
		r: resp.Body, done: offset, total: f.Size, onProgress: o.OnProgress,
	})
	closeErr := out.Close()
	if copyErr != nil {
		return fmt.Errorf("写入文件失败: %w", copyErr)
	}
	if closeErr != nil {
		return fmt.Errorf("保存文件失败: %w", closeErr)
	}
	if f.Size > 0 && offset+written != f.Size {
		return fmt.Errorf("下载长度不符：期望 %d 字节，得到 %d", f.Size, offset+written)
	}
	if f.SHA256 != "" && hex.EncodeToString(hasher.Sum(nil)) != f.SHA256 {
		os.Remove(tmp)
		return fmt.Errorf("%s 校验失败：sha256 与服务器不一致", f.RelPath())
	}
	return os.Rename(tmp, o.Dest)
}

// progressReader 在读取时回报进度。
type progressReader struct {
	r          io.Reader
	done       int64
	total      int64
	onProgress func(done, total int64)
}

func (p *progressReader) Read(b []byte) (int, error) {
	n, err := p.r.Read(b)
	if n > 0 {
		p.done += int64(n)
		if p.onProgress != nil {
			p.onProgress(p.done, p.total)
		}
	}
	return n, err
}

// DownloadZip 下载整条内容的 zip 包（不支持续传）。
func (c *Client) DownloadZip(ctx context.Context, itemID, dest string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		c.base.String()+"/api/items/"+itemID+"/zip", nil)
	if err != nil {
		return err
	}
	if c.sid != "" {
		req.Header.Set("Cookie", "sid="+c.sid)
	}
	resp, err := c.httpc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return parseError(resp)
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o750); err != nil {
		return err
	}
	tmp := dest + ".part"
	out, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o640)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, resp.Body); err != nil {
		out.Close()
		os.Remove(tmp)
		return fmt.Errorf("写入 zip 失败: %w", err)
	}
	if err := out.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, dest)
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/client -v`
Expected: 全部 PASS

- [ ] **Step 5: 提交**

```bash
git add internal/client
git commit -m "feat(client): 附件下载（续传/校验）与 zip 下载"
```

---

## Task 10: CLI 骨架、会话文件与密码读取

**Files:**
- Create: `cmd/lanbox-cli/main.go`
- Create: `cmd/lanbox-cli/session.go`
- Create: `cmd/lanbox-cli/password.go`
- Create: `cmd/lanbox-cli/term_linux.go`
- Create: `cmd/lanbox-cli/term_other.go`
- Test: `cmd/lanbox-cli/session_test.go`

- [ ] **Step 1: 写失败测试**

创建 `cmd/lanbox-cli/session_test.go`：

```go
package main

import (
	"os"
	"path/filepath"
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
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./cmd/lanbox-cli -v`
Expected: 编译失败，`undefined: saveSession`

- [ ] **Step 3: 实现**

创建 `cmd/lanbox-cli/session.go`：

```go
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"lanbox/internal/client"
)

// configDir 返回配置目录（$XDG_CONFIG_HOME/lanbox 或 ~/.config/lanbox）。
func configDir() (string, error) {
	if d := os.Getenv("XDG_CONFIG_HOME"); d != "" {
		return filepath.Join(d, "lanbox"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("无法确定用户主目录: %w", err)
	}
	return filepath.Join(home, ".config", "lanbox"), nil
}

func sessionPath() (string, error) {
	dir, err := configDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "session.json"), nil
}

// saveSession 以 0600 写入会话文件，目录 0700。
func saveSession(s *client.Session) error {
	path, err := sessionPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("创建配置目录失败: %w", err)
	}
	raw, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, append(raw, '\n'), 0o600); err != nil {
		return fmt.Errorf("写入会话失败: %w", err)
	}
	// WriteFile 对已存在文件不改权限，这里强制纠正。
	return os.Chmod(path, 0o600)
}

func loadSession() (*client.Session, error) {
	path, err := sessionPath()
	if err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("尚未登录，请先运行 lanbox-cli login -s <地址>")
		}
		return nil, fmt.Errorf("读取会话失败: %w", err)
	}
	var s client.Session
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, fmt.Errorf("会话文件已损坏，请重新登录: %w", err)
	}
	if s.SID == "" || s.URL == "" {
		return nil, fmt.Errorf("会话文件不完整，请重新登录")
	}
	return &s, nil
}

func removeSession() error {
	path, err := sessionPath()
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("删除会话失败: %w", err)
	}
	return nil
}
```

创建 `cmd/lanbox-cli/password.go`：

```go
package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"
)

// readPassword 按优先级取密码：LANBOX_PASSWORD → 管道 stdin → 终端无回显提示。
func readPassword(stdin *os.File, stderr io.Writer) (string, error) {
	if pw, ok := os.LookupEnv("LANBOX_PASSWORD"); ok && pw != "" {
		return pw, nil
	}
	if !isTerminal(stdin.Fd()) {
		line, err := bufio.NewReader(stdin).ReadString('\n')
		if err != nil && line == "" {
			return "", fmt.Errorf("从标准输入读取密码失败: %w", err)
		}
		return strings.TrimRight(line, "\r\n"), nil
	}
	fmt.Fprint(stderr, "密码: ")
	pw, err := readPasswordNoEcho(stdin)
	fmt.Fprintln(stderr)
	if err != nil {
		return "", err
	}
	return strings.TrimRight(pw, "\r\n"), nil
}
```

创建 `cmd/lanbox-cli/term_linux.go`：

```go
//go:build linux

package main

import (
	"bufio"
	"os"
	"syscall"
	"unsafe"
)

// isTerminal 判断 fd 是否接在终端上。
func isTerminal(fd uintptr) bool {
	var t syscall.Termios
	_, _, errno := syscall.Syscall6(syscall.SYS_IOCTL, fd,
		uintptr(syscall.TCGETS), uintptr(unsafe.Pointer(&t)), 0, 0, 0)
	return errno == 0
}

// readPasswordNoEcho 关闭回显读取一行，结束后恢复原状态。
func readPasswordNoEcho(f *os.File) (string, error) {
	fd := f.Fd()
	var old syscall.Termios
	if _, _, errno := syscall.Syscall6(syscall.SYS_IOCTL, fd,
		uintptr(syscall.TCGETS), uintptr(unsafe.Pointer(&old)), 0, 0, 0); errno != 0 {
		return bufio.NewReader(f).ReadString('\n')
	}
	noEcho := old
	noEcho.Lflag &^= syscall.ECHO
	if _, _, errno := syscall.Syscall6(syscall.SYS_IOCTL, fd,
		uintptr(syscall.TCSETS), uintptr(unsafe.Pointer(&noEcho)), 0, 0, 0); errno != 0 {
		return bufio.NewReader(f).ReadString('\n')
	}
	defer func() {
		syscall.Syscall6(syscall.SYS_IOCTL, fd,
			uintptr(syscall.TCSETS), uintptr(unsafe.Pointer(&old)), 0, 0, 0)
	}()
	return bufio.NewReader(f).ReadString('\n')
}
```

创建 `cmd/lanbox-cli/term_other.go`：

```go
//go:build !linux

package main

import (
	"bufio"
	"fmt"
	"os"
)

func isTerminal(fd uintptr) bool { return false }

func readPasswordNoEcho(f *os.File) (string, error) {
	fmt.Fprintln(os.Stderr, "（当前平台不支持无回显输入，密码会显示出来）")
	return bufio.NewReader(f).ReadString('\n')
}
```

创建 `cmd/lanbox-cli/main.go`（本任务先实现 `login` / `logout` / `whoami`，其余子命令在 Task 11 接入）：

```go
// lanbox-cli 是 lanbox 的命令行客户端。
package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"lanbox/internal/client"
)

// 退出码约定。
const (
	exitOK      = 0
	exitFail    = 1
	exitUsage   = 2
	exitNetwork = 3
	exitAuth    = 4
)

// errUsage 表示参数用法错误。
var errUsage = errors.New("用法错误")

// globals 是全部子命令共享的选项。
type globals struct {
	server   string
	json     bool
	insecure bool
	caFile   string
	verbose  bool
	stdout   io.Writer
	stderr   io.Writer
}

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(args []string, stdout, stderr io.Writer) int {
	g := &globals{stdout: stdout, stderr: stderr}
	fs := flag.NewFlagSet("lanbox-cli", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&g.server, "s", "", "服务地址，如 https://192.168.1.9:8443")
	fs.BoolVar(&g.json, "json", false, "输出机器可读 JSON")
	fs.BoolVar(&g.insecure, "insecure", false, "跳过 TLS 证书校验（不安全）")
	fs.StringVar(&g.caFile, "ca", "", "信任指定的 PEM 证书文件")
	fs.BoolVar(&g.verbose, "v", false, "打印请求细节")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	rest := fs.Args()
	if len(rest) == 0 {
		usage(stderr)
		return exitUsage
	}

	cmd, cmdArgs := rest[0], rest[1:]
	ctx := context.Background()
	var err error
	switch cmd {
	case "login":
		err = cmdLogin(ctx, g, cmdArgs, os.Stdin)
	case "logout":
		err = cmdLogout(ctx, g)
	case "whoami":
		err = cmdWhoami(ctx, g)
	case "help", "-h", "--help":
		usage(stdout)
		return exitOK
	default:
		fmt.Fprintf(stderr, "未知命令: %s\n", cmd)
		usage(stderr)
		return exitUsage
	}
	if err != nil {
		if errors.Is(err, errUsage) {
			fmt.Fprintln(stderr, "错误:", err)
			return exitUsage
		}
		fmt.Fprintln(stderr, "错误:", err)
		var ae *client.Error
		if errors.As(err, &ae) && ae.Status == http.StatusUnauthorized {
			fmt.Fprintln(stderr, "提示: 会话已失效，请重新运行 lanbox-cli login")
			return exitAuth
		}
		if isNetworkErr(err) {
			return exitNetwork
		}
		return exitFail
	}
	return exitOK
}

func usage(w io.Writer) {
	fmt.Fprint(w, `用法: lanbox-cli [全局选项] <命令> [参数]

全局选项:
  -s URL        服务地址（也可用 LANBOX_URL）
  -json         输出 JSON
  -insecure     跳过 TLS 校验（不推荐）
  -ca FILE      信任指定 PEM 证书
  -v            打印请求细节

命令:
  login [-u 用户名]        登录并保存会话
  logout                   注销并删除本地会话
  whoami                   显示当前身份
  put [-m 文本] [-r] 路径…  发布文字/文件/目录（- 表示从 stdin 读文本）
  ls [-n 数量] [-q 关键词] [--todo] [-o]  列出内容（-o 同时打印附件）
  files ID                 列出某条内容的附件
  get ID [-o 目录] [--zip 文件] [--force] [--resume]
  taken ID [--undo]        标记/取消已取走
  rm ID                    删除整条内容
  rm-file ID FID           删除单个附件
`)
}

// newClient 读取会话并按全局选项组装客户端。
func newClient(g *globals) (*client.Client, *client.Session, error) {
	sess, err := loadSession()
	if err != nil {
		return nil, nil, err
	}
	if g.server != "" && strings.TrimRight(g.server, "/") != sess.URL {
		return nil, nil, fmt.Errorf("会话属于 %s，请先运行 lanbox-cli login -s %s", sess.URL, strings.TrimRight(g.server, "/"))
	}
	httpc, err := httpClient(g)
	if err != nil {
		return nil, nil, err
	}
	c, err := client.New(client.Options{BaseURL: sess.URL, SID: sess.SID, HTTP: httpc})
	if err != nil {
		return nil, nil, err
	}
	return c, sess, nil
}

// httpClient 按 -insecure / -ca 构造带超时的 HTTP 客户端。
func httpClient(g *globals) (*http.Client, error) {
	trans := http.DefaultTransport.(*http.Transport).Clone()
	if g.insecure || g.caFile != "" {
		tlsCfg := &tls.Config{MinVersion: tls.VersionTLS12}
		if g.insecure {
			tlsCfg.InsecureSkipVerify = true
			fmt.Fprintln(g.stderr, "警告: 已跳过 TLS 证书校验（-insecure）")
		}
		if g.caFile != "" {
			pem, err := os.ReadFile(g.caFile)
			if err != nil {
				return nil, fmt.Errorf("读取证书 %s 失败: %w", g.caFile, err)
			}
			pool, err := x509.SystemCertPool()
			if err != nil || pool == nil {
				pool = x509.NewCertPool()
			}
			if !pool.AppendCertsFromPEM(pem) {
				return nil, fmt.Errorf("证书文件 %s 里没有可用的 PEM 证书", g.caFile)
			}
			tlsCfg.RootCAs = pool
		}
		trans.TLSClientConfig = tlsCfg
	}
	return &http.Client{Transport: trans}, nil
}

// isNetworkErr 判断是否为连接层面的错误。
func isNetworkErr(err error) bool {
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return true
	}
	var oe *net.OpError
	return errors.As(err, &oe)
}

func cmdLogin(ctx context.Context, g *globals, args []string, stdin *os.File) error {
	fs := flag.NewFlagSet("login", flag.ContinueOnError)
	fs.SetOutput(g.stderr)
	user := fs.String("u", "admin", "用户名")
	if err := fs.Parse(args); err != nil {
		return errUsage
	}
	server := g.server
	if server == "" {
		server = os.Getenv("LANBOX_URL")
	}
	if server == "" {
		return fmt.Errorf("%w: 请用 -s 指定服务地址，例如 -s https://192.168.1.9:8443", errUsage)
	}
	pw, err := readPassword(stdin, g.stderr)
	if err != nil {
		return err
	}
	httpc, err := httpClient(g)
	if err != nil {
		return err
	}
	sess, err := client.Login(ctx, server, *user, pw, httpc)
	if err != nil {
		return err
	}
	if err := saveSession(sess); err != nil {
		return err
	}
	if g.json {
		return printJSON(g.stdout, map[string]string{"url": sess.URL, "username": sess.Username})
	}
	fmt.Fprintf(g.stdout, "已登录 %s（%s）\n", sess.Username, sess.URL)
	return nil
}

func cmdLogout(ctx context.Context, g *globals) error {
	c, sess, err := newClient(g)
	if err != nil {
		return err
	}
	if err := c.Logout(ctx); err != nil {
		fmt.Fprintf(g.stderr, "提示: 服务端注销失败（%v），仍将删除本地会话\n", err)
	}
	if err := removeSession(); err != nil {
		return err
	}
	fmt.Fprintf(g.stdout, "已退出 %s（%s）\n", sess.Username, sess.URL)
	return nil
}

func cmdWhoami(ctx context.Context, g *globals) error {
	c, sess, err := newClient(g)
	if err != nil {
		return err
	}
	me, err := c.Me(ctx)
	if err != nil {
		return err
	}
	if g.json {
		return printJSON(g.stdout, map[string]any{
			"url": sess.URL, "username": me.Username, "is_admin": me.IsAdmin,
		})
	}
	role := "普通用户"
	if me.IsAdmin {
		role = "管理员"
	}
	fmt.Fprintf(g.stdout, "%s（%s）@ %s\n", me.Username, role, sess.URL)
	return nil
}

// printJSON 输出单行 JSON。
func printJSON(w io.Writer, v any) error {
	return json.NewEncoder(w).Encode(v)
}
```

> `main.go` 的 import 需要包含：`context`、`crypto/tls`、`crypto/x509`、`encoding/json`、`errors`、`flag`、`fmt`、`io`、`net`、`net/http`、`os`、`strings`、`lanbox/internal/client`。

`printJSON` 使用 `encoding/json`（已在上面给出最终版本，无需再改）。

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./cmd/lanbox-cli -v && go build ./...`
Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add cmd/lanbox-cli
git commit -m "feat(cli): 骨架、会话文件与密码读取"
```

---

## Task 11: CLI 子命令（put / ls / files / get / taken / rm / rm-file）

**Files:**
- Create: `cmd/lanbox-cli/putwalk.go`
- Create: `cmd/lanbox-cli/render.go`
- Create: `cmd/lanbox-cli/progress.go`
- Create: `cmd/lanbox-cli/cmd_put.go`
- Create: `cmd/lanbox-cli/cmd_ls.go`
- Create: `cmd/lanbox-cli/cmd_get.go`
- Create: `cmd/lanbox-cli/cmd_misc.go`
- Modify: `cmd/lanbox-cli/main.go`（分发）
- Test: `cmd/lanbox-cli/putwalk_test.go`、`cmd/lanbox-cli/render_test.go`

- [ ] **Step 1: 写失败测试**

创建 `cmd/lanbox-cli/putwalk_test.go`：

```go
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
```

创建 `cmd/lanbox-cli/render_test.go`：

```go
package main

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"lanbox/internal/client"
)

func TestRenderItemsTable(t *testing.T) {
	items := []client.Item{{
		ID:        "0001-ab",
		CreatedAt: time.Date(2026, 11, 26, 10, 0, 0, 0, time.Local),
		CreatedBy: "admin",
		Text:      "第一行\n第二行",
		Taken:     true,
		Files:     []client.File{{ID: "f1", Path: "a/b.txt", Size: 2048}},
	}}
	var buf bytes.Buffer
	if err := renderItems(&buf, items); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{"0001-ab", "admin", "2.0 KB", "已取走", "第一行 第二行"} {
		if !strings.Contains(out, want) {
			t.Fatalf("输出缺少 %q:\n%s", want, out)
		}
	}
}

func TestSummarizeTrimsAndTruncates(t *testing.T) {
	if got := summarize("  a\nb  "); got != "a b" {
		t.Fatalf("应压成单行，得到 %q", got)
	}
	long := strings.Repeat("字", 60)
	got := summarize(long)
	if len([]rune(got)) != 40 || !strings.HasSuffix(got, "…") {
		t.Fatalf("应截断到 40 字并加省略号，得到 %q", got)
	}
}

func TestFmtBytes(t *testing.T) {
	cases := map[int64]string{0: "0 B", 999: "999 B", 1024: "1.0 KB", 2048: "2.0 KB", 3 << 20: "3.0 MB"}
	for in, want := range cases {
		if got := fmtBytes(in); got != want {
			t.Fatalf("fmtBytes(%d)=%q，期望 %q", in, got, want)
		}
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./cmd/lanbox-cli -v`
Expected: 编译失败，`undefined: collectUploads`

- [ ] **Step 3: 实现**

创建 `cmd/lanbox-cli/putwalk.go`：

```go
package main

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"lanbox/internal/client"
)

// collectUploads 把命令行路径展开成待上传列表。
// 目录必须显式 -r；目录内跳过符号链接与空目录，路径保留 "目录名/内部路径"。
func collectUploads(paths []string, recursive bool) ([]client.Upload, error) {
	var out []client.Upload
	for _, p := range paths {
		abs, err := filepath.Abs(p)
		if err != nil {
			return nil, fmt.Errorf("解析路径 %s 失败: %w", p, err)
		}
		st, err := os.Lstat(abs)
		if err != nil {
			return nil, fmt.Errorf("读取 %s 失败: %w", p, err)
		}
		if !st.IsDir() {
			if st.Mode()&os.ModeSymlink != 0 {
				return nil, fmt.Errorf("%s 是符号链接，请直接指定目标文件", p)
			}
			out = append(out, newUpload(filepath.Base(abs), abs, st.Size()))
			continue
		}
		if !recursive {
			return nil, fmt.Errorf("%w: %s 是目录，需要 -r 才能上传目录", errUsage, p)
		}
		base := filepath.Dir(abs)
		err = filepath.WalkDir(abs, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				return nil
			}
			if d.Type()&fs.ModeSymlink != 0 {
				return nil
			}
			info, err := d.Info()
			if err != nil {
				return err
			}
			rel, err := filepath.Rel(base, path)
			if err != nil {
				return err
			}
			out = append(out, newUpload(filepath.ToSlash(rel), path, info.Size()))
			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("遍历目录 %s 失败: %w", p, err)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

func newUpload(relPath, diskPath string, size int64) client.Upload {
	return client.Upload{
		Path: strings.TrimPrefix(relPath, "/"),
		Size: size,
		Open: func() (io.ReadCloser, error) { return os.Open(diskPath) },
	}
}
```

创建 `cmd/lanbox-cli/render.go`：

```go
package main

import (
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"lanbox/internal/client"
)

// summarize 把多行文本压成单行并截断到 40 字。
func summarize(text string) string {
	flat := strings.Join(strings.Fields(text), " ")
	runes := []rune(flat)
	if len(runes) <= 40 {
		return flat
	}
	return string(runes[:40]) + "…"
}

// fmtBytes 输出人类可读的字节数。
func fmtBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGT"[exp])
}

// renderItems 输出内容列表表格。
func renderItems(w io.Writer, items []client.Item) error {
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\t时间\t发布者\t附件\t状态\t文本")
	for _, it := range items {
		status := "未取走"
		if it.Taken {
			status = "已取走"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%d 个 / %s\t%s\t%s\n",
			it.ID, it.CreatedAt.Local().Format("01-02 15:04"), it.CreatedBy,
			len(it.Files), fmtBytes(it.TotalSize()), status, summarize(it.Text))
	}
	return tw.Flush()
}

// renderFiles 输出某条内容的附件表格。
func renderFiles(w io.Writer, files []client.File) error {
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "FID\t路径\t大小\t下载")
	for _, f := range files {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%d\n", f.ID, f.RelPath(), fmtBytes(f.Size), f.Downloads)
	}
	return tw.Flush()
}
```

创建 `cmd/lanbox-cli/progress.go`（上传进度渲染，`put` 用）：

```go
package main

import (
	"fmt"
	"io"
)

// progressPrinter 在 TTY 上就地刷新上传进度，非 TTY 时每 10% 报一次。
func progressPrinter(stderr io.Writer, isTTY bool) func(sent, total int64) {
	lastPct := int64(-10)
	return func(sent, total int64) {
		if total <= 0 {
			return
		}
		pct := sent * 100 / total
		if isTTY {
			fmt.Fprintf(stderr, "\r已发送 %s / %s（%d%%）", fmtBytes(sent), fmtBytes(total), pct)
			return
		}
		if pct-lastPct >= 10 {
			lastPct = pct
			fmt.Fprintf(stderr, "已发送 %d%%\n", pct)
		}
	}
}

// finishProgress 收尾，避免 TTY 上的进度行残留。
func finishProgress(stderr io.Writer, isTTY bool) {
	if isTTY {
		fmt.Fprint(stderr, "\r\x1b[K")
	}
}
```

创建 `cmd/lanbox-cli/cmd_put.go`：

```go
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"

	"lanbox/internal/client"
)

func cmdPut(ctx context.Context, g *globals, args []string, stdin *os.File) error {
	fs := flag.NewFlagSet("put", flag.ContinueOnError)
	fs.SetOutput(g.stderr)
	text := fs.String("m", "", "随附件一起发布的文字")
	recursive := fs.Bool("r", false, "递归上传目录")
	if err := fs.Parse(args); err != nil {
		return errUsage
	}
	paths := fs.Args()

	body := *text
	var files []client.Upload
	filePaths := make([]string, 0, len(paths))
	for _, p := range paths {
		if p == "-" {
			raw, err := io.ReadAll(io.LimitReader(stdin, 1<<20))
			if err != nil {
				return fmt.Errorf("读取标准输入失败: %w", err)
			}
			if body == "" {
				body = string(raw)
			}
			continue
		}
		filePaths = append(filePaths, p)
	}
	if len(filePaths) > 0 {
		ups, err := collectUploads(filePaths, *recursive)
		if err != nil {
			return err
		}
		files = ups
	}
	if body == "" && len(files) == 0 {
		return fmt.Errorf("%w: 请提供文字（-m 或 -）或文件路径", errUsage)
	}

	c, _, err := newClient(g)
	if err != nil {
		return err
	}
	tty := isTerminal(os.Stderr.Fd())
	it, err := c.Put(ctx, body, files, progressPrinter(g.stderr, tty))
	finishProgress(g.stderr, tty)
	if err != nil {
		return err
	}
	if g.json {
		return printJSON(g.stdout, it)
	}
	fmt.Fprintf(g.stdout, "已发布 %s（%d 个附件，共 %s）\n", it.ID, len(it.Files), fmtBytes(it.TotalSize()))
	return nil
}
```

创建 `cmd/lanbox-cli/cmd_ls.go`：

```go
package main

import (
	"context"
	"flag"
	"fmt"

	"lanbox/internal/client"
)

func cmdLs(ctx context.Context, g *globals, args []string) error {
	fs := flag.NewFlagSet("ls", flag.ContinueOnError)
	fs.SetOutput(g.stderr)
	limit := fs.Int("n", 20, "返回条数")
	search := fs.String("q", "", "按文本/文件名搜索")
	todo := fs.Bool("todo", false, "只看未取走")
	withFiles := fs.Bool("o", false, "同时打印附件")
	if err := fs.Parse(args); err != nil {
		return errUsage
	}
	c, _, err := newClient(g)
	if err != nil {
		return err
	}
	res, err := c.List(ctx, client.ListQuery{Limit: *limit, Search: *search, Todo: *todo})
	if err != nil {
		return err
	}
	if g.json {
		if *withFiles {
			return printJSON(g.stdout, res)
		}
		brief := &client.ListResult{NextCursor: res.NextCursor}
		for _, it := range res.Items {
			it.Files = nil
			brief.Items = append(brief.Items, it)
		}
		return printJSON(g.stdout, brief)
	}
	if err := renderItems(g.stdout, res.Items); err != nil {
		return err
	}
	if *withFiles {
		for _, it := range res.Items {
			if len(it.Files) == 0 {
				continue
			}
			fmt.Fprintf(g.stdout, "\n%s:\n", it.ID)
			if err := renderFiles(g.stdout, it.Files); err != nil {
				return err
			}
		}
	}
	return nil
}

func cmdFiles(ctx context.Context, g *globals, args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("%w: 用法 lanbox-cli files <ID>", errUsage)
	}
	c, _, err := newClient(g)
	if err != nil {
		return err
	}
	it, err := c.GetItem(ctx, args[0])
	if err != nil {
		return err
	}
	if g.json {
		return printJSON(g.stdout, it.Files)
	}
	if len(it.Files) == 0 {
		fmt.Fprintln(g.stdout, "这条内容没有附件")
		return nil
	}
	return renderFiles(g.stdout, it.Files)
}
```

创建 `cmd/lanbox-cli/cmd_get.go`：

```go
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"lanbox/internal/client"
)

func cmdGet(ctx context.Context, g *globals, args []string) error {
	fs := flag.NewFlagSet("get", flag.ContinueOnError)
	fs.SetOutput(g.stderr)
	out := fs.String("o", ".", "下载到哪个目录")
	zipPath := fs.String("zip", "", "下载整条内容为 zip 文件")
	force := fs.Bool("force", false, "覆盖已存在的文件")
	resume := fs.Bool("resume", false, "单文件断点续传")
	if err := fs.Parse(args); err != nil {
		return errUsage
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("%w: 用法 lanbox-cli get <ID> [-o 目录] [--zip 文件]", errUsage)
	}
	if *zipPath != "" && *resume {
		return fmt.Errorf("%w: --zip 与 --resume 不能同时使用", errUsage)
	}
	c, _, err := newClient(g)
	if err != nil {
		return err
	}
	it, err := c.GetItem(ctx, fs.Arg(0))
	if err != nil {
		return err
	}

	if *zipPath != "" {
		if len(it.Files) == 0 {
			return fmt.Errorf("这条内容没有附件，无法打包")
		}
		if err := c.DownloadZip(ctx, it.ID, *zipPath); err != nil {
			return err
		}
		fmt.Fprintf(g.stdout, "已保存 %s（%d 个附件）\n", *zipPath, len(it.Files))
		return nil
	}

	if len(it.Files) == 0 {
		fmt.Fprint(g.stdout, it.Text)
		if it.Text != "" && !it.Taken {
			fmt.Fprintln(g.stdout)
		}
		return nil
	}

	tty := isTerminal(os.Stderr.Fd())
	single := len(it.Files) == 1
	for _, f := range it.Files {
		rel := f.RelPath()
		if single {
			rel = filepath.Base(rel)
		}
		dest := filepath.Join(*out, filepath.FromSlash(rel))
		opts := client.DownloadOptions{
			Dest:   dest,
			Force:  *force,
			Resume: *resume && single,
			OnProgress: func(done, total int64) {
				if tty && total > 0 {
					fmt.Fprintf(g.stderr, "\r%s：%d%%", rel, done*100/total)
				}
			},
		}
		err := c.DownloadFile(ctx, it.ID, f, opts)
		if errors.Is(err, client.ErrExists) {
			fmt.Fprintf(g.stdout, "跳过已存在：%s\n", dest)
			continue
		}
		if tty {
			fmt.Fprint(g.stderr, "\r\x1b[K")
		}
		if err != nil {
			return err
		}
		fmt.Fprintf(g.stdout, "已下载 %s（%s）\n", dest, fmtBytes(f.Size))
	}
	if *resume && !single {
		fmt.Fprintln(g.stderr, "提示: --resume 只对单附件内容生效")
	}
	return nil
}
```

创建 `cmd/lanbox-cli/cmd_misc.go`：

```go
package main

import (
	"context"
	"flag"
	"fmt"
)

func cmdTaken(ctx context.Context, g *globals, args []string) error {
	fs := flag.NewFlagSet("taken", flag.ContinueOnError)
	fs.SetOutput(g.stderr)
	undo := fs.Bool("undo", false, "取消已取走标记")
	if err := fs.Parse(args); err != nil {
		return errUsage
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("%w: 用法 lanbox-cli taken <ID> [--undo]", errUsage)
	}
	c, _, err := newClient(g)
	if err != nil {
		return err
	}
	it, err := c.Taken(ctx, fs.Arg(0), !*undo)
	if err != nil {
		return err
	}
	if g.json {
		return printJSON(g.stdout, it)
	}
	if it.Taken {
		fmt.Fprintf(g.stdout, "%s 已标记为已取走（%s）\n", it.ID, it.TakenBy)
	} else {
		fmt.Fprintf(g.stdout, "%s 已取消已取走标记\n", it.ID)
	}
	return nil
}

func cmdRm(ctx context.Context, g *globals, args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("%w: 用法 lanbox-cli rm <ID>", errUsage)
	}
	c, _, err := newClient(g)
	if err != nil {
		return err
	}
	if err := c.DeleteItem(ctx, args[0]); err != nil {
		return err
	}
	fmt.Fprintf(g.stdout, "已删除 %s\n", args[0])
	return nil
}

func cmdRmFile(ctx context.Context, g *globals, args []string) error {
	if len(args) != 2 {
		return fmt.Errorf("%w: 用法 lanbox-cli rm-file <ID> <FID>", errUsage)
	}
	c, _, err := newClient(g)
	if err != nil {
		return err
	}
	if err := c.DeleteFile(ctx, args[0], args[1]); err != nil {
		return err
	}
	fmt.Fprintf(g.stdout, "已删除附件 %s\n", args[1])
	return nil
}
```

`cmd/lanbox-cli/main.go` 的 `switch` 补上：

```go
	case "put":
		err = cmdPut(ctx, g, cmdArgs, os.Stdin)
	case "ls":
		err = cmdLs(ctx, g, cmdArgs)
	case "files":
		err = cmdFiles(ctx, g, cmdArgs)
	case "get":
		err = cmdGet(ctx, g, cmdArgs)
	case "taken":
		err = cmdTaken(ctx, g, cmdArgs)
	case "rm":
		err = cmdRm(ctx, g, cmdArgs)
	case "rm-file":
		err = cmdRmFile(ctx, g, cmdArgs)
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./cmd/lanbox-cli -v && go build ./... && go vet ./...`
Expected: 全部 PASS，无 vet 警告

- [ ] **Step 5: 提交**

```bash
git add cmd/lanbox-cli
git commit -m "feat(cli): put/ls/files/get/taken/rm 子命令"
```

---

## Task 12: 网页端目录上传、树状展示与 zip 下载

**Files:**
- Modify: `web/api.js:56-80`（`uploadItem` 字段名）
- Modify: `web/main.js`（拖拽目录、选择文件夹、文件收集）
- Modify: `web/render.js:30-60`（附件按路径展示 + zip 按钮）
- Modify: `web/index.html`（工具栏按钮与隐藏 input）
- Modify: `web/style.css`（目录缩进样式）

- [ ] **Step 1: 手动验收基线（先跑起来）**

```bash
go build -o lanbox ./cmd/lanbox && ./lanbox -config /dev/null
```
记录：单文件上传后卡片正常、单文件下载仍是原链接。

- [ ] **Step 2: 改字段名与文件收集**

`web/api.js` 的 `uploadItem` 改为（唯一改动是循环里那一行）：

```js
// uploadItem 用 XHR 上传以获得真实进度（fetch 没有上传进度）。
export function uploadItem({ text, files, onProgress }) {
  return new Promise((resolve, reject) => {
    const fd = new FormData();
    if (text) fd.append('text', text);
    // Go 的 Part.FileName() 会丢弃目录信息，相对路径只能放在字段名里。
    for (const f of files) fd.append('files:' + (f.relPath || f.name), f, f.name);

    const xhr = new XMLHttpRequest();
    xhr.open('POST', '/api/items');
    xhr.withCredentials = true;
    xhr.upload.onprogress = (e) => {
      if (e.lengthComputable && onProgress) onProgress(e.loaded, e.total);
    };
    xhr.onload = () => {
      let data = null;
      try { data = JSON.parse(xhr.responseText); } catch { /* 忽略非 JSON */ }
      if (xhr.status >= 200 && xhr.status < 300 && data) {
        resolve(data);
        return;
      }
      const err = data && data.error ? data.error : { code: 'unknown', message: xhr.statusText };
      reject(new ApiError(xhr.status, err.code, err.message));
    };
    xhr.onerror = () => reject(new ApiError(0, 'network', '网络错误，上传中断'));
    xhr.onabort = () => reject(new ApiError(0, 'aborted', '上传已取消'));
    xhr.send(fd);
  });
}
```

`web/main.js` 新增目录收集（放在文件末尾的工具区）：

```js
// collectDroppedFiles 把拖入的 DataTransfer 展开成带 relPath 的 File 列表。
async function collectDroppedFiles(dataTransfer) {
  const out = [];
  const walk = async (entry, prefix) => {
    if (entry.isFile) {
      const file = await new Promise((res, rej) => entry.file(res, rej));
      file.relPath = prefix + entry.name;
      out.push(file);
      return;
    }
    if (!entry.isDirectory) return;
    const reader = entry.createReader();
    for (;;) {
      const batch = await new Promise((res, rej) => reader.readEntries(res, rej));
      if (!batch.length) break;
      for (const child of batch) await walk(child, prefix + entry.name + '/');
    }
  };
  const items = Array.from(dataTransfer.items || []);
  for (const item of items) {
    const entry = item.webkitGetAsEntry ? item.webkitGetAsEntry() : null;
    if (entry) {
      await walk(entry, '');
      continue;
    }
    const file = item.getAsFile();
    if (file) {
      file.relPath = file.name;
      out.push(file);
    }
  }
  if (!items.length) {
    for (const file of Array.from(dataTransfer.files || [])) {
      file.relPath = file.webkitRelativePath || file.name;
      out.push(file);
    }
  }
  return out;
}
```

拖拽处理（改现有 `web/main.js` 的 `composer` drop 监听，当前写法是直接 `addFiles(ev.dataTransfer.files)`）：

```js
composer.addEventListener('drop', async (ev) => {
  ev.preventDefault();
  composer.classList.remove('drag');
  try {
    const files = await collectDroppedFiles(ev.dataTransfer);
    warnIfTooMany(files);
    addFiles(files); // main.js 已有：追加到 state.pending 并刷新列表
  } catch (err) {
    setStatus('读取拖入的文件失败：' + err.message);
  }
});
```

「选择文件夹」按钮（`web/index.html` 的 `.composer-actions` 里，紧挨现有的「选择文件」加）：

```html
<label class="file-btn">选择文件夹<input id="composer-dir" type="file" webkitdirectory directory multiple hidden></label>
```

```js
$('composer-dir').addEventListener('change', (ev) => {
  const files = Array.from(ev.target.files || []).map((f) => {
    f.relPath = f.webkitRelativePath || f.name;
    return f;
  });
  warnIfTooMany(files);
  addFiles(files);
  ev.target.value = '';
});
```

数量提示（新增工具函数，用 `limit-hint` 区域展示，项目里没有 toast）：

```js
function warnIfTooMany(files) {
  if (files.length <= 500) return;
  $('limit-hint').textContent =
    `已选择 ${files.length} 个文件，单条内容默认最多 500 个，超出会被服务端拒绝`;
}
```

- [ ] **Step 3: 附件按路径展示 + zip 按钮**

`web/render.js` 的 `renderItem` 整个函数替换为（按 `path` 排序、显示目录、多附件出 zip 按钮）：

```js
// renderItem 依据模板生成一张卡片，并把事件接到 handlers 上。
export function renderItem(item, handlers) {
  const tpl = document.getElementById('tpl-item');
  const node = tpl.content.firstElementChild.cloneNode(true);

  node.dataset.id = item.id;
  node.classList.toggle('taken', item.taken);
  node.querySelector('.item-user').textContent = item.created_by || '未知';
  node.querySelector('.item-ip').textContent = item.source_ip || '未知来源';
  node.querySelector('.item-time').textContent = fmtTime(item.created_at);
  node.querySelector('.item-taken-tag').hidden = !item.taken;
  node.querySelector('.item-text').textContent = item.text || '';

  const fileList = node.querySelector('.files');
  const files = [...(item.files || [])].sort((a, b) =>
    (a.path || a.name).localeCompare(b.path || b.name));

  for (const f of files) {
    const rel = f.path || f.name;
    const dir = rel.includes('/') ? rel.slice(0, rel.lastIndexOf('/')) : '';

    const li = document.createElement('li');
    if (dir) {
      const dirTag = document.createElement('span');
      dirTag.className = 'fdir';
      dirTag.textContent = dir + '/';
      li.append(dirTag);
    }

    const name = document.createElement('span');
    name.className = 'fname';
    name.textContent = f.name;

    const meta = document.createElement('span');
    meta.className = 'fmeta';
    meta.textContent = `${fmtBytes(f.size)} · ${(f.sha256 || '').slice(0, 8)} · 下载 ${f.downloads} 次`;

    const link = document.createElement('a');
    link.href = `/api/items/${encodeURIComponent(item.id)}/files/${encodeURIComponent(f.id)}`;
    link.textContent = '下载';
    link.setAttribute('download', f.name);

    const del = document.createElement('button');
    del.type = 'button';
    del.className = 'danger';
    del.textContent = '删除附件';
    del.addEventListener('click', () => handlers.onDeleteFile(item, f));

    li.append(name, meta, link, del);
    fileList.append(li);
  }

  const needsZip = files.length > 1 || files.some((f) => (f.path || f.name).includes('/'));
  if (needsZip) {
    const zip = document.createElement('a');
    zip.className = 'zip-btn';
    zip.href = `/api/items/${encodeURIComponent(item.id)}/zip`;
    zip.setAttribute('download', `lanbox-${item.id}.zip`);
    zip.textContent = '下载全部 (.zip)';
    fileList.after(zip);
  }

  const takenBtn = node.querySelector('.act-taken');
  takenBtn.textContent = item.taken ? '取消取走' : '标记已取走';
  takenBtn.addEventListener('click', () => handlers.onToggleTaken(item));

  node.querySelector('.act-delete').addEventListener('click', () => handlers.onDelete(item));
  return node;
}
```

`web/style.css` 追加：

```css
.fdir { opacity: .55; font-size: 12px; }
.zip-btn { display: inline-block; margin-top: 6px; font-size: 13px; }
```

- [ ] **Step 4: 手动验收**

按 `README.md` 的手动验收清单逐项过一遍，并新增三项：
1. 拖入含子目录的文件夹 → 上传成功，卡片按 `<目录>/<文件>` 展示
2. 「选择文件夹」行为与拖拽一致
3. 多条附件内容出现「下载全部 (.zip)」→ 下载后 `unzip -t` 通过，条目名与路径一致

Run: `go build ./... && go test ./... -count=1`
Expected: 全绿

- [ ] **Step 5: 提交**

```bash
git add web
git commit -m "feat(web): 目录上传、路径展示与 zip 下载"
```

---

## Task 13: 构建产物、文档与最终校验

**Files:**
- Modify: `Makefile`
- Modify: `README.md`

- [ ] **Step 1: Makefile 增加 CLI 目标**

`Makefile` 顶部变量区追加：

```make
CLI      := lanbox-cli
CLI_CMD  := ./cmd/lanbox-cli
```

`build` 目标改为：

```make
build:
	CGO_ENABLED=0 go build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(APP) $(CMD)
	CGO_ENABLED=0 go build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(CLI) $(CLI_CMD)
	@echo "已生成 ./$(APP) 与 ./$(CLI)（$(HOST_OS)/$(HOST_ARCH)，版本 $(VERSION)）"
```

`dist` 循环里在内层 `go build` 之后追加一次 CLI 编译（同一 `GOOS/GOARCH/GOARM`）：

```make
			CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch GOARM=$$goarm \
				go build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(DIST)/$(APP)-$$os-$$arch$$suffix $(CMD); \
			CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch GOARM=$$goarm \
				go build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(DIST)/$(CLI)-$$os-$$arch$$suffix $(CLI_CMD); \
```

`clean` 目标改为：

```make
clean:
	rm -rf $(DIST) $(APP) $(CLI)
```

`install` 目标追加一行：

```make
	install -m 0755 $(CLI) $(DESTDIR)$(PREFIX)/bin/$(CLI)
```

- [ ] **Step 2: 验证构建**

Run: `make build && ./lanbox-cli help && make dist && ls dist`
Expected: `dist` 下同时有 `lanbox-*` 与 `lanbox-cli-*` 各四个，`SHA256SUMS` 含全部 8 个产物

- [ ] **Step 3: 更新 README**

新增一章「命令行客户端」（放在「快速开始」之后）：

````markdown
## 命令行客户端 lanbox-cli

`make dist` 会同时产出 `lanbox-cli-<os>-<arch>`（4 个平台）。放到 PATH 后：

```bash
# 登录（/etc/lanbox/config.json 里 read 到的地址；自签证书用 -ca 或 -insecure）
lanbox-cli login -s https://192.168.1.9:8443 -u admin
# 密码来源：LANBOX_PASSWORD 环境变量 > 管道 stdin > 终端无回显提示

# 发布文字 / 单个文件 / 整个目录（目录必须 -r）
echo "给内网的一句话" | lanbox-cli put -
lanbox-cli put 报告.pdf -m "内网同事收"
lanbox-cli put -r ./素材 -m "素材目录"

# 查看与取回
lanbox-cli ls -n 20
lanbox-cli files <ID>
lanbox-cli get <ID> -o ./收件        # 按相对路径还原目录
lanbox-cli get <ID> --zip ./all.zip  # 整条内容打包
lanbox-cli get <ID> --resume -o .    # 单文件断点续传

# 其他
lanbox-cli taken <ID>
lanbox-cli rm <ID>
lanbox-cli rm-file <ID> <FID>
lanbox-cli logout
```

退出码：`0` 成功、`1` 一般错误、`2` 用法错误、`3` 网络错误、`4` 认证/会话问题。
自签证书三种处理方式：`-ca data/tls/cert.pem`（推荐）、系统信任证书、`-insecure`（仅调试）。
````

配置表补两行：

```markdown
| `max_item_size_mb` | 2048 | 单条内容附件总大小上限，超出返回 413 |
| `max_files_per_item` | 500 | 单条内容附件数量上限（目录上传会用到） |
```

环境变量列表补 `LANBOX_MAX_ITEM_SIZE_MB`、`LANBOX_MAX_FILES_PER_ITEM`。

「不做的事」一节删掉「zip 打包下载」，并把附件说明补一句：

```markdown
- 附件保留上传时的相对路径；`GET /api/items/<ID>/zip` 可一键打包（不压缩，不支持断点续传）
```

- [ ] **Step 4: 最终校验**

Run: `make check && go test ./... -count=1 && go build ./...`
Expected: gofmt 无输出、vet 无告警、全部测试通过

- [ ] **Step 5: 提交**

```bash
git add Makefile README.md
git commit -m "build: 交叉编译 CLI，README 补充客户端与目录传输说明"
```

---

## 计划自查

**规格覆盖对照：**

| 规格章节 | 对应任务 |
|---|---|
| §4.1 数据模型 + 旧数据兼容 | Task 2 |
| §4.2 `SanitizeRelPath` | Task 1 |
| §4.3 `AddFile` 路径/去重/限额 | Task 3 |
| §4.4 上传字段 `files:<path>` | Task 5 |
| §4.5 配置新增 | Task 4 |
| §4.6 错误码 | Task 3（领域错误）+ Task 5（HTTP 映射） |
| §5 流式 zip | Task 6 |
| §6.1 命令面 | Task 10 + Task 11 |
| §6.2 会话与地址 | Task 10 |
| §6.3 上传细节 | Task 11（`putwalk.go`） |
| §6.4 下载细节 | Task 9 + Task 11（`cmd_get.go`） |
| §6.5 输出与退出码 | Task 10 + Task 11（`render.go`） |
| §6.6 TLS | Task 10（`httpClient`） |
| §6.7 无回显密码 | Task 10（`term_*.go`） |
| §7 网页改动 | Task 12 |
| §8 与 Vue 改版关系 | 不涉及代码，仅在 README 说明 |
| §9 测试策略 | 各任务内测试 |
| §10/§11 不做与兼容 | Task 2、Task 3、Task 13 |

**类型一致性检查：** `AddFile(itemID, relPath string, declaredSize int64, r io.Reader)`（Task 3）在 Task 5 的调用处一致；`client.Upload{Path, Size, Open}`（Task 8）在 Task 11 的 `newUpload` 一致；`client.DownloadOptions{Dest, Force, Resume, OnProgress}`（Task 9）在 Task 11 一致；`collectUploads(paths, recursive)`（Task 11）在测试与 `cmd_put.go` 一致。
