package agent

import (
	"strconv"
	"time"
)

// ---------- 断线重连（纯策略） ----------
//
// 这里只回答「什么时候探活、什么时候用保存的密码重登、什么时候放弃」，
// 不碰网络，也不自己读时钟（now 由调用方传入），所以可以直接单测。
// 真正的探活与重登由装配层（cmd/lanbox-agent）按 Step 执行，再把结果回灌。

// Step 是重连状态机对「此刻该做什么」的判断。
type Step int

const (
	// StepIdle 表示什么都不用做：已连接且没到探活时间、还在退避等待、已停手，或未启用。
	StepIdle Step = iota
	// StepProbe 表示用现有会话探活（GET /api/me）。
	StepProbe
	// StepRelogin 表示没有可用会话，应当用保存的凭据重新登录。
	StepRelogin
)

// ReconnectPolicy 是重连的节奏与放弃条件。
type ReconnectPolicy struct {
	ProbeEvery        time.Duration   // 已连接时的探活间隔
	ConfirmFails      int             // 连续多少次网络失败才判定断线（防抖）
	Backoff           []time.Duration // 断线后的重试间隔；用完后一直沿用最后一个
	AuthCooldown      time.Duration   // 两次自动重登之间的最小间隔，避免打爆服务端登录限速
	RateLimitCooldown time.Duration   // 服务端提示「登录失败次数过多」后的冷却
	MaxAuthFails      int             // 连续认证失败达到该次数就停手，等用户手动登录
}

// DefaultReconnectPolicy 是界面默认使用的策略：
// 30 秒探活、连续 2 次失败判断线、2s→60s 退避、每分钟最多重登一次、连续 3 次认证失败停手。
func DefaultReconnectPolicy() ReconnectPolicy {
	return ReconnectPolicy{
		ProbeEvery:   30 * time.Second,
		ConfirmFails: 2,
		Backoff: []time.Duration{
			2 * time.Second, 5 * time.Second, 10 * time.Second,
			20 * time.Second, 30 * time.Second, 60 * time.Second,
		},
		AuthCooldown:      time.Minute,
		RateLimitCooldown: 10 * time.Minute,
		MaxAuthFails:      3,
	}
}

// Reconnector 是断线重连状态机。零值不可用，请用 NewReconnector 构造。
type Reconnector struct {
	policy ReconnectPolicy
	now    func() time.Time

	enabled    bool // 守护是否运行（登录过且有可用凭据）
	connected  bool // 服务端可达且会话有效
	hasSession bool // 手里是否有可用会话（false 时需要重登）
	stopped    bool // 已停手，等用户手动处理
	reason     string
	lastErr    string

	netFails  int // 连续网络失败次数
	attempts  int // 本轮断线的重试次数
	authFails int // 连续认证失败次数

	lastProbe time.Time // 上次探活时间
	lastAuth  time.Time // 上次真正打到 /api/login 的时间
	nextAt    time.Time // 下次允许尝试的时间
	rateUntil time.Time // 被服务端锁登录后的冷却截止
}

// NewReconnector 构造状态机（初始为未启用）。
func NewReconnector(p ReconnectPolicy) *Reconnector {
	if p.ConfirmFails < 1 {
		p.ConfirmFails = 1
	}
	if len(p.Backoff) == 0 {
		p.Backoff = []time.Duration{30 * time.Second}
	}
	if p.MaxAuthFails < 1 {
		p.MaxAuthFails = 1
	}
	return &Reconnector{policy: p, now: time.Now}
}

// setClock 便于测试注入假时钟。
func (r *Reconnector) setClock(now func() time.Time) { r.now = now }

// Enable 开始（或重置）守护。hasSession 表示当前是否已持有可用会话。
func (r *Reconnector) Enable(hasSession bool) {
	r.enabled = true
	r.connected = hasSession
	r.hasSession = hasSession
	r.stopped = false
	r.reason = ""
	r.lastErr = ""
	// 种一次「已经失败过」：启动瞬间探活就失败属于确定断线，不必再等两次防抖；
	// 防抖只用于运行中区分抖动。
	r.netFails = r.policy.ConfirmFails - 1
	r.attempts = 0
	r.authFails = 0
	r.lastProbe = time.Time{}
	r.lastAuth = time.Time{}
	r.nextAt = time.Time{}
	r.rateUntil = time.Time{}
}

// Disable 关闭守护（退出登录、未保存凭据时）。状态全部清空，但保留注入的时钟。
func (r *Reconnector) Disable() {
	r.enabled = false
	r.connected = false
	r.hasSession = false
	r.stopped = false
	r.reason = ""
	r.lastErr = ""
	r.netFails = 0
	r.attempts = 0
	r.authFails = 0
	r.lastProbe = time.Time{}
	r.lastAuth = time.Time{}
	r.nextAt = time.Time{}
	r.rateUntil = time.Time{}
}

// SetSession 标记会话是否可用（手动登录成功或会话被丢弃）。
func (r *Reconnector) SetSession(ok bool) {
	r.hasSession = ok
	if !ok {
		r.connected = false
	}
}

// Due 返回此刻该做的事。
func (r *Reconnector) Due(now time.Time) Step {
	if !r.enabled || r.stopped {
		return StepIdle
	}
	if r.connected {
		if now.Sub(r.lastProbe) < r.policy.ProbeEvery {
			return StepIdle
		}
		return StepProbe
	}
	if now.Before(r.nextAt) || now.Before(r.rateUntil) {
		return StepIdle
	}
	if r.hasSession {
		return StepProbe
	}
	if !r.authAllowed(now) {
		return StepIdle
	}
	return StepRelogin
}

// authAllowed 报告此刻是否允许自动重登（冷却与次数上限都满足）。
func (r *Reconnector) authAllowed(now time.Time) bool {
	if r.authFails >= r.policy.MaxAuthFails {
		return false
	}
	if r.lastAuth.IsZero() {
		return true
	}
	return now.Sub(r.lastAuth) >= r.policy.AuthCooldown
}

// ProbeOK 记录一次成功的探活。
func (r *Reconnector) ProbeOK(now time.Time) {
	r.lastProbe = now
	r.netFails = 0
	r.connected = true
	r.hasSession = true
	r.attempts = 0
	r.authFails = 0
	r.reason = ""
	r.lastErr = ""
	r.nextAt = time.Time{}
}

// ProbeNetworkErr 记录一次网络类探活失败；连续失败达到阈值时判定断线。
func (r *Reconnector) ProbeNetworkErr(now time.Time, errText string) {
	r.lastProbe = now
	r.netFails++
	r.lastErr = errText
	if r.connected {
		if r.netFails < r.policy.ConfirmFails {
			return
		}
		r.connected = false
	}
	r.schedule(now)
}

// OnSessionLost 记录会话失效（探活 401 或上传时被拒）：下一轮改走重登。
func (r *Reconnector) OnSessionLost(now time.Time, errText string) {
	r.lastProbe = now
	r.connected = false
	r.hasSession = false
	r.lastErr = errText
	r.schedule(now)
}

// ReloginOK 记录自动重登成功。刚拿到新会话，因此把 lastProbe 一起推进，
// 避免成功后又立刻多发一次 /api/me。
func (r *Reconnector) ReloginOK(now time.Time) {
	r.lastAuth = now
	r.lastProbe = now
	r.connected = true
	r.hasSession = true
	r.attempts = 0
	r.authFails = 0
	r.netFails = 0
	r.reason = ""
	r.lastErr = ""
	r.nextAt = time.Time{}
}

// ReloginNetworkErr 记录重登时的网络错误：不占认证次数，也不推进重登冷却。
func (r *Reconnector) ReloginNetworkErr(now time.Time, errText string) {
	r.connected = false
	r.lastErr = errText
	r.schedule(now)
}

// ReloginLocked 记录服务端「登录失败次数过多」：冷却一段时间再试一次。
func (r *Reconnector) ReloginLocked(now time.Time, errText string) {
	r.connected = false
	r.lastAuth = now
	r.lastErr = errText
	r.rateUntil = now.Add(r.policy.RateLimitCooldown)
	r.schedule(now)
}

// ReloginRejected 记录一次认证失败（用户名/密码不对）；达到上限就停手。
func (r *Reconnector) ReloginRejected(now time.Time, errText string) {
	r.connected = false
	r.lastAuth = now
	r.authFails++
	r.lastErr = errText
	if r.authFails >= r.policy.MaxAuthFails {
		r.Stop("已连续 " + strconv.Itoa(r.authFails) + " 次登录失败，请手动登录")
		return
	}
	r.schedule(now)
	// 退避间隔不得短于重登冷却，否则下一轮会被 authAllowed 挡住但仍然空转。
	if wait := now.Add(r.policy.AuthCooldown); r.nextAt.Before(wait) {
		r.nextAt = wait
	}
}

// Stop 停手（证书不符、地址非法、密码未保存等不可自愈的情况）。
func (r *Reconnector) Stop(reason string) {
	r.stopped = true
	r.connected = false
	r.reason = reason
	r.lastErr = reason
}

// schedule 推进重试次数并按退避表设定下次允许尝试的时间。
func (r *Reconnector) schedule(now time.Time) {
	r.attempts++
	idx := r.attempts - 1
	if idx >= len(r.policy.Backoff) {
		idx = len(r.policy.Backoff) - 1
	}
	if idx < 0 {
		idx = 0
	}
	r.nextAt = now.Add(r.policy.Backoff[idx])
}

// Enabled 报告守护是否在运行。
func (r *Reconnector) Enabled() bool { return r.enabled }

// Connected 报告当前是否已连上服务端。
func (r *Reconnector) Connected() bool { return r.enabled && r.connected }

// Reconnecting 报告是否处于「断线且还在自动重连」。
func (r *Reconnector) Reconnecting() bool { return r.enabled && !r.connected && !r.stopped }

// Stopped 返回是否已停手及原因。
func (r *Reconnector) Stopped() (bool, string) { return r.stopped, r.reason }

// LastErr 返回最近一次失败原因（成功时为空）。
func (r *Reconnector) LastErr() string { return r.lastErr }

// Attempts 返回本轮断线已经重试的次数。
func (r *Reconnector) Attempts() int { return r.attempts }

// NextRetryAt 返回下次尝试的时间；已连接、已停手或未启用时返回零值。
func (r *Reconnector) NextRetryAt() time.Time {
	if !r.enabled || r.connected || r.stopped {
		return time.Time{}
	}
	t := r.nextAt
	if r.rateUntil.After(t) {
		t = r.rateUntil
	}
	return t
}
