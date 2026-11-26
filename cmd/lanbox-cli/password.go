package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"
)

// readPassword 按优先级取密码：LANBOX_PASSWORD → 管道 stdin → 终端无回显提示。
func readPassword(stdin *os.File, stderr io.Writer) (string, error) {
	if pw, ok := os.LookupEnv("LANBOX_PASSWORD"); ok && pw != "" {
		return pw, nil
	}
	if !isTerminal(stdin.Fd()) {
		line, err := bufio.NewReader(stdin).ReadString('\n')
		if err != nil && line == "" {
			return "", fmt.Errorf("从标准输入读取密码失败: %w", err)
		}
		return strings.TrimRight(line, "\r\n"), nil
	}
	fmt.Fprint(stderr, "密码: ")
	pw, err := readPasswordNoEcho(stdin)
	fmt.Fprintln(stderr)
	if err != nil {
		return "", err
	}
	return strings.TrimRight(pw, "\r\n"), nil
}
