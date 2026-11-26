package httpapi_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/cookiejar"
	"strings"
	"testing"

	"lanbox/internal/httpapi"
	"lanbox/internal/updater"
)

// fakeUpdate 是 UpdateManager 的假实现。
type fakeUpdate struct {
	st          updater.State
	checkErr    error
	applyErr    error
	rollbackErr error
	checks      int
	applied     string
	rolled      int
}

func (f *fakeUpdate) State() updater.State { return f.st }

func (f *fakeUpdate) Check(context.Context) (updater.State, error) {
	f.checks++
	if f.checkErr != nil {
		return f.st, f.checkErr
	}
	f.st.Available = true
	f.st.Latest = "1.2.4"
	return f.st, nil
}

func (f *fakeUpdate) Apply(_ context.Context, version string) (updater.State, error) {
	if f.applyErr != nil {
		return f.st, f.applyErr
	}
	f.applied = version
	f.st.Pending = version
	f.st.Available = false
	f.st.CanRollback = true
	return f.st, nil
}

func (f *fakeUpdate) Rollback() (updater.State, error) {
	f.rolled++
	if f.rollbackErr != nil {
		return f.st, f.rollbackErr
	}
	f.st.CanRollback = false
	return f.st, nil
}

func withUpdate(m httpapi.UpdateManager, restartDisabled bool) envOption {
	return func(o *httpapi.Options) {
		o.Update = func() httpapi.UpdateManager { return m }
		o.RestartDisabled = restartDisabled
	}
}

func TestUpdateNotSupported(t *testing.T) {
	e := newEnv(t)
	e.loginAdmin(t)
	for _, c := range []struct{ method, path string }{
		{http.MethodGet, "/api/update"},
		{http.MethodPost, "/api/update/check"},
		{http.MethodPost, "/api/update/apply"},
		{http.MethodPost, "/api/update/rollback"},
	} {
		resp, body := e.do(t, e.newJSONReq(t, c.method, c.path, map[string]string{"version": "1.2.4"}))
		if resp.StatusCode != http.StatusNotImplemented {
			t.Fatalf("%s %s 未注入时应 501，得到 %d %s", c.method, c.path, resp.StatusCode, body)
		}
		if !strings.Contains(string(body), "not_supported") {
			t.Fatalf("错误码不对: %s", body)
		}
	}
}

func TestUpdateRequiresAuth(t *testing.T) {
	f := &fakeUpdate{st: updater.State{Current: "1.2.3", Supported: true}}
	e := newEnvWith(t, withUpdate(f, false))

	resp, _ := e.do(t, e.newJSONReq(t, http.MethodGet, "/api/update", nil))
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("未登录应 401，得到 %d", resp.StatusCode)
	}
	e.loginAdmin(t)
	resp, _ = e.do(t, e.newJSONReq(t, http.MethodGet, "/api/update", nil))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("登录后应 200，得到 %d", resp.StatusCode)
	}

	// 普通用户能看状态，但不能触发升级。
	if resp, body := e.do(t, e.newJSONReq(t, http.MethodPost, "/api/users",
		map[string]any{"username": "bob", "password": "bob-password-123", "is_admin": false})); resp.StatusCode != http.StatusCreated {
		t.Fatalf("创建普通用户失败: %d %s", resp.StatusCode, body)
	}
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	e.client = &http.Client{Jar: jar, Transport: e.client.Transport}
	e.login(t, "bob", "bob-password-123")
	resp, _ = e.do(t, e.newJSONReq(t, http.MethodGet, "/api/update", nil))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("普通用户读状态应 200，得到 %d", resp.StatusCode)
	}
	for _, path := range []string{"/api/update/check", "/api/update/apply", "/api/update/rollback"} {
		resp, body := e.do(t, e.newJSONReq(t, http.MethodPost, path, map[string]string{"version": "1.2.4"}))
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("%s 非管理员应 403，得到 %d %s", path, resp.StatusCode, body)
		}
	}
}

func TestUpdateCheckAndApply(t *testing.T) {
	f := &fakeUpdate{st: updater.State{Current: "1.2.3", Supported: true}}
	e := newEnvWith(t, withUpdate(f, false))
	e.loginAdmin(t)

	resp, body := e.do(t, e.newJSONReq(t, http.MethodPost, "/api/update/check", nil))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("检查失败: %d %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), `"available":true`) {
		t.Fatalf("响应里没有可用版本: %s", body)
	}

	resp, body = e.do(t, e.newJSONReq(t, http.MethodPost, "/api/update/apply", map[string]string{"version": "1.2.4"}))
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("升级应 202，得到 %d %s", resp.StatusCode, body)
	}
	if f.applied != "1.2.4" {
		t.Fatalf("传给 Apply 的版本 = %q", f.applied)
	}

	resp, body = e.do(t, e.newJSONReq(t, http.MethodPost, "/api/update/rollback", nil))
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("回滚应 202，得到 %d %s", resp.StatusCode, body)
	}
	if f.rolled != 1 {
		t.Fatalf("回滚调用 %d 次", f.rolled)
	}
}

func TestUpdateApplyBadRequest(t *testing.T) {
	f := &fakeUpdate{st: updater.State{Current: "1.2.3", Supported: true}}
	e := newEnvWith(t, withUpdate(f, false))
	e.loginAdmin(t)
	for _, payload := range []any{map[string]string{}, map[string]string{"version": ""}, map[string]any{"v": "1.2.4"}} {
		resp, body := e.do(t, e.newJSONReq(t, http.MethodPost, "/api/update/apply", payload))
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("%v 应 400，得到 %d %s", payload, resp.StatusCode, body)
		}
	}
}

func TestUpdateErrorMapping(t *testing.T) {
	cases := []struct {
		name   string
		set    func(*fakeUpdate)
		call   string
		status int
		code   string
	}{
		{"检查无产物", func(f *fakeUpdate) { f.checkErr = updater.ErrNoAsset }, "check", http.StatusBadGateway, "no_asset"},
		{"升级中", func(f *fakeUpdate) { f.applyErr = updater.ErrBusy }, "apply", http.StatusConflict, "update_busy"},
		{"版本不一致", func(f *fakeUpdate) { f.applyErr = updater.ErrVersionMismatch }, "apply", http.StatusConflict, "version_mismatch"},
		{"没有备份", func(f *fakeUpdate) { f.rollbackErr = updater.ErrNoBackup }, "rollback", http.StatusConflict, "no_backup"},
		{"校验失败", func(f *fakeUpdate) {
			f.applyErr = &updater.Error{Stage: "verify", Err: errors.New("坏文件")}
		}, "apply", http.StatusBadGateway, "update_verify"},
		{"权限不足", func(f *fakeUpdate) {
			f.applyErr = &updater.Error{Stage: "permission", Err: errors.New("目录不可写")}
		}, "apply", http.StatusInternalServerError, "update_permission"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := &fakeUpdate{st: updater.State{Current: "1.2.3", Supported: true}}
			c.set(f)
			e := newEnvWith(t, withUpdate(f, false))
			e.loginAdmin(t)
			path := "/api/update/" + c.call
			payload := any(nil)
			if c.call == "apply" {
				payload = map[string]string{"version": "1.2.4"}
			}
			resp, body := e.do(t, e.newJSONReq(t, http.MethodPost, path, payload))
			if resp.StatusCode != c.status {
				t.Fatalf("期望 %d，得到 %d %s", c.status, resp.StatusCode, body)
			}
			if !strings.Contains(string(body), c.code) {
				t.Fatalf("期望错误码 %s，得到 %s", c.code, body)
			}
		})
	}
}

func TestUpdateDisabledWhenNoRestart(t *testing.T) {
	f := &fakeUpdate{st: updater.State{Current: "1.2.3", Supported: true}}
	e := newEnvWith(t, withUpdate(f, true))
	e.loginAdmin(t)
	for _, path := range []string{"/api/update/apply", "/api/update/rollback"} {
		resp, body := e.do(t, e.newJSONReq(t, http.MethodPost, path, map[string]string{"version": "1.2.4"}))
		if resp.StatusCode != http.StatusNotImplemented {
			t.Fatalf("%s 不可重启时应 501，得到 %d %s", path, resp.StatusCode, body)
		}
	}
}
