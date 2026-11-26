package httpapi

import (
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
