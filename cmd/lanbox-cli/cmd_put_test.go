package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"testing"
)

// TestPutRequiresInput 断言既没有文字也没有文件时直接报用法错误。
func TestPutRequiresInput(t *testing.T) {
	var out, errOut bytes.Buffer
	g := &globals{stdout: &out, stderr: &errOut}
	if err := cmdPut(context.Background(), g, nil, os.Stdin); !errors.Is(err, errUsage) {
		t.Fatalf("无输入应返回 errUsage，得到 %v", err)
	}
}

// TestPutWithoutSessionAsksLogin 断言没有会话文件时提示先登录。
func TestPutWithoutSessionAsksLogin(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	var out, errOut bytes.Buffer
	g := &globals{stdout: &out, stderr: &errOut}
	err := cmdPut(context.Background(), g, []string{"-m", "hi"}, os.Stdin)
	if err == nil || !errors.Is(err, errAuth) {
		t.Fatalf("应返回认证错误（尚未登录），得到 %v", err)
	}
}

// TestPutRejectsOversizedStdin 断言超长标准输入直接报用法错误，而不是静默截断。
func TestPutRejectsOversizedStdin(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "in")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close() // Windows 上未关闭的文件会阻塞 t.TempDir 的清理
	if _, err := f.Write(bytes.Repeat([]byte("a"), maxStdinText+1)); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	g := &globals{stdout: io.Discard, stderr: io.Discard}
	if err := cmdPut(context.Background(), g, []string{"-"}, f); !errors.Is(err, errUsage) {
		t.Fatalf("超长 stdin 应返回 errUsage，得到 %v", err)
	}
}

// TestPutRejectsTextFlagWithStdin 断言 -m 与 - 同时给出是用法错误。
func TestPutRejectsTextFlagWithStdin(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "in")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close() // Windows 上未关闭的文件会阻塞 t.TempDir 的清理
	f.WriteString("x")
	f.Seek(0, io.SeekStart)
	g := &globals{stdout: io.Discard, stderr: io.Discard}
	if err := cmdPut(context.Background(), g, []string{"-m", "文字", "-"}, f); !errors.Is(err, errUsage) {
		t.Fatalf("-m 与 - 同给应报用法错误，得到 %v", err)
	}
}

// TestPutRejectsMultipleStdin 断言 "-" 只能出现一次。
func TestPutRejectsMultipleStdin(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "in")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close() // Windows 上未关闭的文件会阻塞 t.TempDir 的清理
	f.WriteString("x")
	f.Seek(0, io.SeekStart)
	g := &globals{stdout: io.Discard, stderr: io.Discard}
	if err := cmdPut(context.Background(), g, []string{"-", "-"}, f); !errors.Is(err, errUsage) {
		t.Fatalf("多个 - 应报用法错误，得到 %v", err)
	}
}

// TestPutDoubleDashKeepsDashPaths 断言 "--" 之后以 - 开头的路径被当作文件名。
func TestPutDoubleDashKeepsDashPaths(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	var out, errOut bytes.Buffer
	g := &globals{stdout: &out, stderr: &errOut}
	// 文件不存在应报读取错误，而不是"未知选项 -s"或"-m 缺值"。
	err := cmdPut(context.Background(), g, []string{"--", "-s"}, os.Stdin)
	if err == nil {
		t.Fatal("不存在的文件应报错")
	}
	if errors.Is(err, errUsage) {
		t.Fatalf("-- 之后的 -s 应是文件路径，得到用法错误: %v", err)
	}
}
