//go:build windows

package main

import (
	"context"
	"errors"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"lanbox/internal/agent"
	"lanbox/internal/client"
)

// noopUploader 让 Manager 能构造出来（这些测试不涉及上传）。
type noopUploader struct{}

func (noopUploader) Put(context.Context, string, []agent.File, func(int64, int64)) (string, error) {
	return "item-1", nil
}
func (noopUploader) Delete(context.Context, string) error { return nil }

// newReconnectTestApp 构造一个只依赖被测服务器的最小 App：
// 剪贴板关闭（不碰真实剪贴板）、会话不落盘（不覆盖开发机的 session.json）。
func newReconnectTestApp(t *testing.T, h http.Handler) (*App, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)

	s := defaultSettings()
	s.Clipboard.Enabled = false
	s.Clipboard.Paused = true
	s.Server.URL = srv.URL
	s.Server.Username = "admin"
	s.App.RememberPassword = true
	if err := s.setPassword("pw"); err != nil {
		t.Fatalf("保存密码失败: %v", err)
	}

	a := &App{
		holder:      &clientHolder{},
		settings:    s,
		saveSession: func(*client.Session) error { return nil },
	}
	a.mgr = agent.NewManager(agent.Options{
		Uploader:    noopUploader{},
		Settings:    s.agentSettings(),
		HistorySize: 10,
		Classify:    classify,
		Reauth:      a.reauth,
	})
	a.mgr.SetAvailable(false)
	a.rc = agent.NewReconnector(agent.DefaultReconnectPolicy())
	return a, srv
}

// setClient 把 holder 指向被测服务器（相当于「手里有一个会话」）。
func (a *App) setClient(t *testing.T, baseURL string) {
	t.Helper()
	cli, err := client.New(client.Options{BaseURL: baseURL, SID: "sid-test"})
	if err != nil {
		t.Fatalf("构造客户端失败: %v", err)
	}
	a.holder.set(cli)
}

func TestReconnectProbeNetworkFailureMarksDisconnected(t *testing.T) {
	a, srv := newReconnectTestApp(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	a.setClient(t, srv.URL)
	a.rc.Enable(true)
	a.rc.ProbeOK(time.Now()) // 先当成已连上，才能验证运行中的「抖动防抖」
	a.mgr.SetAvailable(true)

	ctx := context.Background()
	a.probeServer(ctx)
	if !a.rc.Connected() {
		t.Fatalf("首次网络失败还不应判断线（防抖）")
	}
	a.probeServer(ctx)
	if a.rc.Connected() || !a.rc.Reconnecting() {
		t.Fatalf("连续两次失败应进入重连中，connected=%v reconnecting=%v", a.rc.Connected(), a.rc.Reconnecting())
	}
	if a.holder.get() == nil {
		t.Fatalf("网络失败不应丢掉会话")
	}
	if a.mgr.State().Available {
		t.Fatalf("断线后不应再标记服务端可用")
	}
	if a.uiState().NextRetryAt == 0 {
		t.Fatalf("断线后应给出下次重试时间")
	}
}

func TestReconnectProbeRecovers(t *testing.T) {
	var healthy bool
	a, srv := newReconnectTestApp(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/me" && healthy:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"username":"admin"}`))
		default:
			http.Error(w, "down", http.StatusBadGateway)
		}
	}))
	a.setClient(t, srv.URL)
	a.rc.Enable(true)
	a.mgr.SetAvailable(true)

	ctx := context.Background()
	a.probeServer(ctx)
	a.probeServer(ctx)
	if a.rc.Connected() {
		t.Fatalf("应已断线")
	}

	healthy = true
	a.probeServer(ctx)
	if !a.rc.Connected() || a.rc.Reconnecting() {
		t.Fatalf("探活成功后应恢复连接")
	}
	if !a.mgr.State().Available {
		t.Fatalf("恢复后应重新标记服务端可用")
	}
	if !a.uiState().Connected {
		t.Fatalf("uiState 应报告 connected")
	}
}

func TestReconnectProbeAuthFailureDropsSessionThenRelogin(t *testing.T) {
	var meHits, loginHits int
	a, srv := newReconnectTestApp(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/me":
			meHits++
			http.Error(w, `{"error":{"code":"unauthorized","message":"会话已过期"}}`, http.StatusUnauthorized)
		case "/api/login":
			loginHits++
			http.SetCookie(w, &http.Cookie{Name: "sid", Value: "new-sid", Path: "/"})
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"username":"admin"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	a.setClient(t, srv.URL)
	a.rc.Enable(true)
	a.mgr.SetAvailable(true)

	ctx := context.Background()
	a.probeServer(ctx)
	if meHits != 1 {
		t.Fatalf("应探活一次，得到 %d", meHits)
	}
	if a.holder.get() != nil {
		t.Fatalf("401 后应丢掉手里的会话")
	}
	if a.rc.Connected() {
		t.Fatalf("会话失效后不应算已连接")
	}

	a.reloginStep(ctx)
	if loginHits != 1 {
		t.Fatalf("应自动重登一次，得到 %d", loginHits)
	}
	if !a.rc.Connected() {
		t.Fatalf("重登成功后应恢复连接")
	}
	if a.holder.get() == nil || !a.mgr.State().Available {
		t.Fatalf("重登成功后应恢复客户端与服务端可用标记")
	}
}

func TestReconnectReloginRejectedStopsAfterThree(t *testing.T) {
	a, srv := newReconnectTestApp(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":{"code":"bad_credentials","message":"用户名或密码错误"}}`, http.StatusUnauthorized)
	}))
	a.setClient(t, srv.URL)
	a.rc.Enable(true)

	ctx := context.Background()
	for i := 0; i < 3; i++ {
		a.reloginStep(ctx)
	}
	stopped, reason := a.rc.Stopped()
	if !stopped {
		t.Fatalf("连续 3 次认证失败应停手")
	}
	if reason == "" || a.uiState().ReconnectReason == "" {
		t.Fatalf("应给出停手原因，得到 %q", reason)
	}
	if a.uiState().Reconnecting {
		t.Fatalf("停手后不应再显示重连中")
	}
}

func TestReconnectReloginLockedCoolsDown(t *testing.T) {
	a, srv := newReconnectTestApp(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":{"code":"login_locked","message":"登录失败次数过多，请稍后再试"}}`, http.StatusForbidden)
	}))
	a.setClient(t, srv.URL)
	a.rc.Enable(true)
	a.reloginStep(context.Background())

	if stopped, _ := a.rc.Stopped(); stopped {
		t.Fatalf("被限速不应停手（冷却后再试）")
	}
	if got := a.rc.NextRetryAt(); got.Before(time.Now().Add(9 * time.Minute)) {
		t.Fatalf("被限速后应冷却约 10 分钟，得到 %v", time.Until(got))
	}
}

func TestReconnectStopsWhenPasswordNotSaved(t *testing.T) {
	a, srv := newReconnectTestApp(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	a.setClient(t, srv.URL)
	a.settings.Server.Password = ""
	a.rc.Enable(false)

	a.reloginStep(context.Background())

	stopped, reason := a.rc.Stopped()
	if !stopped || reason == "" {
		t.Fatalf("没有保存密码时应直接停手，stopped=%v reason=%q", stopped, reason)
	}
}

func TestIsLoginLocked(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"登录被锁", &client.Error{Status: http.StatusForbidden, Code: "login_locked"}, true},
		{"429", &client.Error{Status: http.StatusTooManyRequests}, true},
		{"密码错", &client.Error{Status: http.StatusUnauthorized, Code: "bad_credentials"}, false},
		{"网络", errors.New("dial tcp: timeout"), false},
	}
	for _, c := range cases {
		if got := isLoginLocked(c.err); got != c.want {
			t.Errorf("%s: isLoginLocked=%v，期望 %v", c.name, got, c.want)
		}
	}
}

func TestEnableReconnectNeedsCredentials(t *testing.T) {
	a, srv := newReconnectTestApp(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	_ = srv
	// 有凭据（URL+用户名+密码）：启用守护但标记没有会话 → 走重登
	a.enableReconnect()
	if !a.rc.Enabled() || a.rc.Connected() {
		t.Fatalf("有凭据时应启用守护且需要重登")
	}

	// 没有密码：关闭守护（只能手动登录）
	a.settings.Server.Password = ""
	a.enableReconnect()
	if a.rc.Enabled() || a.rc.Reconnecting() {
		t.Fatalf("没有密码时不应启用守护")
	}

	// 有会话：启用并视为已连接
	a.settings.Server.Password = "x"
	a.setClient(t, srv.URL)
	a.enableReconnect()
	if !a.rc.Enabled() || !a.rc.Connected() {
		t.Fatalf("有会话时应启用并视为已连接")
	}
}

// TestNewAppWiresReconnectorBeforeManager 防止回退：Manager 的 OnState 回调会读
// App.rc，如果 rc 在 NewManager 之后才建，SetAvailable 会空指针崩溃。
func TestNewAppWiresReconnectorBeforeManager(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	logger := log.New(io.Discard, "", 0)
	a := NewApp("test", logger)
	if a.rc == nil {
		t.Fatalf("NewApp 应建好重连状态机")
	}
	if a.saveSession == nil {
		t.Fatalf("NewApp 应装好会话保存钩子")
	}
	if st := a.uiState(); st.Connected || st.Reconnecting || st.ReconnectStopped {
		t.Fatalf("未登录时重连状态应全为 false，得到 %+v", st)
	}
}
