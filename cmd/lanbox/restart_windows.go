//go:build windows

package main

import (
	"os"
	"os/exec"
	"syscall"
)

// restartProcess 在 Windows 上没有 exec(2)，用「启动新进程 + 退出当前进程」实现自重启。
// 调用前监听套接字已由 shutdown() 关闭，因此新进程可以重新绑定同一端口。
func restartProcess() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	cmd := exec.Command(exe, os.Args[1:]...)
	cmd.Stdin = nil
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = os.Environ()
	// 新进程独立于当前进程组，父进程退出后不会被一并结束。
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP,
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	// 当前进程立即退出，让新进程接管。
	os.Exit(0)
	return nil
}
