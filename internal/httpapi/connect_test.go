package httpapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"lanbox/internal/httpapi"
)

func TestConnectInfo(t *testing.T) {
	e := newEnv(t)

	resp, body := e.do(t, e.newJSONReq(t, http.MethodGet, "/api/connect", nil))
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("未登录应 401，得到 %d %s", resp.StatusCode, body)
	}

	e.loginAdmin(t)
	resp, body = e.do(t, e.newJSONReq(t, http.MethodGet, "/api/connect", nil))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("应 200，得到 %d %s", resp.StatusCode, body)
	}
	var info httpapi.ConnectInfo
	if err := json.Unmarshal(body, &info); err != nil {
		t.Fatalf("解析失败: %v (%s)", err, body)
	}
	if info.Name == "" || info.Version != "test" {
		t.Fatalf("名称/版本不对: %+v", info)
	}
	if len(info.URLs) == 0 {
		t.Fatalf("应至少给出一个地址: %s", body)
	}
	seen := map[string]bool{}
	for _, u := range info.URLs {
		parsed, err := url.Parse(u.URL)
		if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
			t.Fatalf("地址不合法: %q", u.URL)
		}
		if u.Label == "" || u.Kind == "" {
			t.Fatalf("地址缺少标签/类型: %+v", u)
		}
		if seen[u.URL] {
			t.Fatalf("地址重复: %s", u.URL)
		}
		seen[u.URL] = true
	}
	if !seen[info.Preferred] {
		t.Fatalf("preferred=%q 不在地址列表里", info.Preferred)
	}
	// 请求用的是 httptest 的回环地址：它必须被排在最后（手机连不上）
	if info.URLs[len(info.URLs)-1].Kind != "request" || !strings.Contains(info.URLs[len(info.URLs)-1].Label, "仅本机") {
		t.Fatalf("回环地址应排在最后并标注仅本机: %+v", info.URLs[len(info.URLs)-1])
	}
}

func TestConnectQR(t *testing.T) {
	e := newEnv(t)
	e.loginAdmin(t)

	resp, body := e.do(t, e.newJSONReq(t, http.MethodGet, "/api/connect/qr.svg", nil))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("应 200，得到 %d %s", resp.StatusCode, body)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "image/svg+xml") {
		t.Fatalf("Content-Type 应为 image/svg+xml，得到 %q", ct)
	}
	svg := string(body)
	if !strings.HasPrefix(svg, "<svg ") || !strings.HasSuffix(svg, "</svg>") {
		t.Fatalf("不是 SVG: %.60s", svg)
	}
	if !strings.Contains(svg, `shape-rendering="crispEdges"`) || !strings.Contains(svg, "<path ") {
		t.Fatalf("SVG 缺少模块内容")
	}
	if csp := resp.Header.Get("Content-Security-Policy"); !strings.Contains(csp, "sandbox") {
		t.Fatalf("SVG 应带 sandbox CSP，得到 %q", csp)
	}

	// 只接受 /api/connect 里出现过的地址
	resp, body = e.do(t, e.newJSONReq(t, http.MethodGet, "/api/connect/qr.svg?url=https://evil.example/", nil))
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("任意外部地址应 400，得到 %d %s", resp.StatusCode, body)
	}
}

func TestConnectQRAcceptsListedURL(t *testing.T) {
	e := newEnv(t)
	e.loginAdmin(t)

	_, body := e.do(t, e.newJSONReq(t, http.MethodGet, "/api/connect", nil))
	var info httpapi.ConnectInfo
	if err := json.Unmarshal(body, &info); err != nil {
		t.Fatal(err)
	}
	target := info.URLs[0].URL
	resp, svg := e.do(t, e.newJSONReq(t, http.MethodGet, "/api/connect/qr.svg?url="+url.QueryEscape(target), nil))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("列表内地址应 200，得到 %d %s", resp.StatusCode, svg)
	}
}

func TestDiscoverUnavailableWithoutFunc(t *testing.T) {
	e := newEnv(t)
	e.loginAdmin(t)
	resp, body := e.do(t, e.newJSONReq(t, http.MethodGet, "/api/discover", nil))
	if resp.StatusCode != http.StatusNotImplemented {
		t.Fatalf("没注入 Discover 时应 501，得到 %d %s", resp.StatusCode, body)
	}
}

func TestDiscoverListsServersAndCaches(t *testing.T) {
	var calls int32
	fake := func(ctx context.Context) ([]httpapi.DiscoveredServer, error) {
		atomic.AddInt32(&calls, 1)
		return []httpapi.DiscoveredServer{
			{Name: "客厅", Host: "lanbox", Port: 8443, URLs: []string{"https://lanbox.local:8443"}, Proto: "1", Fingerprint: "ab12", Current: true},
			{Name: "书房", Host: "nas", Port: 443, URLs: []string{"https://nas.local"}},
		}, nil
	}
	e := newEnvWith(t, func(o *httpapi.Options) { o.Discover = fake })
	e.loginAdmin(t)

	resp, body := e.do(t, e.newJSONReq(t, http.MethodGet, "/api/discover", nil))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("应 200，得到 %d %s", resp.StatusCode, body)
	}
	var out struct {
		Servers []httpapi.DiscoveredServer `json:"servers"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("解析失败: %v (%s)", err, body)
	}
	if len(out.Servers) != 2 || out.Servers[0].Name != "客厅" || !out.Servers[0].Current {
		t.Fatalf("实例列表不对: %s", body)
	}
	if out.Servers[1].Port != 443 || len(out.Servers[1].URLs) != 1 {
		t.Fatalf("第二个实例不对: %+v", out.Servers[1])
	}

	// 3 秒内重复请求应命中缓存，不再浏览一遍
	for i := 0; i < 3; i++ {
		if resp, body := e.do(t, e.newJSONReq(t, http.MethodGet, "/api/discover", nil)); resp.StatusCode != http.StatusOK {
			t.Fatalf("缓存命中时应 200，得到 %d %s", resp.StatusCode, body)
		}
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("3 秒内应只浏览 1 次，实际 %d 次", got)
	}
}

func TestDiscoverRequiresAuth(t *testing.T) {
	e := newEnvWith(t, func(o *httpapi.Options) {
		o.Discover = func(ctx context.Context) ([]httpapi.DiscoveredServer, error) {
			return nil, nil
		}
	})
	resp, body := e.do(t, e.newJSONReq(t, http.MethodGet, "/api/discover", nil))
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("未登录应 401，得到 %d %s", resp.StatusCode, body)
	}
}

// 空白结果也要返回 JSON 数组而不是 null，前端少一层判断。
func TestDiscoverEmptyResultShape(t *testing.T) {
	e := newEnvWith(t, func(o *httpapi.Options) {
		o.Discover = func(ctx context.Context) ([]httpapi.DiscoveredServer, error) { return nil, nil }
	})
	e.loginAdmin(t)
	resp, body := e.do(t, e.newJSONReq(t, http.MethodGet, "/api/discover", nil))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("应 200，得到 %d %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), `"servers":[]`) {
		t.Fatalf("空结果应是数组: %s", body)
	}
	_ = time.Second
}
