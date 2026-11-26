//go:build windows

package main

import (
	"context"
	"errors"
	"net/http"
	"time"

	"lanbox/internal/agent"
	"lanbox/internal/client"
)

// ---------- 断线重连（装配层） ----------
//
// 节奏与放弃条件都在 agent.Reconnector 里（纯逻辑、可单测），这里只负责：
// 按状态机给出的 Step 真正去打 /api/me 或 /api/login，再把结果回灌。

// reconnectLoop 每秒推一次状态机；绝大多数时候 Due 返回 StepIdle，开销就是一次空转。
func (a *App) reconnectLoop(ctx context.Context) {
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			a.reconnectStep(ctx)
		}
	}
}

func (a *App) reconnectStep(ctx context.Context) {
	switch a.rc.Due(time.Now()) {
	case agent.StepProbe:
		a.probeServer(ctx)
	case agent.StepRelogin:
		a.reloginStep(ctx)
	default:
		return
	}
	a.emitState()
}

// enableReconnect 按当前会话与保存的凭据决定守护是否运行。
func (a *App) enableReconnect() {
	if a.holder.get() != nil {
		a.rc.Enable(true)
		return
	}
	a.mu.Lock()
	s := a.settings
	a.mu.Unlock()
	// 有地址、用户名和保存的密码才谈得上自动重连；否则只能等用户手动登录。
	if s.Server.URL != "" && s.Server.Username != "" && s.password() != "" {
		a.rc.Enable(false)
		return
	}
	a.rc.Disable()
}

// probeServer 用现有会话探活一次，并把结果回灌状态机。
func (a *App) probeServer(ctx context.Context) {
	cli := a.holder.get()
	if cli == nil {
		a.dropSession("没有可用会话")
		return
	}
	cctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	_, err := cli.Me(cctx)

	if err == nil {
		wasDown := !a.rc.Connected()
		a.rc.ProbeOK(time.Now())
		a.mgr.SetAvailable(true)
		a.syncWatcher()
		if wasDown {
			a.logf("已重新连接到服务器")
		}
		return
	}
	if errors.Is(err, context.Canceled) {
		return // 正在退出
	}

	switch f := classify(err); {
	case f == agent.FailureAuth:
		a.dropSession("会话已失效: " + err.Error())
	case f == agent.FailureTLS || f == agent.FailurePermanent:
		// 指纹不符、地址非法这类问题重试多少次都不会好，停手等用户处理。
		a.stopReconnect("无法自动重连: " + err.Error())
	default:
		wasUp := a.rc.Connected()
		a.rc.ProbeNetworkErr(time.Now(), err.Error())
		if !a.rc.Connected() {
			a.mgr.SetAvailable(false)
			if wasUp {
				a.logf("与服务器断开: %v", err)
			}
		}
	}
}

// reloginStep 用保存的密码重登一次。
func (a *App) reloginStep(ctx context.Context) {
	err := a.relogin(ctx)
	if err == nil {
		a.rc.ReloginOK(time.Now())
		a.mgr.SetAvailable(true)
		a.syncWatcher()
		a.logf("自动重新登录成功")
		return
	}
	if errors.Is(err, context.Canceled) {
		return
	}
	a.noteReloginFailure(err)
}

// noteReloginFailure 把重登失败分类后交给状态机；不可自愈的情况直接停手。
func (a *App) noteReloginFailure(err error) {
	now := time.Now()
	if errors.Is(err, errNoPassword) {
		a.stopReconnect("未保存密码，无法自动重连，请手动登录")
		return
	}
	switch f := classify(err); {
	case isLoginLocked(err):
		// 服务端登录限速：冷却后再试一次，且不计入认证失败次数。
		a.rc.ReloginLocked(now, err.Error())
		a.logf("自动重连被服务端限速: %v", err)
	case f == agent.FailureAuth:
		a.rc.ReloginRejected(now, err.Error())
		a.logf("自动重连被拒: %v", err)
	case f == agent.FailureTLS || f == agent.FailurePermanent:
		a.stopReconnect("无法自动重连: " + err.Error())
	default:
		a.rc.ReloginNetworkErr(now, err.Error())
		a.logf("自动重连失败: %v", err)
	}
}

// dropSession 丢掉手里的会话（会话失效），但保留地址与用户名以便重登与展示。
func (a *App) dropSession(reason string) {
	a.holder.set(nil)
	a.mgr.SetAvailable(false)
	a.syncWatcher()
	a.rc.OnSessionLost(time.Now(), reason)
	a.logf("%s", reason)
}

// stopReconnect 停手：重连无法自愈，等用户手动登录。
func (a *App) stopReconnect(reason string) {
	a.rc.Stop(reason)
	a.mgr.SetAvailable(false)
	a.setLastErr(reason)
	a.logf("%s", reason)
}

// isLoginLocked 判断是否是服务端「登录失败次数过多」（403 login_locked，或 429）。
func isLoginLocked(err error) bool {
	var ce *client.Error
	if errors.As(err, &ce) {
		return ce.Code == "login_locked" || ce.Status == http.StatusTooManyRequests
	}
	return false
}
