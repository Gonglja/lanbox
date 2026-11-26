package httpapi_test

import (
	"archive/zip"
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lanbox"
	"lanbox/internal/auth"
	"lanbox/internal/config"
	"lanbox/internal/httpapi"
	"lanbox/internal/store"
)

type env struct {
	srv           *httptest.Server
	client        *http.Client
	clientNoJar   *http.Client
	store         *store.Store
	auth          *auth.Manager
	cfg           config.Config
	adminPassword string
	restarts      *int
}

// testMaxFilesPerItem 刻意取小值，让「超过附件数量」的用例只需几十个 part；
// 夹具上限是 20，任何单条内容超过 20 个附件的测试都会拿到 400 too_many_files。
const testMaxFilesPerItem = 20

func newEnv(t *testing.T) *env {
	t.Helper()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	body := fmt.Sprintf(`{"data_dir":%q,"max_file_size_mb":8,"max_item_size_mb":16,"min_free_disk_mb":0,"max_files_per_item":%d}`,
		dir, testMaxFilesPerItem)
	if err := os.WriteFile(cfgPath, []byte(body), 0o600); err != nil {
		t.Fatalf("写配置失败: %v", err)
	}
	var st *store.Store
	restarts := 0
	live, err := config.OpenLive(cfgPath, config.Hooks{
		Apply: func(prev, next config.Config) error {
			if st != nil {
				st.SetLimits(store.Options{
					MaxFileBytes: next.MaxFileBytes(),
					MinFreeBytes: next.MinFreeBytes(),
					MaxTextBytes: next.MaxTextBytes,
					MaxFiles:     next.MaxFilesPerItem,
					MaxItemBytes: next.MaxItemBytes(),
				})
			}
			return nil
		},
		Restart: func() { restarts++ },
	})
	if err != nil {
		t.Fatalf("打开 Live 失败: %v", err)
	}
	cfg := live.Current()

	st, err = store.Open(filepath.Join(cfg.DataDir, "items"), store.Options{
		MaxFileBytes: cfg.MaxFileBytes(),
		MinFreeBytes: 0,
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
	s := httpapi.New(httpapi.Options{
		Cfg: cfg, Store: st, Auth: am, Web: lanbox.WebFS(), Log: log.New(io.Discard, "", 0),
		Live: live, Version: "test", StartedAt: time.Now(),
	})
	srv := httptest.NewTLSServer(s)
	t.Cleanup(srv.Close)

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	return &env{
		srv:           srv,
		client:        &http.Client{Jar: jar, Transport: srv.Client().Transport, Timeout: 30 * time.Second},
		clientNoJar:   &http.Client{Transport: srv.Client().Transport, Timeout: 30 * time.Second},
		store:         st,
		auth:          am,
		cfg:           cfg,
		adminPassword: pw,
		restarts:      &restarts,
	}
}

func (e *env) do(t *testing.T, req *http.Request) (*http.Response, []byte) {
	t.Helper()
	return e.doWith(t, e.client, req)
}

func (e *env) doWith(t *testing.T, c *http.Client, req *http.Request) (*http.Response, []byte) {
	t.Helper()
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("%s %s 请求失败: %v", req.Method, req.URL.Path, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("读取响应失败: %v", err)
	}
	return resp, body
}

func (e *env) newJSONReq(t *testing.T, method, path string, payload any) *http.Request {
	t.Helper()
	var buf bytes.Buffer
	if payload != nil {
		if err := json.NewEncoder(&buf).Encode(payload); err != nil {
			t.Fatal(err)
		}
	}
	req, err := http.NewRequest(method, e.srv.URL+path, &buf)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	return req
}

func (e *env) login(t *testing.T, user, pass string) {
	t.Helper()
	resp, body := e.do(t, e.newJSONReq(t, http.MethodPost, "/api/login", map[string]string{"username": user, "password": pass}))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("登录失败: %d %s", resp.StatusCode, body)
	}
}

func (e *env) loginAdmin(t *testing.T) {
	t.Helper()
	e.login(t, "admin", e.adminPassword)
}

func TestLoginFlow(t *testing.T) {
	e := newEnv(t)

	resp, body := e.do(t, e.newJSONReq(t, http.MethodGet, "/api/me", nil))
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("未登录访问 /api/me 应为 401，得到 %d %s", resp.StatusCode, body)
	}

	resp, body = e.do(t, e.newJSONReq(t, http.MethodPost, "/api/login", map[string]string{"username": "admin", "password": "wrong"}))
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("错误密码应 401，得到 %d %s", resp.StatusCode, body)
	}

	e.loginAdmin(t)
	resp, body = e.do(t, e.newJSONReq(t, http.MethodGet, "/api/me", nil))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("登录后 /api/me 应 200，得到 %d %s", resp.StatusCode, body)
	}
	var me struct {
		Username string `json:"username"`
		IsAdmin  bool   `json:"is_admin"`
	}
	if err := json.Unmarshal(body, &me); err != nil {
		t.Fatalf("解析 /api/me 失败: %v (%s)", err, body)
	}
	if me.Username != "admin" || !me.IsAdmin {
		t.Fatalf("身份信息不对: %+v", me)
	}

	resp, _ = e.do(t, e.newJSONReq(t, http.MethodPost, "/api/logout", nil))
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("登出应 204，得到 %d", resp.StatusCode)
	}
	resp, _ = e.do(t, e.newJSONReq(t, http.MethodGet, "/api/me", nil))
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("登出后应 401，得到 %d", resp.StatusCode)
	}
}

func TestLoginLockoutAfterRepeatedFailures(t *testing.T) {
	e := newEnv(t)
	for i := 0; i < 5; i++ {
		resp, _ := e.do(t, e.newJSONReq(t, http.MethodPost, "/api/login",
			map[string]string{"username": "admin", "password": "bad"}))
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("第 %d 次失败应 401，得到 %d", i+1, resp.StatusCode)
		}
	}
	// 锁定期间即使密码正确也应被拒绝
	resp, body := e.do(t, e.newJSONReq(t, http.MethodPost, "/api/login",
		map[string]string{"username": "admin", "password": e.adminPassword}))
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("连续失败后应锁定为 403，得到 %d %s", resp.StatusCode, body)
	}
}

func TestCSRFRejectsForeignOrigin(t *testing.T) {
	e := newEnv(t)
	e.loginAdmin(t)

	req := e.newJSONReq(t, http.MethodPost, "/api/logout", nil)
	req.Header.Set("Origin", "https://evil.example")
	resp, body := e.do(t, req)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("跨站来源应 403，得到 %d %s", resp.StatusCode, body)
	}

	req = e.newJSONReq(t, http.MethodPost, "/api/logout", nil)
	req.Header.Set("Origin", e.srv.URL)
	resp, _ = e.do(t, req)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("同源请求应通过，得到 %d", resp.StatusCode)
	}
}

func TestSecurityHeaders(t *testing.T) {
	e := newEnv(t)
	resp, _ := e.do(t, e.newJSONReq(t, http.MethodGet, "/", nil))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("首页应 200，得到 %d", resp.StatusCode)
	}
	if got := resp.Header.Get("Content-Security-Policy"); got != "default-src 'self'" {
		t.Fatalf("CSP 不对: %q", got)
	}
	if got := resp.Header.Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("nosniff 缺失: %q", got)
	}
	if got := resp.Header.Get("X-Frame-Options"); got != "DENY" {
		t.Fatalf("X-Frame-Options 不对: %q", got)
	}
}

// buildMultipart 组装一次上传请求体。
func buildMultipart(t *testing.T, text string, files map[string][]byte) (*bytes.Buffer, string) {
	t.Helper()
	buf := &bytes.Buffer{}
	mw := multipart.NewWriter(buf)
	if text != "" {
		if err := mw.WriteField("text", text); err != nil {
			t.Fatal(err)
		}
	}
	for name, body := range files {
		fw, err := mw.CreateFormFile("files", name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fw.Write(body); err != nil {
			t.Fatal(err)
		}
	}
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf, mw.FormDataContentType()
}

func (e *env) upload(t *testing.T, text string, files map[string][]byte) *http.Response {
	t.Helper()
	buf, ct := buildMultipart(t, text, files)
	req, err := http.NewRequest(http.MethodPost, e.srv.URL+"/api/items", buf)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", ct)
	resp, body := e.do(t, req)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("上传应 201，得到 %d %s", resp.StatusCode, body)
	}
	return resp
}

func decodeItem(t *testing.T, body []byte) store.Item {
	t.Helper()
	var it store.Item
	if err := json.Unmarshal(body, &it); err != nil {
		t.Fatalf("解析内容失败: %v (%s)", err, body)
	}
	return it
}

func TestItemCreateListAndTaken(t *testing.T) {
	e := newEnv(t)
	e.loginAdmin(t)

	content := []byte(strings.Repeat("file-body-", 500))
	e.upload(t, "你好，请看附件", map[string][]byte{"报告 2026.pdf": content})
	e.upload(t, "只有文字", nil)

	resp, body := e.do(t, e.newJSONReq(t, http.MethodGet, "/api/items", nil))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("列表应 200，得到 %d %s", resp.StatusCode, body)
	}
	var list struct {
		Items      []store.Item `json:"items"`
		NextCursor string       `json:"next_cursor"`
	}
	if err := json.Unmarshal(body, &list); err != nil {
		t.Fatalf("解析列表失败: %v (%s)", err, body)
	}
	if len(list.Items) != 2 {
		t.Fatalf("应有 2 条，得到 %d", len(list.Items))
	}
	newest := list.Items[0]
	if newest.Text != "只有文字" {
		t.Fatalf("最新一条应是纯文字，得到 %+v", newest)
	}
	withFile := list.Items[1]
	if withFile.SourceIP == "" || withFile.CreatedBy != "admin" {
		t.Fatalf("来源信息缺失: %+v", withFile)
	}
	if len(withFile.Files) != 1 || withFile.Files[0].Size != int64(len(content)) {
		t.Fatalf("附件信息不对: %+v", withFile.Files)
	}
	want := sha256.Sum256(content)
	if withFile.Files[0].SHA256 != hex.EncodeToString(want[:]) {
		t.Fatal("sha256 不匹配")
	}
	if withFile.Text != "你好，请看附件" {
		t.Fatalf("文本未回填: %q", withFile.Text)
	}

	// 未取走筛选
	resp, body = e.do(t, e.newJSONReq(t, http.MethodGet, "/api/items?filter=todo", nil))
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), "只有文字") {
		t.Fatalf("未取走筛选异常: %d %s", resp.StatusCode, body)
	}

	// 标记已取走
	resp, body = e.do(t, e.newJSONReq(t, http.MethodPatch, "/api/items/"+withFile.ID, map[string]bool{"taken": true}))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("标记取走应 200，得到 %d %s", resp.StatusCode, body)
	}
	if marked := decodeItem(t, body); !marked.Taken || marked.TakenBy != "admin" {
		t.Fatalf("标记未生效: %+v", marked)
	}
	resp, body = e.do(t, e.newJSONReq(t, http.MethodGet, "/api/items?filter=todo", nil))
	if strings.Contains(string(body), withFile.ID) {
		t.Fatalf("已取走的内容不应出现在 todo 列表: %s", body)
	}

	// 搜索
	resp, body = e.do(t, e.newJSONReq(t, http.MethodGet, "/api/items?q=只有", nil))
	if !strings.Contains(string(body), "只有文字") || strings.Contains(string(body), withFile.ID) {
		t.Fatalf("搜索应只命中纯文字那条: %s", body)
	}
	// 按附件名搜索
	_, body = e.do(t, e.newJSONReq(t, http.MethodGet, "/api/items?q=报告", nil))
	if !strings.Contains(string(body), withFile.ID) {
		t.Fatalf("按附件名搜索应命中: %s", body)
	}

	// 删除
	resp, _ = e.do(t, e.newJSONReq(t, http.MethodDelete, "/api/items/"+withFile.ID, nil))
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("删除应 204，得到 %d", resp.StatusCode)
	}
	resp, _ = e.do(t, e.newJSONReq(t, http.MethodGet, "/api/items/"+withFile.ID, nil))
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("删除后应 404，得到 %d", resp.StatusCode)
	}
	resp, _ = e.do(t, e.newJSONReq(t, http.MethodDelete, "/api/items/"+withFile.ID, nil))
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("重复删除应 404，得到 %d", resp.StatusCode)
	}
}

func TestItemRejectsEmptyAndOversize(t *testing.T) {
	e := newEnv(t)
	e.loginAdmin(t)

	buf, ct := buildMultipart(t, "", nil)
	req, _ := http.NewRequest(http.MethodPost, e.srv.URL+"/api/items", buf)
	req.Header.Set("Content-Type", ct)
	resp, body := e.do(t, req)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("空内容应 400，得到 %d %s", resp.StatusCode, body)
	}

	big := bytes.Repeat([]byte("x"), int(e.cfg.MaxFileBytes())+10)
	buf, ct = buildMultipart(t, "太大", map[string][]byte{"big.bin": big})
	req, _ = http.NewRequest(http.MethodPost, e.srv.URL+"/api/items", buf)
	req.Header.Set("Content-Type", ct)
	resp, body = e.do(t, req)
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("超大文件应 413，得到 %d %s", resp.StatusCode, body)
	}
	resp, body = e.do(t, e.newJSONReq(t, http.MethodGet, "/api/items", nil))
	if strings.Contains(string(body), "太大") {
		t.Fatalf("失败的上传不应留下内容: %s", body)
	}
}

func TestItemRequiresAuth(t *testing.T) {
	e := newEnv(t)
	resp, _ := e.doWith(t, e.clientNoJar, e.newJSONReq(t, http.MethodGet, "/api/items", nil))
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("未登录访问内容应 401，得到 %d", resp.StatusCode)
	}
}

func TestDownloadStreamsAndCounts(t *testing.T) {
	e := newEnv(t)
	e.loginAdmin(t)

	content := []byte(strings.Repeat("download-me-", 2000))
	e.upload(t, "下载测试", map[string][]byte{"中文 名称.bin": content})

	_, body := e.do(t, e.newJSONReq(t, http.MethodGet, "/api/items", nil))
	var list struct {
		Items []store.Item `json:"items"`
	}
	if err := json.Unmarshal(body, &list); err != nil {
		t.Fatal(err)
	}
	it := list.Items[0]
	f := it.Files[0]

	req, _ := http.NewRequest(http.MethodGet, e.srv.URL+"/api/items/"+it.ID+"/files/"+f.ID, nil)
	resp, got := e.do(t, req)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("下载应 200，得到 %d %s", resp.StatusCode, got)
	}
	if !bytes.Equal(got, content) {
		t.Fatal("下载内容与上传不一致")
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/octet-stream" {
		t.Fatalf("Content-Type 应为 octet-stream，得到 %q", ct)
	}
	cd := resp.Header.Get("Content-Disposition")
	if !strings.HasPrefix(cd, "attachment;") || !strings.Contains(cd, "filename*=UTF-8''") {
		t.Fatalf("Content-Disposition 不对: %q", cd)
	}

	// Range 支持
	req, _ = http.NewRequest(http.MethodGet, e.srv.URL+"/api/items/"+it.ID+"/files/"+f.ID, nil)
	req.Header.Set("Range", "bytes=0-9")
	resp, part := e.do(t, req)
	if resp.StatusCode != http.StatusPartialContent {
		t.Fatalf("Range 请求应 206，得到 %d", resp.StatusCode)
	}
	if !bytes.Equal(part, content[:10]) {
		t.Fatalf("Range 内容不对: %q", part)
	}

	req, _ = http.NewRequest(http.MethodGet, e.srv.URL+"/api/items/"+it.ID+"/files/fdoesnotexist", nil)
	resp, _ = e.do(t, req)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("不存在的文件应 404，得到 %d", resp.StatusCode)
	}

	// 下载计数应累加（含 Range 那次）
	_, body = e.do(t, e.newJSONReq(t, http.MethodGet, "/api/items/"+it.ID, nil))
	after := decodeItem(t, body)
	if after.Files[0].Downloads != 2 {
		t.Fatalf("下载计数应为 2，得到 %d", after.Files[0].Downloads)
	}
}

func TestDownloadInlinePreview(t *testing.T) {
	e := newEnv(t)
	e.loginAdmin(t)

	jpeg := []byte("\xff\xd8\xff\xe0fake-jpeg")
	e.upload(t, "预览", map[string][]byte{
		"照片.JPG": jpeg,
		"clip.mp4": []byte("fake-mp4-body"),
		"说明.txt": []byte("hello"),
		"图标.svg": []byte("<svg xmlns='http://www.w3.org/2000/svg'/>"),
	})

	_, body := e.do(t, e.newJSONReq(t, http.MethodGet, "/api/items", nil))
	var list struct {
		Items []store.Item `json:"items"`
	}
	if err := json.Unmarshal(body, &list); err != nil || len(list.Items) != 1 {
		t.Fatalf("解析列表失败: %v (%s)", err, body)
	}
	it := list.Items[0]
	byName := map[string]store.File{}
	for _, f := range it.Files {
		byName[f.Name] = f
	}
	fileURL := func(f store.File, query string) string {
		return e.srv.URL + "/api/items/" + it.ID + "/files/" + f.ID + query
	}

	// 图片 inline=1：正确 MIME + inline，内容不变
	req, _ := http.NewRequest(http.MethodGet, fileURL(byName["照片.JPG"], "?inline=1"), nil)
	resp, got := e.do(t, req)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("内联预览应 200，得到 %d %s", resp.StatusCode, got)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "image/jpeg" {
		t.Fatalf("图片 Content-Type 应为 image/jpeg，得到 %q", ct)
	}
	if cd := resp.Header.Get("Content-Disposition"); !strings.HasPrefix(cd, "inline;") {
		t.Fatalf("内联预览 Content-Disposition 应为 inline，得到 %q", cd)
	}
	if !bytes.Equal(got, jpeg) {
		t.Fatal("内联预览内容与上传不一致")
	}
	if cc := resp.Header.Get("Cache-Control"); cc == "no-store" {
		t.Fatalf("内联预览应允许浏览器缓存，得到 %q", cc)
	}

	// 视频预览仍需支持 Range（播放器会分段请求）
	req, _ = http.NewRequest(http.MethodGet, fileURL(byName["clip.mp4"], "?inline=1"), nil)
	req.Header.Set("Range", "bytes=0-3")
	resp, part := e.do(t, req)
	if resp.StatusCode != http.StatusPartialContent {
		t.Fatalf("视频预览 Range 应 206，得到 %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "video/mp4" {
		t.Fatalf("视频 Content-Type 应为 video/mp4，得到 %q", ct)
	}
	if !bytes.Equal(part, []byte("fake")) {
		t.Fatalf("Range 内容不对: %q", part)
	}

	// 白名单外的类型即使带 inline=1 也回退为附件下载
	req, _ = http.NewRequest(http.MethodGet, fileURL(byName["说明.txt"], "?inline=1"), nil)
	resp, _ = e.do(t, req)
	if ct := resp.Header.Get("Content-Type"); ct != "application/octet-stream" {
		t.Fatalf("非白名单类型应回退 octet-stream，得到 %q", ct)
	}
	if cd := resp.Header.Get("Content-Disposition"); !strings.HasPrefix(cd, "attachment;") {
		t.Fatalf("非白名单类型应为附件，得到 %q", cd)
	}

	// SVG 可以内嵌脚本：内联下发时额外带沙箱 CSP
	req, _ = http.NewRequest(http.MethodGet, fileURL(byName["图标.svg"], "?inline=1"), nil)
	resp, _ = e.do(t, req)
	if ct := resp.Header.Get("Content-Type"); ct != "image/svg+xml" {
		t.Fatalf("SVG Content-Type 应为 image/svg+xml，得到 %q", ct)
	}
	if csp := resp.Header.Get("Content-Security-Policy"); !strings.Contains(csp, "sandbox") {
		t.Fatalf("SVG 内联应带沙箱 CSP，得到 %q", csp)
	}

	// inline=0 显式关闭预览
	req, _ = http.NewRequest(http.MethodGet, fileURL(byName["照片.JPG"], "?inline=0"), nil)
	resp, _ = e.do(t, req)
	if cd := resp.Header.Get("Content-Disposition"); !strings.HasPrefix(cd, "attachment;") {
		t.Fatalf("inline=0 应为附件，得到 %q", cd)
	}

	// 预览不计入下载次数（图片 1 次预览 + 视频 Range 1 次 = 0），只有 inline=0 那次计 1
	_, body = e.do(t, e.newJSONReq(t, http.MethodGet, "/api/items/"+it.ID, nil))
	after := decodeItem(t, body)
	for _, f := range after.Files {
		switch f.Name {
		case "照片.JPG":
			if f.Downloads != 1 {
				t.Fatalf("图片下载次数应为 1（仅 inline=0），得到 %d", f.Downloads)
			}
		case "说明.txt":
			// 非白名单类型 inline=1 回退为附件，按下载计数
			if f.Downloads != 1 {
				t.Fatalf("回退为附件的下载次数应为 1，得到 %d", f.Downloads)
			}
		default:
			if f.Downloads != 0 {
				t.Fatalf("%s 不应计入下载次数，得到 %d", f.Name, f.Downloads)
			}
		}
	}
}

func TestDeleteSingleFile(t *testing.T) {
	e := newEnv(t)
	e.loginAdmin(t)
	e.upload(t, "两个附件", map[string][]byte{"a.txt": []byte("aaa"), "b.txt": []byte("bbb")})

	_, body := e.do(t, e.newJSONReq(t, http.MethodGet, "/api/items", nil))
	var list struct {
		Items []store.Item `json:"items"`
	}
	if err := json.Unmarshal(body, &list); err != nil {
		t.Fatal(err)
	}
	it := list.Items[0]
	if len(it.Files) != 2 {
		t.Fatalf("应有 2 个附件: %+v", it.Files)
	}
	resp, _ := e.do(t, e.newJSONReq(t, http.MethodDelete, "/api/items/"+it.ID+"/files/"+it.Files[0].ID, nil))
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("删除附件应 204，得到 %d", resp.StatusCode)
	}
	_, body = e.do(t, e.newJSONReq(t, http.MethodGet, "/api/items/"+it.ID, nil))
	after := decodeItem(t, body)
	if len(after.Files) != 1 {
		t.Fatalf("应剩 1 个附件: %+v", after.Files)
	}
}

func TestSSEPushesEvents(t *testing.T) {
	e := newEnv(t)
	e.loginAdmin(t)

	req, _ := http.NewRequest(http.MethodGet, e.srv.URL+"/api/events", nil)
	resp, err := e.client.Do(req)
	if err != nil {
		t.Fatalf("连接 SSE 失败: %v", err)
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("SSE Content-Type 不对: %q", ct)
	}

	type sseEvent struct{ name, data string }
	events := make(chan sseEvent, 16)
	go func() {
		sc := bufio.NewScanner(resp.Body)
		var cur sseEvent
		for sc.Scan() {
			line := sc.Text()
			switch {
			case strings.HasPrefix(line, "event: "):
				cur.name = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: "):
				cur.data = strings.TrimPrefix(line, "data: ")
			case line == "":
				if cur.name != "" {
					events <- cur
				}
				cur = sseEvent{}
			}
		}
		close(events)
	}()

	// 等订阅在服务端建立（Do 返回时响应头已发出，订阅紧随其后）
	time.Sleep(100 * time.Millisecond)
	deadline := time.After(3 * time.Second)
	e.upload(t, "实时推送", nil)

	select {
	case ev, ok := <-events:
		if !ok {
			t.Fatal("SSE 连接被关闭")
		}
		if ev.name != "item.created" || !strings.Contains(ev.data, "实时推送") {
			t.Fatalf("事件内容不对: %+v", ev)
		}
	case <-deadline:
		t.Fatal("3 秒内未收到 SSE 事件")
	}
}

type userOut struct {
	Username string `json:"username"`
	IsAdmin  bool   `json:"is_admin"`
}

func TestAdminUserManagement(t *testing.T) {
	e := newEnv(t)
	e.loginAdmin(t)

	// 非管理员不能访问用户管理
	if err := e.auth.CreateUser("bob", "bobpassword", false); err != nil {
		t.Fatal(err)
	}
	e.login(t, "bob", "bobpassword")
	resp, _ := e.do(t, e.newJSONReq(t, http.MethodGet, "/api/users", nil))
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("普通用户访问用户管理应 403，得到 %d", resp.StatusCode)
	}
	e.loginAdmin(t)

	// 新增用户
	resp, body := e.do(t, e.newJSONReq(t, http.MethodPost, "/api/users",
		map[string]any{"username": "carol", "password": "carolpassword", "is_admin": false}))
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("新增用户应 201，得到 %d %s", resp.StatusCode, body)
	}
	// 重名
	resp, _ = e.do(t, e.newJSONReq(t, http.MethodPost, "/api/users",
		map[string]any{"username": "carol", "password": "carolpassword"}))
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("重名应 409，得到 %d", resp.StatusCode)
	}
	// 弱密码
	resp, _ = e.do(t, e.newJSONReq(t, http.MethodPost, "/api/users",
		map[string]any{"username": "dave", "password": "123"}))
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("弱密码应 400，得到 %d", resp.StatusCode)
	}

	// 列表不含哈希
	resp, body = e.do(t, e.newJSONReq(t, http.MethodGet, "/api/users", nil))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("列表应 200，得到 %d", resp.StatusCode)
	}
	if bytes.Contains(body, []byte("hash")) || bytes.Contains(body, []byte("salt")) {
		t.Fatalf("用户列表不应泄漏哈希: %s", body)
	}
	var users []userOut
	if err := json.Unmarshal(body, &users); err != nil {
		t.Fatalf("解析用户列表失败: %v (%s)", err, body)
	}
	if len(users) != 3 {
		t.Fatalf("应有 3 个用户，得到 %d: %s", len(users), body)
	}

	// 改密
	resp, _ = e.do(t, e.newJSONReq(t, http.MethodPost, "/api/users/carol/password",
		map[string]any{"password": "newcarolpassword"}))
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("改密应 204，得到 %d", resp.StatusCode)
	}
	if _, ok := e.auth.Verify("carol", "newcarolpassword"); !ok {
		t.Fatal("新密码未生效")
	}

	// 不能删自己
	resp, _ = e.do(t, e.newJSONReq(t, http.MethodDelete, "/api/users/admin", nil))
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("删除自己应 403，得到 %d", resp.StatusCode)
	}
	// 删除普通用户
	resp, _ = e.do(t, e.newJSONReq(t, http.MethodDelete, "/api/users/carol", nil))
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("删除普通用户应 204，得到 %d", resp.StatusCode)
	}
	resp, _ = e.do(t, e.newJSONReq(t, http.MethodDelete, "/api/users/carol", nil))
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("重复删除应 404，得到 %d", resp.StatusCode)
	}
}

func TestCannotDeleteLastAdmin(t *testing.T) {
	e := newEnv(t)
	if err := e.auth.CreateUser("boss2", "boss2password", true); err != nil {
		t.Fatal(err)
	}
	e.login(t, "boss2", "boss2password")

	// boss2 删 admin 允许（还剩 boss2 是管理员）
	resp, _ := e.do(t, e.newJSONReq(t, http.MethodDelete, "/api/users/admin", nil))
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("删除另一管理员应 204，得到 %d", resp.StatusCode)
	}
	// 只剩自己，删自己应报"不能删除自己"
	resp, body := e.do(t, e.newJSONReq(t, http.MethodDelete, "/api/users/boss2", nil))
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("删除自己应 403，得到 %d %s", resp.StatusCode, body)
	}
}

func TestChangeOwnPassword(t *testing.T) {
	e := newEnv(t)
	e.loginAdmin(t)

	resp, _ := e.do(t, e.newJSONReq(t, http.MethodPost, "/api/password",
		map[string]string{"old": "wrong", "new": "newpassword"}))
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("旧密码错误应 403，得到 %d", resp.StatusCode)
	}
	resp, _ = e.do(t, e.newJSONReq(t, http.MethodPost, "/api/password",
		map[string]string{"old": e.adminPassword, "new": "newpassword"}))
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("改密应 204，得到 %d", resp.StatusCode)
	}
	// 会话已被踢，需重新登录
	resp, _ = e.do(t, e.newJSONReq(t, http.MethodGet, "/api/me", nil))
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("改密后旧会话应失效，得到 %d", resp.StatusCode)
	}
}

func TestUploadBadNameMapsTo400(t *testing.T) {
	e := newEnv(t)
	e.loginAdmin(t)

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	part, err := mw.CreateFormFile("files", "***")
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
		t.Fatalf("非法文件名应 400，得到 %d %s", resp.StatusCode, body)
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
}

func TestUploadTooManyFilesMessageUsesConfig(t *testing.T) {
	e := newEnv(t)
	e.loginAdmin(t)

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for i := 0; i <= e.cfg.MaxFilesPerItem; i++ {
		part, err := mw.CreateFormFile("files", "f.txt")
		if err != nil {
			t.Fatal(err)
		}
		part.Write([]byte("x"))
	}
	mw.Close()

	req, err := http.NewRequest(http.MethodPost, e.srv.URL+"/api/items", &buf)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, body := e.do(t, req)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("超过附件数量应 400，得到 %d %s", resp.StatusCode, body)
	}
	if !bytes.Contains(body, []byte(fmt.Sprintf("不能超过 %d 个", e.cfg.MaxFilesPerItem))) {
		t.Fatalf("文案应带上实际上限 %d: %s", e.cfg.MaxFilesPerItem, body)
	}
}

func TestUploadItemTooLargeMapsTo413(t *testing.T) {
	e := newEnv(t)
	e.loginAdmin(t)

	// 夹具 MaxItemSizeMB=16、MaxFileSizeMB=8。同一条内容里先放 8MiB + 4MiB，
	// 再用 5MiB 触发：单文件没超 8MiB，但单条只剩 4MiB，必须命中 ErrItemTooLarge 而不是 ErrTooLarge。
	// 三个 part 必须在同一次请求里，否则每次上传都会新建一条内容，额度不会累积。
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for _, f := range []struct {
		name string
		size int
	}{{"a.bin", 8 << 20}, {"b.bin", 4 << 20}, {"c.bin", 5 << 20}} {
		part, err := mw.CreateFormFile("files", f.name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := part.Write(bytes.Repeat([]byte("x"), f.size)); err != nil {
			t.Fatal(err)
		}
	}
	mw.Close()

	req, err := http.NewRequest(http.MethodPost, e.srv.URL+"/api/items", &buf)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, body := e.do(t, req)
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("超过单条总大小应 413，得到 %d %s", resp.StatusCode, body)
	}
	if !bytes.Contains(body, []byte("item_too_large")) {
		t.Fatalf("错误码应为 item_too_large: %s", body)
	}
	if !bytes.Contains(body, []byte(fmt.Sprintf("%dMB", e.cfg.MaxItemSizeMB))) {
		t.Fatalf("文案应带上单条上限 %dMB: %s", e.cfg.MaxItemSizeMB, body)
	}
}

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
	if _, err := part.Write([]byte("x")); err != nil {
		t.Fatal(err)
	}
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
	if !bytes.Contains(body, []byte("no_files")) {
		t.Fatalf("错误码应为 no_files: %s", body)
	}
}

func TestDownloadZipRequiresAuth(t *testing.T) {
	e := newEnv(t)
	resp, body := e.doWith(t, e.clientNoJar, e.newJSONReq(t, http.MethodGet, "/api/items/anything/zip", nil))
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("未登录应 401，得到 %d %s", resp.StatusCode, body)
	}
}

func TestDownloadZipMissingFileIsNotFound(t *testing.T) {
	e := newEnv(t)
	e.loginAdmin(t)
	it := e.uploadParts(t, []uploadPart{{field: "files", filename: "a.txt", body: "aaa"}})

	// 绕过 store 直接删掉磁盘文件，模拟磁盘被外部清理。
	disk, _, err := e.store.FilePath(it.ID, it.Files[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(disk); err != nil {
		t.Fatal(err)
	}

	resp, body := e.do(t, e.newJSONReq(t, http.MethodGet, "/api/items/"+it.ID+"/zip", nil))
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("附件缺失应 404，得到 %d %s", resp.StatusCode, body)
	}
	if !bytes.Contains(body, []byte("file_missing")) {
		t.Fatalf("错误码应为 file_missing: %s", body)
	}
}

func TestDownloadZipSetsNoStore(t *testing.T) {
	e := newEnv(t)
	e.loginAdmin(t)
	it := e.uploadParts(t, []uploadPart{{field: "files", filename: "a.txt", body: "a"}})

	// 走真实 HTTP 时，net/http 会对小体积响应自动补上 Content-Length，
	// 断言会误报；这里直接调用 handler，只验证它自己没有显式设置该头。
	u, err := url.Parse(e.srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/items/"+it.ID+"/zip", nil)
	for _, ck := range e.client.Jar.Cookies(u) {
		req.AddCookie(ck)
	}
	rec := httptest.NewRecorder()
	e.srv.Config.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("zip 应 200，得到 %d %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control 应为 no-store，得到 %q", got)
	}
	if got := rec.Header().Get("Content-Length"); got != "" {
		t.Fatalf("handler 不应显式设置 Content-Length，得到 %q", got)
	}
}

func TestHealthzIsPublic(t *testing.T) {
	e := newEnv(t)
	resp, body := e.doWith(t, e.clientNoJar, e.newJSONReq(t, http.MethodGet, "/api/healthz", nil))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("healthz 应公开可访问，得到 %d %s", resp.StatusCode, body)
	}
	var got struct {
		OK      bool   `json:"ok"`
		Name    string `json:"name"`
		Version string `json:"version"`
	}
	if err := json.Unmarshal(body, &got); err != nil || !got.OK || got.Name != "lanbox" || got.Version != "test" {
		t.Fatalf("healthz 响应不对: %s", body)
	}
	if resp.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("healthz 应 no-store，得到 %q", resp.Header.Get("Cache-Control"))
	}
}

func TestConfigRequiresAdmin(t *testing.T) {
	e := newEnv(t)
	e.loginAdmin(t)
	resp, body := e.do(t, e.newJSONReq(t, http.MethodPost, "/api/users", map[string]any{
		"username": "bob", "password": "password123", "is_admin": false,
	}))
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("创建普通用户失败: %d %s", resp.StatusCode, body)
	}
	e.do(t, e.newJSONReq(t, http.MethodPost, "/api/logout", nil))
	e.login(t, "bob", "password123")

	resp, body = e.do(t, e.newJSONReq(t, http.MethodGet, "/api/config", nil))
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("普通成员读配置应 403，得到 %d %s", resp.StatusCode, body)
	}
	resp, body = e.do(t, e.newJSONReq(t, http.MethodPut, "/api/config", map[string]any{
		"values": map[string]any{"max_text_bytes": 10},
	}))
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("普通成员写配置应 403，得到 %d %s", resp.StatusCode, body)
	}
	resp, body = e.do(t, e.newJSONReq(t, http.MethodPost, "/api/config/restart", nil))
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("普通成员重启应 403，得到 %d %s", resp.StatusCode, body)
	}
}

func TestPutConfigHotApplyAndRestart(t *testing.T) {
	e := newEnv(t)
	e.loginAdmin(t)

	resp, body := e.do(t, e.newJSONReq(t, http.MethodPut, "/api/config", map[string]any{
		"values": map[string]any{"max_text_bytes": 5, "listen": ":0"},
	}))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("保存应成功，得到 %d %s", resp.StatusCode, body)
	}
	var res struct {
		Changed         []string `json:"changed"`
		Applied         []string `json:"applied"`
		RestartRequired []string `json:"restart_required"`
	}
	if err := json.Unmarshal(body, &res); err != nil {
		t.Fatal(err)
	}
	if len(res.Applied) != 1 || res.Applied[0] != "max_text_bytes" {
		t.Fatalf("applied 不对: %+v", res.Applied)
	}
	if len(res.RestartRequired) != 1 || res.RestartRequired[0] != "listen" {
		t.Fatalf("restart_required 不对: %+v", res.RestartRequired)
	}

	if _, err := e.store.Create(store.CreateInput{Text: "0123456789"}); !errors.Is(err, store.ErrTextTooLong) {
		t.Fatalf("max_text_bytes 未热更，得到 %v", err)
	}

	resp, body = e.do(t, e.newJSONReq(t, http.MethodGet, "/api/config", nil))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("读配置失败: %d %s", resp.StatusCode, body)
	}
	var got struct {
		Origins map[string]string `json:"origins"`
		Paths   map[string]string `json:"paths"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	if got.Origins["max_text_bytes"] != "web" || got.Paths["web_config"] == "" {
		t.Fatalf("来源/路径不对: %+v", got)
	}

	resp, body = e.do(t, e.newJSONReq(t, http.MethodPost, "/api/config/restart", nil))
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("重启应 202，得到 %d %s", resp.StatusCode, body)
	}
	if *e.restarts != 1 {
		t.Fatalf("Restart 钩子应调用一次，得到 %d", *e.restarts)
	}
	resp, body = e.do(t, e.newJSONReq(t, http.MethodPost, "/api/config/restart", nil))
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("冷却期内应 429，得到 %d %s", resp.StatusCode, body)
	}
}

func TestPutConfigRejectsInvalid(t *testing.T) {
	e := newEnv(t)
	e.loginAdmin(t)

	resp, body := e.do(t, e.newJSONReq(t, http.MethodPut, "/api/config", map[string]any{
		"values": map[string]any{"max_files_per_item": 0},
	}))
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("非法值应 400，得到 %d %s", resp.StatusCode, body)
	}

	resp, body = e.do(t, e.newJSONReq(t, http.MethodPut, "/api/config", map[string]any{
		"values": map[string]any{"no_such_field": 1},
	}))
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("未知字段应 400，得到 %d %s", resp.StatusCode, body)
	}
}

// TestStatsVisibility 断言统计接口的鉴权与 admin 分块。
func TestStatsVisibility(t *testing.T) {
	e := newEnv(t)

	// 未登录 → 401。
	resp, body := e.do(t, e.newJSONReq(t, http.MethodGet, "/api/stats", nil))
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("未登录访问 /api/stats 应 401，得到 %d %s", resp.StatusCode, body)
	}

	e.loginAdmin(t)
	if _, err := e.store.Create(store.CreateInput{Text: "只有文字", CreatedBy: "admin"}); err != nil {
		t.Fatal(err)
	}
	it, err := e.store.Create(store.CreateInput{Text: "带附件", CreatedBy: "admin"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.store.AddFile(it.ID, "a.txt", 3, strings.NewReader("abc")); err != nil {
		t.Fatal(err)
	}
	if _, err := e.store.SetTaken(it.ID, true, "admin"); err != nil {
		t.Fatal(err)
	}

	resp, body = e.do(t, e.newJSONReq(t, http.MethodGet, "/api/stats", nil))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("管理员访问 /api/stats 应 200，得到 %d %s", resp.StatusCode, body)
	}
	if cc := resp.Header.Get("Cache-Control"); cc != "no-store" {
		t.Fatalf("应禁用缓存，得到 Cache-Control=%q", cc)
	}
	var out struct {
		Version string `json:"version"`
		UptimeS int64  `json:"uptime_s"`
		Items   struct{ Total, Todo, Taken int }
		Files   struct {
			Count int
			Bytes int64
		}
		Admin *struct {
			Users int `json:"users"`
			Disk  struct {
				FreeBytes *int64 `json:"free_bytes"`
				Error     string `json:"error"`
			} `json:"disk"`
			ItemsDir string              `json:"items_dir"`
			Listen   string              `json:"listen"`
			MDNS     *httpapi.MDNSStatus `json:"mdns"`
		} `json:"admin"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("解析统计响应失败: %v (%s)", err, body)
	}
	if out.Version != "test" {
		t.Fatalf("版本应为 test，得到 %q", out.Version)
	}
	if out.UptimeS < 0 {
		t.Fatalf("运行时长异常: %d", out.UptimeS)
	}
	if out.Items.Total != 2 || out.Items.Todo != 1 || out.Items.Taken != 1 {
		t.Fatalf("内容统计不对: %+v", out.Items)
	}
	if out.Files.Count != 1 || out.Files.Bytes != 3 {
		t.Fatalf("附件统计不对: %+v", out.Files)
	}
	if out.Admin == nil {
		t.Fatal("管理员应看到 admin 块")
	}
	if out.Admin.Users != 1 {
		t.Fatalf("用户数应为 1，得到 %d", out.Admin.Users)
	}
	if want := filepath.Join(e.cfg.DataDir, "items"); out.Admin.ItemsDir != want {
		t.Fatalf("存储路径应为 %q，得到 %q", want, out.Admin.ItemsDir)
	}
	if out.Admin.Listen != e.cfg.Listen {
		t.Fatalf("监听地址应为 %q，得到 %q", e.cfg.Listen, out.Admin.Listen)
	}
	if out.Admin.Disk.FreeBytes == nil && out.Admin.Disk.Error == "" {
		t.Fatalf("磁盘字段既没有剩余空间也没有错误: %+v", out.Admin.Disk)
	}
	if out.Admin.MDNS != nil {
		t.Fatalf("未注入 mDNS 回调时应省略 mdns，得到 %+v", out.Admin.MDNS)
	}

	// 普通用户：同一接口可用，但没有 admin 键。
	if err := e.auth.CreateUser("bob", "bobpassword", false); err != nil {
		t.Fatal(err)
	}
	e.login(t, "bob", "bobpassword")
	resp, body = e.do(t, e.newJSONReq(t, http.MethodGet, "/api/stats", nil))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("普通用户访问 /api/stats 应 200，得到 %d %s", resp.StatusCode, body)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		t.Fatalf("解析统计响应失败: %v (%s)", err, body)
	}
	if _, ok := raw["admin"]; ok {
		t.Fatalf("普通用户不应看到 admin 键: %s", body)
	}
	for _, key := range []string{"version", "uptime_s", "items", "files"} {
		if _, ok := raw[key]; !ok {
			t.Fatalf("普通用户应看到 %q: %s", key, body)
		}
	}
}

// TestStatsDiskErrorDegrades 断言磁盘探测失败只降级 disk 字段，不返回 500。
func TestStatsDiskErrorDegrades(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "items"), store.Options{
		FreeSpace: func(string) (int64, error) { return 0, errors.New("statfs 不可用") },
	})
	if err != nil {
		t.Fatalf("打开 store 失败: %v", err)
	}
	am, err := auth.Open(filepath.Join(dir, "users.json"), time.Hour)
	if err != nil {
		t.Fatalf("打开 auth 失败: %v", err)
	}
	pw, _, err := am.BootstrapAdmin()
	if err != nil {
		t.Fatalf("初始化管理员失败: %v", err)
	}
	cfg := config.Defaults()
	cfg.DataDir = dir
	srv := httptest.NewTLSServer(httpapi.New(httpapi.Options{
		Cfg: cfg, Store: st, Auth: am, Web: lanbox.WebFS(), Log: log.New(io.Discard, "", 0),
	}))
	t.Cleanup(srv.Close)

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	httpc := &http.Client{Jar: jar, Transport: srv.Client().Transport}
	loginBody := strings.NewReader(fmt.Sprintf(`{"username":"admin","password":%q}`, pw))
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/api/login", loginBody)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := httpc.Do(req)
	if err != nil {
		t.Fatalf("登录失败: %v", err)
	}
	resp.Body.Close()

	resp, err = httpc.Get(srv.URL + "/api/stats")
	if err != nil {
		t.Fatalf("请求 /api/stats 失败: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("磁盘探测失败不应影响接口，得到 %d %s", resp.StatusCode, body)
	}
	var out struct {
		Admin struct {
			Disk struct {
				FreeBytes *int64 `json:"free_bytes"`
				Error     string `json:"error"`
			} `json:"disk"`
		} `json:"admin"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("解析响应失败: %v (%s)", err, body)
	}
	if out.Admin.Disk.Error != "statfs 不可用" {
		t.Fatalf("应带上探测错误，得到 %q", out.Admin.Disk.Error)
	}
	if out.Admin.Disk.FreeBytes != nil {
		t.Fatalf("探测失败时不应有 free_bytes，得到 %v", *out.Admin.Disk.FreeBytes)
	}
}
