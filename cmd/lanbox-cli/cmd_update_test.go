package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"lanbox/internal/httpapi"
	"lanbox/internal/updater"
)

// fakeUpdater 是 httpapi.UpdateManager 的假实现。
type fakeUpdater struct {
	st          updater.State
	checkErr    error
	applyErr    error
	rollbackErr error
	newVersion  string
	checks      int
	applied     string
	rollbacks   int
}

func (f *fakeUpdater) State() updater.State { return f.st }

func (f *fakeUpdater) Check(context.Context) (updater.State, error) {
	f.checks++
	if f.checkErr != nil {
		return f.st, f.checkErr
	}
	if f.newVersion == "" {
		f.st.Available = false
		return f.st, nil
	}
	f.st.Available = true
	f.st.Latest = f.newVersion
	f.st.Size = 12 * 1024 * 1024
	return f.st, nil
}

func (f *fakeUpdater) Apply(_ context.Context, version string) (updater.State, error) {
	if f.applyErr != nil {
		return f.st, f.applyErr
	}
	f.applied = version
	f.st.Available = false
	f.st.Pending = version
	return f.st, nil
}

func (f *fakeUpdater) Rollback() (updater.State, error) {
	f.rollbacks++
	if f.rollbackErr != nil {
		return f.st, f.rollbackErr
	}
	f.st.CanRollback = false
	return f.st, nil
}

func withFakeUpdater(f *fakeUpdater, restartDisabled bool) func(*httpapi.Options) {
	return func(o *httpapi.Options) {
		o.Update = func() httpapi.UpdateManager { return f }
		o.RestartDisabled = restartDisabled
	}
}

func baseState() updater.State {
	return updater.State{Current: "1.2.3", Supported: true, CanRollback: true}
}

// withUpdate 造一个「有新版 1.2.4 可升」的假管理器。
func withUpdate(t *testing.T) (*fakeUpdater, *cmdEnv) {
	t.Helper()
	f := &fakeUpdater{st: baseState(), newVersion: "1.2.4"}
	return f, newCmdEnv(t, withFakeUpdater(f, false))
}

func TestCmdUpdateShow(t *testing.T) {
	f := &fakeUpdater{st: baseState()}
	e := newCmdEnv(t, withFakeUpdater(f, false))
	if err := cmdUpdate(context.Background(), e.g, nil, os.Stdin); err != nil {
		t.Fatalf("update 失败: %v", err)
	}
	out := e.out.String()
	for _, want := range []string{"版本   1.2.3", "检查   还没有检查过", "回滚   可用"} {
		if !strings.Contains(out, want) {
			t.Fatalf("输出缺少 %q：\n%s", want, out)
		}
	}
}

func TestCmdUpdateCheck(t *testing.T) {
	f, e := withUpdate(t)
	if err := cmdUpdate(context.Background(), e.g, []string{"--check"}, os.Stdin); err != nil {
		t.Fatalf("update --check 失败: %v", err)
	}
	if f.checks != 1 {
		t.Fatalf("检查次数 = %d", f.checks)
	}
	out := e.out.String()
	if !strings.Contains(out, "最新   1.2.4 可用（12.0 MB）") {
		t.Fatalf("输出没显示新版本：\n%s", out)
	}
}

func TestCmdUpdateApply(t *testing.T) {
	f, e := withUpdate(t)
	if err := cmdUpdate(context.Background(), e.g, []string{"--apply", "--yes"}, os.Stdin); err != nil {
		t.Fatalf("update --apply 失败: %v", err)
	}
	if f.applied != "1.2.4" {
		t.Fatalf("升级版本 = %q", f.applied)
	}
	if !strings.Contains(e.out.String(), "已升级到 1.2.4") {
		t.Fatalf("输出不对：\n%s", e.out.String())
	}
}

func TestCmdUpdateApplyNeedsConfirmation(t *testing.T) {
	f, e := withUpdate(t)
	// 测试环境里 os.Stdin 不是终端：不加 --yes 必须拒绝。
	err := cmdUpdate(context.Background(), e.g, []string{"--apply"}, os.Stdin)
	if !errors.Is(err, errUsage) {
		t.Fatalf("期望用法错误，得到 %v", err)
	}
	if f.applied != "" {
		t.Fatal("没确认就不该升级")
	}
}

func TestCmdUpdateAlreadyLatest(t *testing.T) {
	f := &fakeUpdater{st: baseState()} // newVersion 为空 → 检查后仍不可升级
	e := newCmdEnv(t, withFakeUpdater(f, false))
	if err := cmdUpdate(context.Background(), e.g, []string{"--apply", "--yes"}, os.Stdin); err != nil {
		t.Fatalf("已是最新时不该报错: %v", err)
	}
	if f.applied != "" {
		t.Fatalf("已是最新却调用了升级: %q", f.applied)
	}
	if !strings.Contains(e.out.String(), "已是最新") {
		t.Fatalf("输出不对：%s", e.out.String())
	}
}

func TestCmdUpdateRollback(t *testing.T) {
	f := &fakeUpdater{st: baseState()}
	e := newCmdEnv(t, withFakeUpdater(f, false))
	if err := cmdUpdate(context.Background(), e.g, []string{"--rollback", "--yes"}, os.Stdin); err != nil {
		t.Fatalf("update --rollback 失败: %v", err)
	}
	if f.rollbacks != 1 {
		t.Fatalf("回滚次数 = %d", f.rollbacks)
	}
}

func TestCmdUpdateRollbackUnavailable(t *testing.T) {
	f := &fakeUpdater{st: baseState()}
	f.st.CanRollback = false
	e := newCmdEnv(t, withFakeUpdater(f, false))
	err := cmdUpdate(context.Background(), e.g, []string{"--rollback", "--yes"}, os.Stdin)
	if err == nil || !strings.Contains(err.Error(), "没有可回滚") {
		t.Fatalf("期望没有备份的提示，得到 %v", err)
	}
}

func TestCmdUpdateFlagsExclusive(t *testing.T) {
	f := &fakeUpdater{st: baseState()}
	e := newCmdEnv(t, withFakeUpdater(f, false))
	if err := cmdUpdate(context.Background(), e.g, []string{"--check", "--apply"}, os.Stdin); !errors.Is(err, errUsage) {
		t.Fatalf("互斥选项应报用法错误，得到 %v", err)
	}
	if err := cmdUpdate(context.Background(), e.g, []string{"多余参数"}, os.Stdin); !errors.Is(err, errUsage) {
		t.Fatalf("位置参数应报用法错误，得到 %v", err)
	}
}

func TestCmdUpdateUnsupported(t *testing.T) {
	e := newCmdEnv(t) // 没有注入 Update
	err := cmdUpdate(context.Background(), e.g, nil, os.Stdin)
	if err == nil || !strings.Contains(err.Error(), "不支持") {
		t.Fatalf("期望不支持提示，得到 %v", err)
	}
}

func TestCmdUpdateJSON(t *testing.T) {
	_, e := withUpdate(t)
	e.g.json = true
	if err := cmdUpdate(context.Background(), e.g, []string{"--check"}, os.Stdin); err != nil {
		t.Fatalf("update --check -json 失败: %v", err)
	}
	var st updater.State
	if err := json.Unmarshal(e.out.Bytes(), &st); err != nil {
		t.Fatalf("输出不是合法 JSON：%s", e.out.String())
	}
	if st.Current != "1.2.3" || st.Latest != "1.2.4" {
		t.Fatalf("JSON 内容不对: %+v", st)
	}
}

func TestCmdUpdateErrorsPropagate(t *testing.T) {
	f, e := withUpdate(t)
	f.applyErr = &updater.Error{Stage: "verify", Err: errors.New("坏文件")}
	err := cmdUpdate(context.Background(), e.g, []string{"--apply", "--yes"}, os.Stdin)
	if err == nil || !strings.Contains(err.Error(), "坏文件") {
		t.Fatalf("服务端失败时应带回原因，得到 %v", err)
	}
}
