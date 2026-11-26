package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"strings"
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
	if err == nil || !strings.Contains(err.Error(), "尚未登录") {
		t.Fatalf("应提示尚未登录，得到 %v", err)
	}
}

// TestFlagsFirst 断言写在位置参数后面的选项也会被移到前面。
func TestFlagsFirst(t *testing.T) {
	got := flagsFirst([]string{"ID", "-o", "目录", "--resume"}, "-o", "--zip")
	want := []string{"-o", "目录", "--resume", "ID"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("重排结果不对: %v，期望 %v", got, want)
	}
	// 不占值的布尔选项不能吞掉后面的位置参数。
	got = flagsFirst([]string{"ID", "--undo"}, "-o")
	if strings.Join(got, "|") != "--undo|ID" {
		t.Fatalf("布尔选项重排不对: %v", got)
	}
	// 没有位置参数时保持原样。
	got = flagsFirst([]string{"-n", "5"}, "-n")
	if strings.Join(got, "|") != "-n|5" {
		t.Fatalf("纯选项应保持原样: %v", got)
	}
}
