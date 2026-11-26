package httpapi

import (
	"context"
	"net/http"
	"net/url"
	"runtime/debug"
	"strings"
	"time"

	"lanbox/internal/auth"
)

type ctxKey int

const (
	ctxUser ctxKey = iota
	ctxToken
)

// sessionCookie 是会话 Cookie 名。
const sessionCookie = "sid"

// chain 组合所有全局中间件。
func (s *Server) chain(next http.Handler) http.Handler {
	return s.recoverMW(s.securityHeaders(s.accessLog(s.csrf(next))))
}

func (s *Server) recoverMW(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				s.log.Printf("panic: %v\n%s", rec, debug.Stack())
				writeError(w, http.StatusInternalServerError, "panic", "服务器内部错误")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func (s *Server) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", "default-src 'self'")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
}

type logWriter struct {
	http.ResponseWriter
	status int
	size   int64
}

func (l *logWriter) WriteHeader(code int) {
	if l.status == 0 {
		l.status = code
	}
	l.ResponseWriter.WriteHeader(code)
}

func (l *logWriter) Write(b []byte) (int, error) {
	if l.status == 0 {
		l.status = http.StatusOK
	}
	n, err := l.ResponseWriter.Write(b)
	l.size += int64(n)
	return n, err
}

// Flush 让 SSE 能即时下发（包装后仍满足 http.Flusher）。
func (l *logWriter) Flush() {
	if f, ok := l.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (s *Server) accessLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/healthz" {
			next.ServeHTTP(w, r)
			return
		}
		start := time.Now()
		lw := &logWriter{ResponseWriter: w}
		next.ServeHTTP(lw, r)
		if strings.HasPrefix(r.URL.Path, "/api/") && r.URL.Path != "/api/events" {
			s.log.Printf("%s %s %d %dB %s %s", r.Method, r.URL.Path, lw.status, lw.size,
				clientIP(r), time.Since(start).Round(time.Millisecond))
		}
	})
}

// csrf 校验不安全方法的 Origin/Referer 必须与 Host 同源。
func (s *Server) csrf(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			next.ServeHTTP(w, r)
			return
		}
		if origin := r.Header.Get("Origin"); origin != "" {
			if !sameHost(origin, r.Host) {
				writeError(w, http.StatusForbidden, "csrf", "请求来源不被信任")
				return
			}
		} else if ref := r.Header.Get("Referer"); ref != "" {
			if !sameHost(ref, r.Host) {
				writeError(w, http.StatusForbidden, "csrf", "请求来源不被信任")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// sameHost 比较 URL 的 Host 与请求 Host。
func sameHost(rawURL, host string) bool {
	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	return strings.EqualFold(u.Host, host)
}

// withAuth 校验会话，把用户放进 context，并做滑动续期。
func (s *Server) withAuth(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(sessionCookie)
		if err != nil || c.Value == "" {
			writeError(w, http.StatusUnauthorized, "unauthorized", "请先登录")
			return
		}
		u, ok := s.auth.Lookup(c.Value)
		if !ok {
			writeError(w, http.StatusUnauthorized, "unauthorized", "会话已过期，请重新登录")
			return
		}
		http.SetCookie(w, &http.Cookie{
			Name: sessionCookie, Value: c.Value, Path: "/",
			HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode,
			Expires: time.Now().Add(s.current().SessionTTL()),
		})
		ctx := context.WithValue(r.Context(), ctxUser, u)
		ctx = context.WithValue(ctx, ctxToken, c.Value)
		h(w, r.WithContext(ctx))
	}
}

// withAdmin 在 withAuth 基础上要求管理员。
func (s *Server) withAdmin(h http.HandlerFunc) http.HandlerFunc {
	return s.withAuth(func(w http.ResponseWriter, r *http.Request) {
		u := userFrom(r)
		if u == nil || !u.IsAdmin {
			writeError(w, http.StatusForbidden, "forbidden", "需要管理员权限")
			return
		}
		h(w, r)
	})
}

// userFrom 取出当前登录用户（已由 withAuth 注入）。
func userFrom(r *http.Request) *auth.User {
	u, _ := r.Context().Value(ctxUser).(*auth.User)
	return u
}

func tokenFrom(r *http.Request) string {
	t, _ := r.Context().Value(ctxToken).(string)
	return t
}
