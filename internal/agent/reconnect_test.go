package agent

import (
	"testing"
	"time"
)

// 固定起点，方便按秒推进。
var rcT0 = time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

func newTestReconnector() *Reconnector {
	r := NewReconnector(DefaultReconnectPolicy())
	r.setClock(func() time.Time { return rcT0 })
	return r
}

func TestReconnectProbeCadence(t *testing.T) {
	r := newTestReconnector()
	r.Enable(true)

	// 刚启用时立刻探活一次。
	if got := r.Due(rcT0); got != StepProbe {
		t.Fatalf("启用后应立即探活，得到 %v", got)
	}
	r.ProbeOK(rcT0)
	if !r.Connected() || r.Reconnecting() {
		t.Fatalf("探活成功后应处于已连接状态")
	}

	if got := r.Due(rcT0.Add(29 * time.Second)); got != StepIdle {
		t.Fatalf("未到探活间隔不应动作，得到 %v", got)
	}
	if got := r.Due(rcT0.Add(30 * time.Second)); got != StepProbe {
		t.Fatalf("到探活间隔应探活，得到 %v", got)
	}
}

func TestReconnectDebounceNetworkFails(t *testing.T) {
	r := newTestReconnector()
	r.Enable(true)
	r.ProbeOK(rcT0)

	// 第一次失败：还没到阈值，仍算连接正常。
	r.ProbeNetworkErr(rcT0.Add(30*time.Second), "网络不通")
	if !r.Connected() {
		t.Fatalf("首次失败不应直接判断线")
	}

	// 第二次失败：判定断线，进入退避。
	t1 := rcT0.Add(60 * time.Second)
	r.ProbeNetworkErr(t1, "网络不通")
	if r.Connected() || !r.Reconnecting() {
		t.Fatalf("连续两次失败应判定断线，connected=%v reconnecting=%v", r.Connected(), r.Reconnecting())
	}
	if got, want := r.NextRetryAt(), t1.Add(2*time.Second); !got.Equal(want) {
		t.Fatalf("首次退避应为 2s，得到 %v 期望 %v", got, want)
	}
}

func TestReconnectBackoffSequence(t *testing.T) {
	want := []time.Duration{
		2 * time.Second, 5 * time.Second, 10 * time.Second,
		20 * time.Second, 30 * time.Second, 60 * time.Second, 60 * time.Second,
	}
	r := newTestReconnector()
	r.Enable(true)
	r.OnSessionLost(rcT0, "会话失效")

	now := rcT0
	for i := range want {
		at := now.Add(want[i])
		if got := r.Due(at.Add(-time.Millisecond)); got != StepIdle {
			t.Fatalf("第 %d 次退避未到，不应动作，得到 %v", i+1, got)
		}
		if got := r.Due(at); got != StepRelogin {
			t.Fatalf("第 %d 次应重登，得到 %v", i+1, got)
		}
		// 网络错误：等待下一个退避窗口。
		r.ReloginNetworkErr(at, "连不上")
		next := want[i]
		if i+1 < len(want) {
			next = want[i+1]
		}
		if got, expect := r.NextRetryAt(), at.Add(next); !got.Equal(expect) {
			t.Fatalf("第 %d 次退避应为 %v，得到 %v", i+1, next, got.Sub(at))
		}
		now = at
	}
	// 会话失效本身也算一次退避（第 1 次），之后 7 次网络失败各算一次。
	if got, expect := r.Attempts(), len(want)+1; got != expect {
		t.Fatalf("重试次数应为 %d，得到 %d", expect, got)
	}
}

func TestReconnectReloginDoesNotProbeImmediately(t *testing.T) {
	r := newTestReconnector()
	r.Enable(false)
	r.OnSessionLost(rcT0, "会话失效")

	at := rcT0.Add(2 * time.Second)
	if got := r.Due(at); got != StepRelogin {
		t.Fatalf("应先重登，得到 %v", got)
	}
	r.ReloginOK(at)
	if got := r.Due(at.Add(time.Second)); got != StepIdle {
		t.Fatalf("重登成功后不应立刻再探活，得到 %v", got)
	}
	if got := r.Due(at.Add(30 * time.Second)); got != StepProbe {
		t.Fatalf("30 秒后应恢复常规探活，得到 %v", got)
	}
}

func TestReconnectSessionLostSwitchesToRelogin(t *testing.T) {
	r := newTestReconnector()
	r.Enable(true)
	r.ProbeOK(rcT0)

	r.OnSessionLost(rcT0.Add(30*time.Second), "会话已过期")
	if r.Connected() {
		t.Fatalf("会话失效后不应再算已连接")
	}
	// 退避 2s 后改走重登
	if got := r.Due(rcT0.Add(31 * time.Second)); got != StepIdle {
		t.Fatalf("退避期内不应动作，得到 %v", got)
	}
	if got := r.Due(rcT0.Add(32 * time.Second)); got != StepRelogin {
		t.Fatalf("退避结束后应重登，得到 %v", got)
	}
}

func TestReconnectResumeAfterProbeOK(t *testing.T) {
	r := newTestReconnector()
	r.Enable(true)
	r.ProbeOK(rcT0)
	r.ProbeNetworkErr(rcT0.Add(30*time.Second), "e")
	r.ProbeNetworkErr(rcT0.Add(60*time.Second), "e")
	if r.Connected() {
		t.Fatalf("应已断线")
	}

	r.ProbeOK(rcT0.Add(62 * time.Second))
	if !r.Connected() || r.Reconnecting() {
		t.Fatalf("探活成功后应恢复连接")
	}
	if r.Attempts() != 0 {
		t.Fatalf("恢复后重试次数应清零，得到 %d", r.Attempts())
	}
	if !r.NextRetryAt().IsZero() {
		t.Fatalf("已连接时不应再有下次重试时间")
	}
}

func TestReconnectAuthCooldown(t *testing.T) {
	r := newTestReconnector()
	r.Enable(true)
	r.OnSessionLost(rcT0, "会话失效")

	// 第一次重登被拒：进入退避 + 重登冷却
	r.ReloginRejected(rcT0.Add(2*time.Second), "用户名或密码错误")
	t1 := rcT0.Add(2 * time.Second)
	if got, want := r.NextRetryAt(), t1.Add(time.Minute); !got.Equal(want) {
		t.Fatalf("重登冷却应优先于退避，得到 %v 期望 %v", got.Sub(t1), want.Sub(t1))
	}
	if got := r.Due(t1.Add(30 * time.Second)); got != StepIdle {
		t.Fatalf("重登冷却期内不应重登，得到 %v", got)
	}
	if got := r.Due(t1.Add(time.Minute)); got != StepRelogin {
		t.Fatalf("冷却结束后应重登，得到 %v", got)
	}
}

func TestReconnectStopsAfterMaxAuthFails(t *testing.T) {
	r := newTestReconnector()
	r.Enable(true)
	r.OnSessionLost(rcT0, "会话失效")

	now := rcT0
	for i := 0; i < 3; i++ {
		now = now.Add(time.Minute)
		if got := r.Due(now); got != StepRelogin {
			t.Fatalf("第 %d 次应重登，得到 %v", i+1, got)
		}
		r.ReloginRejected(now, "用户名或密码错误")
	}

	stopped, reason := r.Stopped()
	if !stopped || reason == "" {
		t.Fatalf("连续 3 次认证失败应停手，stopped=%v reason=%q", stopped, reason)
	}
	if r.Reconnecting() {
		t.Fatalf("停手后不应再显示重连中")
	}
	if got := r.Due(now.Add(time.Hour)); got != StepIdle {
		t.Fatalf("停手后不应再动作，得到 %v", got)
	}
}

func TestReconnectRateLimitedCoolDown(t *testing.T) {
	r := newTestReconnector()
	r.Enable(true)
	r.OnSessionLost(rcT0, "会话失效")

	r.ReloginLocked(rcT0.Add(2*time.Second), "登录失败次数过多，请稍后再试")
	if got := r.Due(rcT0.Add(5 * time.Minute)); got != StepIdle {
		t.Fatalf("被锁期间不应重登，得到 %v", got)
	}
	if got := r.Due(rcT0.Add(2*time.Second + 10*time.Minute)); got != StepRelogin {
		t.Fatalf("冷却结束后应再试一次，得到 %v", got)
	}
	if stopped, _ := r.Stopped(); stopped {
		t.Fatalf("被限速不算停手")
	}
}

func TestReconnectReloginNetworkErrIgnoresCooldown(t *testing.T) {
	r := newTestReconnector()
	r.Enable(true)
	r.OnSessionLost(rcT0, "会话失效")

	// 网络错误既不占认证次数、也不推进重登冷却：连打 5 次都还能继续重登。
	for i := 0; i < 5; i++ {
		at := r.NextRetryAt()
		if got := r.Due(at); got != StepRelogin {
			t.Fatalf("第 %d 次网络失败后应能重登，得到 %v", i+1, got)
		}
		r.ReloginNetworkErr(at, "网络不通")
	}
	if stopped, reason := r.Stopped(); stopped {
		t.Fatalf("网络错误不应导致停手，reason=%q", reason)
	}
	if r.Attempts() != 6 {
		t.Fatalf("退避次数应为 6（含会话失效那次），得到 %d", r.Attempts())
	}
}

func TestReconnectStopAndReset(t *testing.T) {
	r := newTestReconnector()
	r.Enable(true)
	r.Stop("服务端证书指纹与固定值不一致")

	if stopped, reason := r.Stopped(); !stopped || reason == "" {
		t.Fatalf("应停手并带原因")
	}
	if got := r.Due(rcT0.Add(time.Hour)); got != StepIdle {
		t.Fatalf("停手后不应动作，得到 %v", got)
	}

	// 手动登录后重新启用应清空停手状态
	r.Enable(true)
	if stopped, _ := r.Stopped(); stopped {
		t.Fatalf("重新启用后不应仍是停手状态")
	}
	if got := r.Due(rcT0); got != StepProbe {
		t.Fatalf("重新启用后应立即探活，得到 %v", got)
	}
}

func TestReconnectDisable(t *testing.T) {
	r := newTestReconnector()
	r.Enable(true)
	r.ProbeOK(rcT0)
	r.Disable()

	if r.Enabled() || r.Connected() || r.Reconnecting() {
		t.Fatalf("Disable 后各项应为 false")
	}
	if got := r.Due(rcT0.Add(time.Hour)); got != StepIdle {
		t.Fatalf("Disable 后不应动作，得到 %v", got)
	}
}

func TestReconnectSetSession(t *testing.T) {
	r := newTestReconnector()
	r.Enable(false)
	r.OnSessionLost(rcT0, "会话失效")
	if got := r.Due(rcT0.Add(2 * time.Second)); got != StepRelogin {
		t.Fatalf("没有会话时应重登，得到 %v", got)
	}

	r.SetSession(true)
	if got := r.Due(rcT0.Add(2 * time.Second)); got != StepProbe {
		t.Fatalf("有会话时应改为探活，得到 %v", got)
	}

	r.SetSession(false)
	if r.Connected() {
		t.Fatalf("丢弃会话后不应仍算已连接")
	}
}
