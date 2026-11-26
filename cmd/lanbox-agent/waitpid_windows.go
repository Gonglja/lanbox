//go:build windows

package main

import (
	"strconv"
	"strings"
	"syscall"
	"time"
)

var (
	kernel32           = syscall.NewLazyDLL("kernel32.dll")
	procOpenProcess    = kernel32.NewProc("OpenProcess")
	procWaitForSingle  = kernel32.NewProc("WaitForSingleObject")
	procCloseHandle    = kernel32.NewProc("CloseHandle")
	processSynchronize = 0x00100000
)

// waitPidArg 从命令行里取出隐藏参数 -wait-pid 的值（没有则返回 0）。
// 升级时旧进程用它告诉新进程「先等我退出再抢单实例锁」。
func waitPidArg(args []string) uint32 {
	for i, a := range args {
		v := ""
		switch {
		case a == "-wait-pid" || a == "--wait-pid":
			if i+1 < len(args) {
				v = args[i+1]
			}
		case strings.HasPrefix(a, "-wait-pid="), strings.HasPrefix(a, "--wait-pid="):
			v = a[strings.IndexByte(a, '=')+1:]
		default:
			continue
		}
		n, err := strconv.ParseUint(strings.TrimSpace(v), 10, 32)
		if err == nil && n > 0 {
			return uint32(n)
		}
	}
	return 0
}

// waitForProcess 等指定进程退出（最多 timeout）。进程已不存在时立即返回。
func waitForProcess(pid uint32, timeout time.Duration) {
	h, _, _ := procOpenProcess.Call(uintptr(processSynchronize), 0, uintptr(pid))
	if h == 0 {
		return // 进程已经不在了
	}
	defer procCloseHandle.Call(h)
	ms := uintptr(timeout / time.Millisecond)
	if ms == 0 {
		ms = 15000
	}
	procWaitForSingle.Call(h, ms) // WAIT_OBJECT_0 / WAIT_TIMEOUT 都往下走
}

// argsWithoutWaitPid 去掉已有的 -wait-pid，避免拉起新进程时参数越堆越多。
func argsWithoutWaitPid(args []string) []string {
	out := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "-wait-pid" || a == "--wait-pid":
			i++ // 跳过值
		case strings.HasPrefix(a, "-wait-pid="), strings.HasPrefix(a, "--wait-pid="):
		default:
			out = append(out, a)
		}
	}
	return out
}
