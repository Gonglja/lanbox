package main

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/pem"
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

// cmdEnv 是本包测试用的真实服务端：临时 data_dir + 自签证书 + 已登录会话。
type cmdEnv struct {
	g      *globals
	c      *client.Client
	out    *bytes.Buffer
	errOut *bytes.Buffer
	srv    *httptest.Server
	am     *auth.Manager
	caFile string
}

// newCmdEnv 起一个 TLS 测试服务端，把自签证书写到文件供 -ca 使用，
// 并保存登录会话，返回可直接调用 cmdXxx 的 globals。
func newCmdEnv(t *testing.T, opts ...func(*httpapi.Options)) *cmdEnv {
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
	srvOpts := httpapi.Options{
		Cfg: cfg, Store: st, Auth: am, Web: lanbox.WebFS(), Log: log.New(io.Discard, "", 0),
	}
	for _, opt := range opts {
		opt(&srvOpts)
	}
	srv := httptest.NewTLSServer(httpapi.New(srvOpts))
	t.Cleanup(srv.Close)

	// 自签证书落盘，正好验证 -ca 的信任路径。
	caPath := filepath.Join(t.TempDir(), "cert.pem")
	raw := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw})
	if err := os.WriteFile(caPath, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	httpc := srv.Client() // 已信任测试证书
	sess, err := client.Login(context.Background(), srv.URL, "admin", pw, httpc)
	if err != nil {
		t.Fatalf("登录失败: %v", err)
	}
	if err := saveSession(sess); err != nil {
		t.Fatalf("保存会话失败: %v", err)
	}
	c, err := client.New(client.Options{BaseURL: sess.URL, SID: sess.SID, HTTP: httpc})
	if err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	return &cmdEnv{
		g:      &globals{server: sess.URL, caFile: caPath, stdout: &out, stderr: &errOut},
		c:      c,
		out:    &out,
		errOut: &errOut,
		srv:    srv,
		am:     am,
		caFile: caPath,
	}
}

// loginAs 以另一个账号登录并覆盖本地会话文件，供非管理员用例使用。
func (e *cmdEnv) loginAs(t *testing.T, user, pass string) {
	t.Helper()
	sess, err := client.Login(context.Background(), e.srv.URL, user, pass, e.srv.Client())
	if err != nil {
		t.Fatalf("以 %s 登录失败: %v", user, err)
	}
	if err := saveSession(sess); err != nil {
		t.Fatalf("保存会话失败: %v", err)
	}
}

// uploadOf 构造一个内存附件。
func uploadOf(path, body string) client.Upload {
	return client.Upload{
		Path: path,
		Size: int64(len(body)),
		Open: func() (io.ReadCloser, error) { return io.NopCloser(strings.NewReader(body)), nil },
	}
}

// putItem 发布一条带附件的内容并返回 ID。
func (e *cmdEnv) putItem(t *testing.T, ups ...client.Upload) string {
	t.Helper()
	it, err := e.c.Put(context.Background(), "", ups, nil)
	if err != nil {
		t.Fatalf("上传失败: %v", err)
	}
	return it.ID
}

// TestCmdGetJSON 断言 get --json 输出合法 JSON 且字段与实际情况一致。
func TestCmdGetJSON(t *testing.T) {
	e := newCmdEnv(t)
	id := e.putItem(t, uploadOf("d/a.txt", "aaa"), uploadOf("b.txt", "bb"))
	dir := t.TempDir()

	if err := cmdGet(context.Background(), e.g, []string{id, "-o", dir, "--json"}); err != nil {
		t.Fatalf("get --json 失败: %v", err)
	}
	var got struct {
		Downloaded []struct {
			Path string `json:"path"`
			Size int64  `json:"size"`
		} `json:"downloaded"`
		Skipped []string `json:"skipped"`
		Text    string   `json:"text"`
	}
	if err := json.Unmarshal(e.out.Bytes(), &got); err != nil {
		t.Fatalf("输出不是合法 JSON: %v：%s", err, e.out.String())
	}
	if len(got.Downloaded) != 2 {
		t.Fatalf("应有 2 条下载记录，得到 %+v", got)
	}
	sizes := map[string]int64{}
	for _, d := range got.Downloaded {
		sizes[filepath.Base(d.Path)] = d.Size
	}
	if sizes["a.txt"] != 3 || sizes["b.txt"] != 2 {
		t.Fatalf("下载记录的大小不对: %+v", sizes)
	}
	if got.Text != "" || len(got.Skipped) != 0 {
		t.Fatalf("不该出现 text/skipped: %+v", got)
	}
	if raw, err := os.ReadFile(filepath.Join(dir, "d", "a.txt")); err != nil || string(raw) != "aaa" {
		t.Fatalf("落盘内容不对: %q %v", raw, err)
	}
}

// TestCmdGetSkipsExistingToStderr 断言重复下载时"跳过已存在"打到 stderr 且不算失败。
func TestCmdGetSkipsExistingToStderr(t *testing.T) {
	e := newCmdEnv(t)
	id := e.putItem(t, uploadOf("a.txt", "a"))
	dir := t.TempDir()

	if err := cmdGet(context.Background(), e.g, []string{id, "-o", dir}); err != nil {
		t.Fatalf("首次下载失败: %v", err)
	}
	e.out.Reset()
	e.errOut.Reset()
	if err := cmdGet(context.Background(), e.g, []string{id, "-o", dir}); err != nil {
		t.Fatalf("跳过应视为成功，得到 %v", err)
	}
	if strings.Contains(e.out.String(), "跳过已存在") {
		t.Fatalf("跳过信息不应写 stdout: %q", e.out.String())
	}
	if !strings.Contains(e.errOut.String(), "跳过已存在") {
		t.Fatalf("跳过信息应写 stderr: %q", e.errOut.String())
	}
}

// TestCmdGetZipForce 断言 --zip 已存在时默认跳过，--force 时才覆盖。
func TestCmdGetZipForce(t *testing.T) {
	e := newCmdEnv(t)
	id := e.putItem(t, uploadOf("a.txt", "a"))
	zipPath := filepath.Join(t.TempDir(), "out.zip")

	if err := cmdGet(context.Background(), e.g, []string{id, "--zip", zipPath}); err != nil {
		t.Fatalf("首次下载 zip 失败: %v", err)
	}
	e.errOut.Reset()
	if err := cmdGet(context.Background(), e.g, []string{id, "--zip", zipPath}); err != nil {
		t.Fatalf("已存在时跳过应返回 nil，得到 %v", err)
	}
	if !strings.Contains(e.errOut.String(), "跳过已存在") {
		t.Fatalf("应提示跳过已存在: %q", e.errOut.String())
	}
	// 弄脏目标文件，--force 后应重新变成合法 zip。
	if err := os.WriteFile(zipPath, []byte("junk"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := cmdGet(context.Background(), e.g, []string{id, "--zip", zipPath, "--force"}); err != nil {
		t.Fatalf("--force 覆盖失败: %v", err)
	}
	raw, err := os.ReadFile(zipPath)
	if err != nil || !bytes.HasPrefix(raw, []byte("PK")) {
		t.Fatalf("--force 后应是合法 zip，得到 %q %v", raw[:min(4, len(raw))], err)
	}
}

// TestCmdGetRejectsNonDirOut 断言 -o 指向普通文件时给出清晰用法错误。
func TestCmdGetRejectsNonDirOut(t *testing.T) {
	e := newCmdEnv(t)
	id := e.putItem(t, uploadOf("a.txt", "a"))
	notDir := filepath.Join(t.TempDir(), "notdir")
	if err := os.WriteFile(notDir, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	err := cmdGet(context.Background(), e.g, []string{id, "-o", notDir})
	if !errors.Is(err, errUsage) {
		t.Fatalf("-o 不是目录应返回 errUsage，得到 %v", err)
	}
	if !strings.Contains(err.Error(), "不是目录") {
		t.Fatalf("错误应说明不是目录: %v", err)
	}
}

// TestCmdGetAggregatesFailures 断言单个附件失败不影响其余文件，最后汇总报错。
func TestCmdGetAggregatesFailures(t *testing.T) {
	e := newCmdEnv(t)
	id := e.putItem(t, uploadOf("d/one.txt", "1"), uploadOf("ok.txt", "2"))
	dir := t.TempDir()
	// 在需要成为目录的位置放一个普通文件，令 d/one.txt 写入失败。
	if err := os.WriteFile(filepath.Join(dir, "d"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	err := cmdGet(context.Background(), e.g, []string{id, "-o", dir})
	if err == nil || !strings.Contains(err.Error(), "1 个文件下载失败") {
		t.Fatalf("应汇总为失败错误，得到 %v", err)
	}
	if raw, err := os.ReadFile(filepath.Join(dir, "ok.txt")); err != nil || string(raw) != "2" {
		t.Fatalf("其余文件应下载成功: %q %v", raw, err)
	}
}

// TestCmdRmAndLogoutJSON 断言 rm / rm-file / logout 的 --json 输出。
func TestCmdRmAndLogoutJSON(t *testing.T) {
	e := newCmdEnv(t)
	id := e.putItem(t, uploadOf("a.txt", "a"))

	if err := cmdRm(context.Background(), e.g, []string{id, "--json"}); err != nil {
		t.Fatalf("rm --json 失败: %v", err)
	}
	var rmOut map[string]string
	if err := json.Unmarshal(e.out.Bytes(), &rmOut); err != nil || rmOut["deleted"] != id {
		t.Fatalf("rm --json 输出不对: %v %s", err, e.out.String())
	}

	e.out.Reset()
	id2 := e.putItem(t, uploadOf("b.txt", "b"))
	it, err := e.c.GetItem(context.Background(), id2)
	if err != nil {
		t.Fatal(err)
	}
	if err := cmdRmFile(context.Background(), e.g, []string{id2, it.Files[0].ID, "--json"}); err != nil {
		t.Fatalf("rm-file --json 失败: %v", err)
	}
	var rmFileOut map[string]string
	if err := json.Unmarshal(e.out.Bytes(), &rmFileOut); err != nil || rmFileOut["deleted_file"] != it.Files[0].ID {
		t.Fatalf("rm-file --json 输出不对: %v %s", err, e.out.String())
	}

	e.out.Reset()
	if err := cmdLogout(context.Background(), e.g, []string{"--json"}); err != nil {
		t.Fatalf("logout --json 失败: %v", err)
	}
	var logoutOut map[string]string
	if err := json.Unmarshal(e.out.Bytes(), &logoutOut); err != nil || logoutOut["logged_out"] != "admin" {
		t.Fatalf("logout --json 输出不对: %v %s", err, e.out.String())
	}
}
