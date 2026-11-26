package httpapi_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"lanbox/internal/httpapi"
)

// withLoopback 打开回环引导登录并给定令牌。
func withLoopback(token string, enabled bool) envOption {
	return func(o *httpapi.Options) {
		o.AllowLoopbackLogin = enabled
		o.LoopbackToken = token
	}
}

// doLogin 用指定来源 IP 直接打处理器（绕过真实 TCP，便于伪造非回环地址）。
func doLogin(t *testing.T, e *env, target, remoteAddr string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, target, nil)
	req.RemoteAddr = remoteAddr
	w := httptest.NewRecorder()
	e.srv.Config.Handler.ServeHTTP(w, req)
	return w
}

func TestLoopbackLogin(t *testing.T) {
	const token = "0123456789abcdef"
	e := newEnvWith(t, withLoopback(token, true))

	t.Run("回环加正确令牌发会话并重定向", func(t *testing.T) {
		w := doLogin(t, e, "/__app/login?t="+token, "127.0.0.1:5000")
		if w.Code != http.StatusFound {
			t.Fatalf("期望 302，得到 %d %s", w.Code, w.Body.String())
		}
		if loc := w.Header().Get("Location"); loc != "/" {
			t.Fatalf("Location 应是 /，得到 %q", loc)
		}
		if !strings.Contains(w.Header().Get("Set-Cookie"), "sid=") {
			t.Fatalf("应下发 sid cookie: %v", w.Header())
		}
		if got := w.Header().Get("Cache-Control"); got != "no-store" {
			t.Fatalf("Cache-Control 应是 no-store，得到 %q", got)
		}
		// 该会话能直接访问 /api/me
		cookie := w.Result().Cookies()[0]
		req := httptest.NewRequest(http.MethodGet, "/api/me", nil)
		req.AddCookie(cookie)
		rec := httptest.NewRecorder()
		e.srv.Config.Handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "admin") {
			t.Fatalf("引导会话不可用: %d %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("非回环返回 404", func(t *testing.T) {
		if w := doLogin(t, e, "/__app/login?t="+token, "10.1.2.3:5000"); w.Code != http.StatusNotFound {
			t.Fatalf("期望 404，得到 %d", w.Code)
		}
	})

	t.Run("错误令牌返回 404", func(t *testing.T) {
		if w := doLogin(t, e, "/__app/login?t=wrong", "127.0.0.1:5000"); w.Code != http.StatusNotFound {
			t.Fatalf("期望 404，得到 %d", w.Code)
		}
	})

	t.Run("缺少令牌返回 404", func(t *testing.T) {
		if w := doLogin(t, e, "/__app/login", "127.0.0.1:5000"); w.Code != http.StatusNotFound {
			t.Fatalf("期望 404，得到 %d", w.Code)
		}
	})
}

func TestLoopbackLoginDisabled(t *testing.T) {
	e := newEnvWith(t, withLoopback("0123456789abcdef", false))
	w := doLogin(t, e, "/__app/login?t=0123456789abcdef", "127.0.0.1:5000")
	if w.Code != http.StatusNotFound {
		t.Fatalf("未启用时应 404，得到 %d", w.Code)
	}
}

func TestLoopbackLoginRateLimited(t *testing.T) {
	e := newEnvWith(t, withLoopback("0123456789abcdef", true))
	var last int
	for i := 0; i < 12; i++ {
		last = doLogin(t, e, "/__app/login?t=wrong", "127.0.0.1:5000").Code
	}
	if last != http.StatusTooManyRequests {
		t.Fatalf("连续错误令牌应触发限速（429），最后一次得到 %d", last)
	}
}
