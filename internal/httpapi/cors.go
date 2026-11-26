package httpapi

import (
	"net/http"
	"strings"
)

// cors 允许「纯 web 客户端」从任意源访问 /api/*。
//
// 关键取舍：这里**不设置** Access-Control-Allow-Credentials。浏览器因此不会把
// sid cookie 带到跨站请求里，本服务也没有 SameSite=None 的会话 cookie —— 跨域页面
// 的能力完全取决于它显式携带的 Bearer 令牌（见 withAuth），拿不到令牌就一律 401。
// 同源 Web UI 不受影响：它继续用 cookie，且登录响应不会下发令牌。
func (s *Server) cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/api/") {
			next.ServeHTTP(w, r)
			return
		}
		h := w.Header()
		h.Set("Access-Control-Allow-Origin", "*")
		h.Set("Access-Control-Allow-Methods", "GET, HEAD, POST, PATCH, DELETE, OPTIONS")
		h.Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
		h.Set("Access-Control-Expose-Headers", "Content-Disposition")
		h.Set("Access-Control-Max-Age", "600")
		if r.Method == http.MethodOptions {
			// 预检不需要认证：它只声明"允许跨域调用哪些方法与请求头"，
			// 真正的鉴权在后续实际请求上完成。
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// crossOrigin 判断请求是否来自别的源。
//
// 同源 fetch 默认不带 Origin，跨域请求（以及 file:// 打开的页面）一定会带，
// 因此「有 Origin 且与 Host 不同」即可判定为纯 web 客户端。只有这种请求才在
// 登录响应体里下发令牌，同源 Web UI 的响应体保持原样。
func crossOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	return origin != "" && !sameHost(origin, r.Host)
}
