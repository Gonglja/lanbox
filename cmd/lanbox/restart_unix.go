//go:build unix

package main

import (
	"os"
	"syscall"
)

// restartProcess 用 exec 原地重启（PID 不变），交给 systemd 的 Restart=always 兜底。
func restartProcess() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	return syscall.Exec(exe, os.Args, os.Environ())
}
