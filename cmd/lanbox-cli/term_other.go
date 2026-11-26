//go:build !linux

package main

import (
	"bufio"
	"fmt"
	"os"
)

func isTerminal(fd uintptr) bool { return false }

func readPasswordNoEcho(f *os.File) (string, error) {
	fmt.Fprintln(os.Stderr, "（当前平台不支持无回显输入，密码会显示出来）")
	return bufio.NewReader(f).ReadString('\n')
}
