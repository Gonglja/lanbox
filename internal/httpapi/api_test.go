package httpapi_test

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
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
}

func newEnv(t *testing.T) *env {
	t.Helper()
	cfg := config.Defaults()
	cfg.DataDir = t.TempDir()
	cfg.MaxFileSizeMB = 8
	cfg.MinFreeDiskMB = 0

	st, err := store.Open(filepath.Join(cfg.DataDir, "items"), store.Options{
		MaxFileBytes: cfg.MaxFileBytes(),
		MinFreeBytes: 0,
		MaxTextBytes: cfg.MaxTextBytes,
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
	s := httpapi.New(httpapi.Options{Cfg: cfg, Store: st, Auth: am, Web: lanbox.WebFS(), Log: log.New(io.Discard, "", 0)})
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
