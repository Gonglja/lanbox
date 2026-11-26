package auth

import (
	"testing"
	"time"
)

func TestLimiterLocksAfterThreshold(t *testing.T) {
	l := NewLimiter(15*time.Minute, 5, 10*time.Minute)
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	l.now = func() time.Time { return now }

	for i := 0; i < 4; i++ {
		l.Fail("1.2.3.4")
	}
	if !l.Allowed("1.2.3.4") {
		t.Fatal("4 次失败后仍应允许")
	}
	l.Fail("1.2.3.4")
	if l.Allowed("1.2.3.4") {
		t.Fatal("第 5 次失败后应锁定")
	}
	if !l.Allowed("9.9.9.9") {
		t.Fatal("其它 IP 不受影响")
	}
	now = now.Add(11 * time.Minute)
	if !l.Allowed("1.2.3.4") {
		t.Fatal("锁定到期后应允许")
	}
}

func TestLimiterWindowResets(t *testing.T) {
	l := NewLimiter(15*time.Minute, 5, 10*time.Minute)
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	l.now = func() time.Time { return now }
	for i := 0; i < 4; i++ {
		l.Fail("ip")
	}
	now = now.Add(16 * time.Minute)
	l.Fail("ip")
	if !l.Allowed("ip") {
		t.Fatal("窗口过期后失败计数应清零")
	}
}

func TestLimiterReset(t *testing.T) {
	l := NewLimiter(15*time.Minute, 2, 10*time.Minute)
	l.Fail("ip")
	l.Reset("ip")
	l.Fail("ip")
	if !l.Allowed("ip") {
		t.Fatal("Reset 后计数应重新开始")
	}
}
