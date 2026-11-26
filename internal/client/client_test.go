package client_test

import (
	"archive/zip"
	"context"
	"errors"
	"io"
	"log"
	"net/http/httptest"
	"os"
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
	auth  *auth.Manager
}

// newTestEnv 起一个真实的 TLS 服务端，与 httpapi 测试夹具同构。
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
	return &testEnv{url: srv.URL, https: srv, pw: pw, store: st, auth: am}
}

func loginClient(t *testing.T, e *testEnv) *client.Client {
	t.Helper()
	httpc := e.https.Client() // 已信任测试证书
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
	if len(got.Files) != 1 || got.Files[0].Path != "photos/a.jpg" || got.Files[0].RelPath() != "photos/a.jpg" {
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

func TestClientRejectsBadBaseURL(t *testing.T) {
	for _, bad := range []string{"", "not-a-url", "ftp://example.com"} {
		if _, err := client.New(client.Options{BaseURL: bad}); err == nil {
			t.Fatalf("%q 应报错", bad)
		}
	}
}

func TestListTodoFilter(t *testing.T) {
	e := newTestEnv(t)
	c := loginClient(t, e)
	ctx := context.Background()

	todoItem, err := e.store.Create(store.CreateInput{Text: "待取的发票", CreatedBy: "admin"})
	if err != nil {
		t.Fatal(err)
	}
	doneItem, err := e.store.Create(store.CreateInput{Text: "已归档的合同", CreatedBy: "admin"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.store.SetTaken(doneItem.ID, true, "admin"); err != nil {
		t.Fatal(err)
	}

	todo, err := c.List(ctx, client.ListQuery{Limit: 10, Todo: true})
	if err != nil {
		t.Fatalf("过滤未取走失败: %v", err)
	}
	if len(todo.Items) != 1 || todo.Items[0].ID != todoItem.ID {
		t.Fatalf("未取走应只剩 1 条: %+v", todo.Items)
	}

	all, err := c.List(ctx, client.ListQuery{Limit: 10})
	if err != nil {
		t.Fatalf("列表失败: %v", err)
	}
	if len(all.Items) != 2 {
		t.Fatalf("全部应返回 2 条: %+v", all.Items)
	}

	found, err := c.List(ctx, client.ListQuery{Limit: 10, Search: "合同"})
	if err != nil {
		t.Fatalf("搜索失败: %v", err)
	}
	if len(found.Items) != 1 || found.Items[0].ID != doneItem.ID {
		t.Fatalf("搜索应命中已归档的合同: %+v", found.Items)
	}
}

// readerFunc 把字符串包装成可重复打开的 Upload 内容源。
func readerFunc(s string) func() (io.ReadCloser, error) {
	return func() (io.ReadCloser, error) {
		return io.NopCloser(strings.NewReader(s)), nil
	}
}

func TestPutListTakenAndDelete(t *testing.T) {
	e := newTestEnv(t)
	c := loginClient(t, e)
	ctx := context.Background()

	var sent, total int64
	it, err := c.Put(ctx, "内网素材", []client.Upload{
		{Path: "photos/2024/a.jpg", Size: 3, Open: readerFunc("aaa")},
		{Path: "notes.txt", Size: 5, Open: readerFunc("hello")},
	}, func(s, tt int64) { sent, total = s, tt })
	if err != nil {
		t.Fatalf("上传失败: %v", err)
	}
	if it.Text != "内网素材" || len(it.Files) != 2 {
		t.Fatalf("返回内容不对: %+v", it)
	}
	if it.Files[0].Path != "photos/2024/a.jpg" || it.Files[1].Path != "notes.txt" {
		t.Fatalf("路径应保留: %+v", it.Files)
	}
	if sent != total || total != 8+int64(len("内网素材")) {
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

func TestPutKeepsNestedPaths(t *testing.T) {
	e := newTestEnv(t)
	c := loginClient(t, e)
	it, err := c.Put(context.Background(), "", []client.Upload{
		{Path: "素材/2024/a.jpg", Size: 1, Open: readerFunc("a")},
		{Path: "素材/b.txt", Size: 1, Open: readerFunc("b")},
	}, nil)
	if err != nil {
		t.Fatalf("上传失败: %v", err)
	}
	if it.Files[0].Path != "素材/2024/a.jpg" || it.Files[1].Path != "素材/b.txt" {
		t.Fatalf("中文目录路径应保留: %+v", it.Files)
	}
}

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
	if err := os.WriteFile(dest+".part", []byte(body[:400]), 0o640); err != nil {
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

func TestDownloadFileDetectsChecksumMismatch(t *testing.T) {
	e := newTestEnv(t)
	c := loginClient(t, e)
	ctx := context.Background()
	it, err := c.Put(ctx, "", []client.Upload{{Path: "a.txt", Size: 3, Open: readerFunc("abc")}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	bad := it.Files[0]
	bad.SHA256 = strings.Repeat("0", 64)
	dest := filepath.Join(t.TempDir(), "a.txt")
	if err := c.DownloadFile(ctx, it.ID, bad, client.DownloadOptions{Dest: dest}); err == nil {
		t.Fatal("校验不匹配应报错")
	}
	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Fatal("校验失败不应留下目标文件")
	}
	if _, err := os.Stat(dest + ".part"); !os.IsNotExist(err) {
		t.Fatal("校验失败应删除临时文件")
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
	if err := c.DownloadZip(ctx, it.ID, dest, false); err != nil {
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

// TestDownloadZipRespectsForce 断言 zip 目标已存在时需显式 Force 才覆盖。
func TestDownloadZipRespectsForce(t *testing.T) {
	e := newTestEnv(t)
	c := loginClient(t, e)
	ctx := context.Background()
	it, err := c.Put(ctx, "", []client.Upload{{Path: "a.txt", Size: 1, Open: readerFunc("a")}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(t.TempDir(), "out.zip")
	if err := c.DownloadZip(ctx, it.ID, dest, false); err != nil {
		t.Fatalf("首次下载 zip 失败: %v", err)
	}
	if err := c.DownloadZip(ctx, it.ID, dest, false); !errors.Is(err, client.ErrExists) {
		t.Fatalf("已存在且未 Force 应返回 ErrExists，得到 %v", err)
	}
	if err := c.DownloadZip(ctx, it.ID, dest, true); err != nil {
		t.Fatalf("Force 覆盖失败: %v", err)
	}
	if _, err := os.Stat(dest + ".part"); !os.IsNotExist(err) {
		t.Fatal("成功 rename 后不应残留 .part")
	}
}

// TestDownloadFileResumeRefusesExistingDest 断言 --resume 不会绕过「目标已存在」保护。
func TestDownloadFileResumeRefusesExistingDest(t *testing.T) {
	e := newTestEnv(t)
	c := loginClient(t, e)
	ctx := context.Background()
	it, err := c.Put(ctx, "", []client.Upload{{Path: "a.txt", Size: 3, Open: readerFunc("abc")}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	dest := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(dest, []byte("old"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dest+".part", []byte("a"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := c.DownloadFile(ctx, it.ID, it.Files[0], client.DownloadOptions{Dest: dest, Resume: true}); !errors.Is(err, client.ErrExists) {
		t.Fatalf("resume 遇到已存在目标应返回 ErrExists，得到 %v", err)
	}
	raw, _ := os.ReadFile(dest)
	if string(raw) != "old" {
		t.Fatalf("不得覆盖已有文件，得到 %q", raw)
	}
	// 带 --force 才允许续传覆盖。
	if err := c.DownloadFile(ctx, it.ID, it.Files[0], client.DownloadOptions{Dest: dest, Resume: true, Force: true}); err != nil {
		t.Fatalf("force + resume 应成功: %v", err)
	}
	raw, _ = os.ReadFile(dest)
	if string(raw) != "abc" {
		t.Fatalf("续传后内容不对: %q", raw)
	}
}

// TestPutProgressReachesTotalWithText 断言带文本时上传进度能到 100%。
func TestPutProgressReachesTotalWithText(t *testing.T) {
	e := newTestEnv(t)
	c := loginClient(t, e)
	var last, total int64
	_, err := c.Put(context.Background(), "说明文字", []client.Upload{
		{Path: "a.bin", Size: 3, Open: readerFunc("abc")},
	}, func(s, tt int64) { last, total = s, tt })
	if err != nil {
		t.Fatal(err)
	}
	if last != total || total == 0 {
		t.Fatalf("进度应到 100%%: last=%d total=%d", last, total)
	}
}

// TestStatsAdmin 断言管理员能看到统计与 admin 块。
func TestStatsAdmin(t *testing.T) {
	e := newTestEnv(t)
	c := loginClient(t, e)
	ctx := context.Background()

	it, err := e.store.Create(store.CreateInput{Text: "带附件", CreatedBy: "admin"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.store.AddFile(it.ID, "a.txt", 2, strings.NewReader("hi")); err != nil {
		t.Fatal(err)
	}
	if _, err := e.store.Create(store.CreateInput{Text: "只有文字", CreatedBy: "admin"}); err != nil {
		t.Fatal(err)
	}

	st, err := c.Stats(ctx)
	if err != nil {
		t.Fatalf("Stats 失败: %v", err)
	}
	if st.Items != (client.StatsItems{Total: 2, Todo: 2}) {
		t.Fatalf("内容统计不对: %+v", st.Items)
	}
	if st.Files != (client.StatsFiles{Count: 1, Bytes: 2}) {
		t.Fatalf("附件统计不对: %+v", st.Files)
	}
	if st.Version == "" || st.UptimeS < 0 {
		t.Fatalf("版本/运行时长异常: %q %d", st.Version, st.UptimeS)
	}
	if st.Admin == nil {
		t.Fatal("管理员响应应包含 admin 块")
	}
	if st.Admin.Users != 1 {
		t.Fatalf("用户数应为 1，得到 %d", st.Admin.Users)
	}
	if st.Admin.ItemsDir == "" || st.Admin.Listen == "" {
		t.Fatalf("存储路径/监听地址为空: %+v", st.Admin)
	}
	if st.Admin.Disk.FreeBytes == nil && st.Admin.Disk.Error == "" {
		t.Fatalf("磁盘字段既没有空间也没有错误: %+v", st.Admin.Disk)
	}
}

// TestStatsNonAdminHasNoAdminBlock 断言普通用户拿不到 admin 块。
func TestStatsNonAdminHasNoAdminBlock(t *testing.T) {
	e := newTestEnv(t)
	if err := e.auth.CreateUser("bob", "bobpassword", false); err != nil {
		t.Fatalf("创建普通用户失败: %v", err)
	}
	httpc := e.https.Client()
	sess, err := client.Login(context.Background(), e.url, "bob", "bobpassword", httpc)
	if err != nil {
		t.Fatalf("登录失败: %v", err)
	}
	c, err := client.New(client.Options{BaseURL: sess.URL, SID: sess.SID, HTTP: httpc})
	if err != nil {
		t.Fatal(err)
	}
	st, err := c.Stats(context.Background())
	if err != nil {
		t.Fatalf("Stats 失败: %v", err)
	}
	if st.Admin != nil {
		t.Fatalf("普通用户不应看到 admin 块: %+v", st.Admin)
	}
	if st.Version == "" {
		t.Fatal("普通用户也应看到版本")
	}
}
