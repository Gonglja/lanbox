package updater

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// quiet 是测试用的静默日志。
var quiet = log.New(io.Discard, "", 0)

// releaseServer 同时提供 SHA256SUMS 与资产。
func releaseServer(t *testing.T, tag string, blob []byte, gate chan struct{}) *httptest.Server {
	t.Helper()
	sum := shaOf(blob)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/releases/latest/download/SHA256SUMS":
			http.Redirect(w, r, "/releases/download/"+tag+"/SHA256SUMS", http.StatusFound)
		case "/releases/download/" + tag + "/SHA256SUMS":
			fmt.Fprintf(w, "%s  lanbox-linux-amd64\n", sum)
		case "/releases/download/" + tag + "/lanbox-linux-amd64":
			if gate != nil {
				<-gate
			}
			w.Write(blob)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(ts.Close)
	return ts
}

func newTestManager(t *testing.T, baseURL, exe string, restart func()) (*Manager, *Updater) {
	t.Helper()
	up := newTestUpdater(t, Options{Current: "1.2.3", Asset: "lanbox-linux-amd64", BaseURL: baseURL})
	m := NewManager(ManagerOptions{Updater: up, ExePath: exe, Restart: restart, Log: quiet})
	return m, up
}

// tempExeFile 造一个「当前程序」文件。
func tempExeFile(t *testing.T) string {
	t.Helper()
	exe := filepath.Join(t.TempDir(), "lanbox")
	if err := os.WriteFile(exe, []byte("旧版本"), 0o755); err != nil {
		t.Fatal(err)
	}
	return exe
}

func TestManagerNotSupported(t *testing.T) {
	m := NewManager(ManagerOptions{})
	if m.State().Supported {
		t.Fatal("没有 Updater 时不该声明支持")
	}
	if _, err := m.Check(context.Background()); !errors.Is(err, ErrNotSupported) {
		t.Fatalf("Check 期望 ErrNotSupported，得到 %v", err)
	}
	if _, err := m.Apply(context.Background(), "1.2.4"); !errors.Is(err, ErrNotSupported) {
		t.Fatalf("Apply 期望 ErrNotSupported，得到 %v", err)
	}
	if _, err := m.Rollback(); !errors.Is(err, ErrNotSupported) {
		t.Fatalf("Rollback 期望 ErrNotSupported，得到 %v", err)
	}
	m.Run(context.Background(), "daily") // 不该 panic / 不该起 goroutine
}

func TestManagerCheck(t *testing.T) {
	blob := fakeBinary("新版本")
	ts := releaseServer(t, "v1.2.4", blob, nil)
	exe := tempExeFile(t)

	var mu sync.Mutex
	var states []State
	up := newTestUpdater(t, Options{Current: "1.2.3", Asset: "lanbox-linux-amd64", BaseURL: ts.URL})
	m := NewManager(ManagerOptions{Updater: up, ExePath: exe, Log: quiet, OnState: func(s State) {
		mu.Lock()
		states = append(states, s)
		mu.Unlock()
	}})

	st, err := m.Check(context.Background())
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if !st.Available || st.Latest != "1.2.4" || st.Current != "1.2.3" || st.CheckError != "" {
		t.Fatalf("状态不对: %+v", st)
	}
	if st.CheckedAt.IsZero() {
		t.Fatal("没记录检查时间")
	}
	if len(states) == 0 || !states[len(states)-1].Available {
		t.Fatal("OnState 没收到可用版本")
	}
	if line := st.VersionLine(); !strings.Contains(line, "1.2.4") {
		t.Fatalf("VersionLine = %q", line)
	}

	// 已是最高版本：不报错、保留 Latest、不显示可升级。
	upSame := newTestUpdater(t, Options{Current: "1.2.4", Asset: "lanbox-linux-amd64", BaseURL: ts.URL})
	mSame := NewManager(ManagerOptions{Updater: upSame, ExePath: exe, Log: quiet})
	st, err = mSame.Check(context.Background())
	if err != nil {
		t.Fatalf("已是最新时 Check 不该报错: %v", err)
	}
	if st.Available {
		t.Fatal("同一版本不该还可升级")
	}
	if st.Latest != "1.2.4" || st.CheckError != "" {
		t.Fatalf("状态不对: %+v", st)
	}
	if line := st.VersionLine(); !strings.Contains(line, "已是最新") {
		t.Fatalf("VersionLine = %q", line)
	}
}

func TestManagerCheckKeepsOldStateOnError(t *testing.T) {
	blob := fakeBinary("新版本")
	ts := releaseServer(t, "v1.2.4", blob, nil)
	exe := tempExeFile(t)
	m, _ := newTestManager(t, ts.URL, exe, nil)
	if _, err := m.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	ts.Close() // 之后所有请求都失败

	st, err := m.Check(context.Background())
	if err == nil {
		t.Fatal("源关掉后应该报错")
	}
	if st.Latest != "1.2.4" || !st.Available {
		t.Fatalf("失败时不该丢掉上次的结果: %+v", st)
	}
	if st.CheckError == "" {
		t.Fatal("应该记录错误原因")
	}
	if line := st.VersionLine(); !strings.Contains(line, "检查失败") {
		t.Fatalf("VersionLine = %q", line)
	}
}

func TestManagerApply(t *testing.T) {
	blob := fakeBinary("全新的版本")
	ts := releaseServer(t, "v1.2.4", blob, nil)
	exe := tempExeFile(t)
	restarts := 0
	m, _ := newTestManager(t, ts.URL, exe, func() { restarts++ })

	// 没检查过就升级 → 版本不一致。
	if _, err := m.Apply(context.Background(), "1.2.4"); !errors.Is(err, ErrVersionMismatch) {
		t.Fatalf("未检查时 Apply 期望 ErrVersionMismatch，得到 %v", err)
	}
	if _, err := m.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	// 版本号对不上 → 拒绝。
	if _, err := m.Apply(context.Background(), "1.2.3"); !errors.Is(err, ErrVersionMismatch) {
		t.Fatalf("版本不一致期望 ErrVersionMismatch，得到 %v", err)
	}

	st, err := m.Apply(context.Background(), "1.2.4")
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if restarts != 1 {
		t.Fatalf("重启被调用 %d 次，期望 1", restarts)
	}
	if st.Updating || st.Progress != 100 || st.Available || st.Pending != "1.2.4" || !st.CanRollback {
		t.Fatalf("升级后状态不对: %+v", st)
	}
	got, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(blob) {
		t.Fatal("可执行文件没被替换")
	}
	if old, err := os.ReadFile(BackupPath(exe)); err != nil || string(old) != "旧版本" {
		t.Fatalf("备份不对: %q %v", old, err)
	}

	// 升级后回滚。
	restarts = 0
	st, err = m.Rollback()
	if err != nil {
		t.Fatalf("Rollback: %v", err)
	}
	if restarts != 1 || st.CanRollback || st.Pending != "" {
		t.Fatalf("回滚后状态不对: %+v（restarts=%d）", st, restarts)
	}
	if got, _ := os.ReadFile(exe); string(got) != "旧版本" {
		t.Fatalf("回滚后内容 = %q", got)
	}
}

func TestManagerApplyBusy(t *testing.T) {
	blob := fakeBinary("新版本")
	gate := make(chan struct{})
	ts := releaseServer(t, "v1.2.4", blob, gate)
	exe := tempExeFile(t)
	m, _ := newTestManager(t, ts.URL, exe, nil)
	if _, err := m.Check(context.Background()); err != nil {
		t.Fatal(err)
	}

	done := make(chan error, 1)
	go func() {
		_, err := m.Apply(context.Background(), "1.2.4")
		done <- err
	}()
	// 等下载真正开始（Updating 置位）。
	deadline := time.Now().Add(3 * time.Second)
	for !m.State().Updating {
		if time.Now().After(deadline) {
			t.Fatal("等不到升级开始")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if _, err := m.Apply(context.Background(), "1.2.4"); !errors.Is(err, ErrBusy) {
		t.Fatalf("并发 Apply 期望 ErrBusy，得到 %v", err)
	}
	if _, err := m.Check(context.Background()); !errors.Is(err, ErrBusy) {
		t.Fatalf("升级中 Check 期望 ErrBusy，得到 %v", err)
	}
	if _, err := m.Rollback(); !errors.Is(err, ErrBusy) {
		t.Fatalf("升级中 Rollback 期望 ErrBusy，得到 %v", err)
	}
	close(gate)
	if err := <-done; err != nil {
		t.Fatalf("升级失败: %v", err)
	}
}

func TestManagerApplyDownloadFailureKeepsState(t *testing.T) {
	blob := fakeBinary("新版本")
	ts := releaseServer(t, "v1.2.4", blob, nil)
	exe := tempExeFile(t)
	m, _ := newTestManager(t, ts.URL, exe, nil)
	if _, err := m.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	// 删掉资产：下载 404。
	ts.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "SHA256SUMS") {
			fmt.Fprintf(w, "%s  lanbox-linux-amd64\n", shaOf(blob))
			return
		}
		http.NotFound(w, r)
	})
	if _, err := m.Apply(context.Background(), "1.2.4"); err == nil {
		t.Fatal("下载失败时应该报错")
	}
	st := m.State()
	if st.Updating {
		t.Fatal("失败后 Updating 应该复位")
	}
	if !st.Available {
		t.Fatal("失败后仍应保留可升级状态，方便重试")
	}
	if got, _ := os.ReadFile(exe); string(got) != "旧版本" {
		t.Fatalf("失败后不该动原程序，得到 %q", got)
	}
}

func TestManagerRollbackWithoutBackup(t *testing.T) {
	exe := tempExeFile(t)
	m := NewManager(ManagerOptions{ExePath: exe, Log: quiet})
	m.mu.Lock()
	m.st = State{Current: "1.2.3", Supported: true}
	m.mu.Unlock()
	if _, err := m.Rollback(); !errors.Is(err, ErrNotSupported) {
		t.Fatalf("没有 updater 时期望 ErrNotSupported，得到 %v", err)
	}
}

func TestManagerRun(t *testing.T) {
	blob := fakeBinary("新版本")
	ts := releaseServer(t, "v1.2.4", blob, nil)
	exe := tempExeFile(t)
	up := newTestUpdater(t, Options{Current: "1.2.3", Asset: "lanbox-linux-amd64", BaseURL: ts.URL})
	after := make(chan time.Time, 1)
	after <- time.Now() // 立即触发第一次检查
	m := NewManager(ManagerOptions{
		Updater: up, ExePath: exe, Log: quiet,
		After: func(time.Duration) <-chan time.Time { return after },
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m.Run(ctx, "startup")

	deadline := time.Now().Add(3 * time.Second)
	for !m.State().Available {
		if time.Now().After(deadline) {
			t.Fatalf("后台检查没跑起来: %+v", m.State())
		}
		time.Sleep(5 * time.Millisecond)
	}
}
