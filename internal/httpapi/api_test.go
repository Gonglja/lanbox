package httpapi_test

import (
	"bytes"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"path/filepath"
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
