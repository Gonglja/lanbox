//go:build linux

package main

import (
	"bufio"
	"os"
	"syscall"
	"unsafe"
)

// isTerminal 判断 fd 是否接在终端上。
func isTerminal(fd uintptr) bool {
	var t syscall.Termios
	_, _, errno := syscall.Syscall6(syscall.SYS_IOCTL, fd,
		uintptr(syscall.TCGETS), uintptr(unsafe.Pointer(&t)), 0, 0, 0)
	return errno == 0
}

// readPasswordNoEcho 关闭回显读取一行，结束后恢复原状态。
func readPasswordNoEcho(f *os.File) (string, error) {
	fd := f.Fd()
	var old syscall.Termios
	if _, _, errno := syscall.Syscall6(syscall.SYS_IOCTL, fd,
		uintptr(syscall.TCGETS), uintptr(unsafe.Pointer(&old)), 0, 0, 0); errno != 0 {
		return bufio.NewReader(f).ReadString('\n')
	}
	noEcho := old
	noEcho.Lflag &^= syscall.ECHO
	if _, _, errno := syscall.Syscall6(syscall.SYS_IOCTL, fd,
		uintptr(syscall.TCSETS), uintptr(unsafe.Pointer(&noEcho)), 0, 0, 0); errno != 0 {
		return bufio.NewReader(f).ReadString('\n')
	}
	defer func() {
		syscall.Syscall6(syscall.SYS_IOCTL, fd,
			uintptr(syscall.TCSETS), uintptr(unsafe.Pointer(&old)), 0, 0, 0)
	}()
	return bufio.NewReader(f).ReadString('\n')
}
