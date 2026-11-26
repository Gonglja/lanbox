package auth

import (
	"sync"
	"time"
)

type limitEntry struct {
	fails       int
	windowStart time.Time
	lockedUntil time.Time
}

// Limiter 按来源 IP 限制登录失败次数。
type Limiter struct {
	window   time.Duration
	maxFails int
	lock     time.Duration
	now      func() time.Time
	mu       sync.Mutex
	entries  map[string]*limitEntry
}

// NewLimiter 创建限速器：window 内失败 maxFails 次则锁 lock 时长。
func NewLimiter(window time.Duration, maxFails int, lock time.Duration) *Limiter {
	return &Limiter{
		window: window, maxFails: maxFails, lock: lock,
		now:     time.Now,
		entries: map[string]*limitEntry{},
	}
}

// Allowed 报告该 IP 是否允许尝试登录。
func (l *Limiter) Allowed(ip string) bool {
	if l == nil {
		return true
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	e, ok := l.entries[ip]
	if !ok {
		return true
	}
	now := l.now()
	if now.Before(e.lockedUntil) {
		return false
	}
	if !e.lockedUntil.IsZero() && now.After(e.lockedUntil) {
		delete(l.entries, ip)
	}
	return true
}

// Fail 记录一次失败；达到阈值时开始锁定。
func (l *Limiter) Fail(ip string) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	e, ok := l.entries[ip]
	if !ok || now.Sub(e.windowStart) > l.window {
		l.entries[ip] = &limitEntry{fails: 1, windowStart: now}
		return
	}
	e.fails++
	if e.fails >= l.maxFails {
		e.lockedUntil = now.Add(l.lock)
	}
}

// Reset 清空该 IP 的失败记录（登录成功时调用）。
func (l *Limiter) Reset(ip string) {
	if l == nil {
		return
	}
	l.mu.Lock()
	delete(l.entries, ip)
	l.mu.Unlock()
}
