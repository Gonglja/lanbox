package httpapi_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// crossOrigin 是纯 web 客户端所在源的示例（与服务端 Host 不同）。
const testClientOrigin = "https://client.example:8443"

// loginCross 模拟跨域客户端登录：带 Origin，且不保存 cookie。
func (e *env) loginCross(t *testing.T) string {
	t.Helper()
	req := e.newJSONReq(t, http.MethodPost, "/api/login",
		map[string]string{"username": "admin", "password": e.adminPassword})
	req.Header.Set("Origin", testClientOrigin)
	resp, body := e.doWith(t, e.clientNoJar, req)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("跨域登录应 200，得到 %d %s", resp.StatusCode, body)
	}
	var out struct {
		Username  string `json:"username"`
		Token     string `json:"token"`
		ExpiresAt string `json:"expires_at"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("解析登录响应失败: %v (%s)", err, body)
	}
	if out.Token == "" || out.ExpiresAt == "" {
		t.Fatalf("跨域登录应下发令牌，得到 %s", body)
	}
	return out.Token
}

// bearerReq 构造一个只带 Authorization、不保存 cookie 的请求。
func (e *env) bearerReq(t *testing.T, method, path string, body string) *http.Request {
	t.Helper()
	var req *http.Request
	var err error
	if body == "" {
		req, err = http.NewRequest(method, e.srv.URL+path, nil)
	} else {
		req, err = http.NewRequest(method, e.srv.URL+path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
	}
	if err != nil {
		t.Fatal(err)
	}
	return req
}

func TestCORSPreflight(t *testing.T) {
	e := newEnv(t)

	req := e.bearerReq(t, http.MethodOptions, "/api/items", "")
	req.Header.Set("Origin", testClientOrigin)
	req.Header.Set("Access-Control-Request-Method", "POST")
	req.Header.Set("Access-Control-Request-Headers", "authorization,content-type")
	resp, body := e.doWith(t, e.clientNoJar, req)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("预检应 204，得到 %d %s", resp.StatusCode, body)
	}
	h := resp.Header
	if got := h.Get("Access-Control-Allow-Origin"); got != "*" {
		t.Fatalf("Allow-Origin 应为 *，得到 %q", got)
	}
	if got := h.Get("Access-Control-Allow-Headers"); !strings.Contains(strings.ToLower(got), "authorization") {
		t.Fatalf("Allow-Headers 应包含 Authorization，得到 %q", got)
	}
	if got := h.Get("Access-Control-Allow-Methods"); !strings.Contains(got, "PATCH") {
		t.Fatalf("Allow-Methods 应包含 PATCH，得到 %q", got)
	}
	if got := h.Get("Access-Control-Expose-Headers"); !strings.Contains(got, "Content-Disposition") {
		t.Fatalf("Expose-Headers 应包含 Content-Disposition，得到 %q", got)
	}
	// 关键安全边界：绝不能允许跨域携带凭据（否则 sid cookie 可被第三方站点借用）
	if got := h.Get("Access-Control-Allow-Credentials"); got != "" {
		t.Fatalf("不应允许跨域凭据，得到 %q", got)
	}
}

func TestCORSNotAppliedToNonAPI(t *testing.T) {
	e := newEnv(t)
	resp, _ := e.doWith(t, e.clientNoJar, e.bearerReq(t, http.MethodGet, "/", ""))
	if got := resp.Header.Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("静态资源不应带 CORS 头，得到 %q", got)
	}
}

func TestBearerAuthFullFlow(t *testing.T) {
	e := newEnv(t)
	token := e.loginCross(t)

	// 未带令牌：一律 401
	resp, body := e.doWith(t, e.clientNoJar, e.bearerReq(t, http.MethodGet, "/api/me", ""))
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("无令牌应 401，得到 %d %s", resp.StatusCode, body)
	}

	req := e.bearerReq(t, http.MethodGet, "/api/me", "")
	req.Header.Set("Authorization", "Bearer "+token)
	resp, body = e.doWith(t, e.clientNoJar, req)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("Bearer 访问 /api/me 应 200，得到 %d %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), `"username":"admin"`) {
		t.Fatalf("身份不对: %s", body)
	}

	// 跨域写操作：Origin 与服务端不同源，但带令牌，CSRF 中间件必须放行
	buf, ct := buildMultipart(t, "来自纯 web 客户端", nil)
	req, err := http.NewRequest(http.MethodPost, e.srv.URL+"/api/items", buf)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", ct)
	req.Header.Set("Origin", testClientOrigin)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, body = e.doWith(t, e.clientNoJar, req)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("Bearer 上传应 201，得到 %d %s", resp.StatusCode, body)
	}

	// 无令牌的跨域写操作仍应被 CSRF 拦下
	req = e.bearerReq(t, http.MethodPost, "/api/items", `{"text":"x"}`)
	req.Header.Set("Origin", testClientOrigin)
	resp, body = e.doWith(t, e.clientNoJar, req)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("无令牌的跨域写操作应 403，得到 %d %s", resp.StatusCode, body)
	}
}

func TestTokenInQueryOnlyForSafeMethods(t *testing.T) {
	e := newEnv(t)
	token := e.loginCross(t)

	req := e.bearerReq(t, http.MethodGet, "/api/items?token="+token, "")
	resp, body := e.doWith(t, e.clientNoJar, req)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET + ?token= 应 200，得到 %d %s", resp.StatusCode, body)
	}

	// 写操作不接受查询串令牌，避免令牌进入历史记录/日志后还能被用来改数据
	req = e.bearerReq(t, http.MethodDelete, "/api/items/whatever?token="+token, "")
	resp, body = e.doWith(t, e.clientNoJar, req)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("DELETE + ?token= 应 401，得到 %d %s", resp.StatusCode, body)
	}
}

func TestSameOriginLoginDoesNotLeakToken(t *testing.T) {
	e := newEnv(t)
	// 同源请求不带 Origin（浏览器对同源 fetch 的行为），响应体里不应出现令牌
	resp, body := e.do(t, e.newJSONReq(t, http.MethodPost, "/api/login",
		map[string]string{"username": "admin", "password": e.adminPassword}))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("登录应 200，得到 %d %s", resp.StatusCode, body)
	}
	if strings.Contains(string(body), `"token"`) || strings.Contains(string(body), `"expires_at"`) {
		t.Fatalf("同源登录不应下发令牌: %s", body)
	}
	var out map[string]any
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if _, ok := out["username"]; !ok {
		t.Fatalf("字段集变了，可能影响旧前端: %s", body)
	}

	// 同时确认同源登录仍然下发 cookie
	found := false
	for _, ck := range resp.Cookies() {
		if ck.Name == "sid" && ck.Value != "" {
			found = true
		}
	}
	if !found {
		t.Fatal("同源登录应设置 sid cookie")
	}
}

func TestBearerLogoutRevokesSession(t *testing.T) {
	e := newEnv(t)
	token := e.loginCross(t)

	req := e.bearerReq(t, http.MethodPost, "/api/logout", "")
	req.Header.Set("Authorization", "Bearer "+token)
	if resp, body := e.doWith(t, e.clientNoJar, req); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("登出应 204，得到 %d %s", resp.StatusCode, body)
	}

	req = e.bearerReq(t, http.MethodGet, "/api/me", "")
	req.Header.Set("Authorization", "Bearer "+token)
	if resp, body := e.doWith(t, e.clientNoJar, req); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("登出后令牌应失效，得到 %d %s", resp.StatusCode, body)
	}
}

func TestNonBearerAuthorizationRejected(t *testing.T) {
	e := newEnv(t)
	req := e.bearerReq(t, http.MethodGet, "/api/me", "")
	req.Header.Set("Authorization", "Basic YWRtaW46YWRtaW4=")
	resp, body := e.doWith(t, e.clientNoJar, req)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("非 Bearer 凭据应 401，得到 %d %s", resp.StatusCode, body)
	}
}

// TestQueryTokenOnDownload 覆盖纯 web 客户端最常用的路径：
// <img>/<video>/<a download> 无法设置请求头，只能把令牌放进查询串。
func TestQueryTokenOnDownload(t *testing.T) {
	e := newEnv(t)
	e.loginAdmin(t)
	e.upload(t, "带文件", map[string][]byte{"a.txt": []byte("hello")})

	resp, body := e.do(t, e.newJSONReq(t, http.MethodGet, "/api/items", nil))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("列表应 200，得到 %d %s", resp.StatusCode, body)
	}
	var list struct {
		Items []struct {
			ID    string `json:"id"`
			Files []struct {
				ID string `json:"id"`
			} `json:"files"`
		} `json:"items"`
	}
	if err := json.Unmarshal(body, &list); err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	var itemID, fileID string
	for _, it := range list.Items {
		if len(it.Files) > 0 {
			itemID, fileID = it.ID, it.Files[0].ID
		}
	}
	if itemID == "" {
		t.Fatal("没有找到带附件的内容")
	}

	token := e.loginCross(t)
	req := e.bearerReq(t, http.MethodGet, "/api/items/"+itemID+"/files/"+fileID+"?token="+token, "")
	resp, dl := e.doWith(t, e.clientNoJar, req)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("查询串令牌下载应 200，得到 %d %s", resp.StatusCode, dl)
	}
	if string(dl) != "hello" {
		t.Fatalf("下载内容不对: %q", dl)
	}
	if got := resp.Header.Get("Access-Control-Allow-Origin"); got != "*" {
		t.Fatalf("下载响应也应带 CORS 头，得到 %q", got)
	}
}
