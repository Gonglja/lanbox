package httpapi

import (
	"crypto/subtle"
	"net"
	"net/http"
	"time"
)

type loginInput struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type meOutput struct {
	Username string `json:"username"`
	IsAdmin  bool   `json:"is_admin"`
}

// loginOutput 是 /api/login 的响应。
// 令牌只在跨域请求时下发（纯 web 客户端）：同源 Web UI 继续依赖 HttpOnly cookie，
// 响应体与以前完全一致（token/expires_at 为 omitempty）。
type loginOutput struct {
	Username  string `json:"username"`
	IsAdmin   bool   `json:"is_admin"`
	Token     string `json:"token,omitempty"`
	ExpiresAt string `json:"expires_at,omitempty"`
}

// handleLoopbackLogin 是 Android App 的免密入口：Go 侧每次启动生成一次性随机令牌，
// Kotlin 用它打开 https://127.0.0.1:<port>/__app/login?t=<token>，换来一个正常会话 Cookie。
//
// 约束：只在 AllowLoopbackLogin 打开时存在；只接受回环来源；常量时间比较；
// 独立限速；失败一律 404（不暴露路由存在性）。
func (s *Server) handleLoopbackLogin(w http.ResponseWriter, r *http.Request) {
	if !s.allowLoopback || s.loopbackToken == "" {
		http.NotFound(w, r)
		return
	}
	ip := net.ParseIP(clientIP(r))
	if ip == nil || !ip.IsLoopback() {
		http.NotFound(w, r)
		return
	}
	if !s.loopbackLimiter.Allowed("loopback") {
		writeError(w, http.StatusTooManyRequests, "too_many_requests", "请求过于频繁，请稍后再试")
		return
	}
	got := r.URL.Query().Get("t")
	if subtle.ConstantTimeCompare([]byte(got), []byte(s.loopbackToken)) != 1 {
		s.loopbackLimiter.Fail("loopback")
		http.NotFound(w, r)
		return
	}
	s.loopbackLimiter.Reset("loopback")
	token, expires, err := s.auth.NewSession("admin")
	if err != nil {
		s.writeStoreErr(w, err)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: token, Path: "/",
		HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode, Expires: expires,
	})
	w.Header().Set("Cache-Control", "no-store")
	s.log.Printf("info: 本地回环引导登录成功")
	http.Redirect(w, r, "/", http.StatusFound)
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var in loginInput
	if !decodeJSON(w, r, &in) {
		return
	}
	ip := clientIP(r)
	if !s.auth.Limiter().Allowed(ip) {
		writeError(w, http.StatusForbidden, "login_locked", "登录失败次数过多，请稍后再试")
		return
	}
	u, ok := s.auth.Verify(in.Username, in.Password)
	if !ok {
		s.auth.Limiter().Fail(ip)
		writeError(w, http.StatusUnauthorized, "bad_credentials", "用户名或密码错误")
		return
	}
	s.auth.Limiter().Reset(ip)
	token, expires, err := s.auth.NewSession(u.Username)
	if err != nil {
		s.writeStoreErr(w, err)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: token, Path: "/",
		HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode, Expires: expires,
	})
	s.log.Printf("info: %s 从 %s 登录成功", u.Username, ip)

	out := loginOutput{Username: u.Username, IsAdmin: u.IsAdmin}
	if crossOrigin(r) {
		// 跨域客户端拿不到 Set-Cookie 的会话，需要自己保存并随请求携带。
		out.Token = token
		out.ExpiresAt = expires.UTC().Format(time.RFC3339)
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	s.auth.Drop(tokenFrom(r))
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: "", Path: "/",
		HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode,
		Expires: time.Unix(0, 0),
	})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r)
	writeJSON(w, http.StatusOK, meOutput{Username: u.Username, IsAdmin: u.IsAdmin})
}

func (s *Server) handleChangePassword(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Old string `json:"old"`
		New string `json:"new"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	u := userFrom(r)
	if _, ok := s.auth.Verify(u.Username, in.Old); !ok {
		writeError(w, http.StatusForbidden, "bad_credentials", "当前密码不正确")
		return
	}
	if err := s.auth.SetPassword(u.Username, in.New); err != nil {
		writeError(w, http.StatusBadRequest, "weak_password", err.Error())
		return
	}
	// 改密会踢掉该用户所有会话，这里顺手清掉 Cookie
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: "", Path: "/",
		HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode,
		Expires: time.Unix(0, 0),
	})
	w.WriteHeader(http.StatusNoContent)
}
