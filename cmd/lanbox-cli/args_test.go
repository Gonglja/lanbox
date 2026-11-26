package main

import (
	"bytes"
	"strings"
	"testing"
)

// TestSplitGlobals 断言全局选项写在子命令前后都能被识别。
func TestSplitGlobals(t *testing.T) {
	globals, rest := splitGlobals([]string{"-s", "https://a", "login", "-u", "admin"})
	if strings.Join(globals, "|") != "-s|https://a" {
		t.Fatalf("子命令前的全局选项提取错: %v", globals)
	}
	if strings.Join(rest, "|") != "login|-u|admin" {
		t.Fatalf("其余参数错: %v", rest)
	}

	globals, rest = splitGlobals([]string{"login", "-s", "https://a", "-insecure", "-u", "admin"})
	if strings.Join(globals, "|") != "-s|https://a|-insecure" {
		t.Fatalf("子命令后的全局选项提取错: %v", globals)
	}
	if strings.Join(rest, "|") != "login|-u|admin" {
		t.Fatalf("子命令自己的选项应留在原处: %v", rest)
	}

	// -s=值 的写法不能被拆开。
	globals, _ = splitGlobals([]string{"whoami", "-s=https://a"})
	if strings.Join(globals, "|") != "-s=https://a" {
		t.Fatalf("-s=值 提取错: %v", globals)
	}
}

// TestRunAcceptsGlobalsAfterCommand 断言 login 后面的 -s/-insecure 生效：
// 连不上端口应返回 exitNetwork，而不是用法错误。
func TestRunAcceptsGlobalsAfterCommand(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("LANBOX_PASSWORD", "x")
	var out, errOut bytes.Buffer
	code := run([]string{"login", "-s", "https://127.0.0.1:1", "-insecure", "-u", "admin"}, &out, &errOut)
	if code != exitNetwork {
		t.Fatalf("应返回 exitNetwork(%d)，得到 %d：%s", exitNetwork, code, errOut.String())
	}
}

// TestSplitGlobalsDoubleDashJSON 断言 --json 与 -json 等价。
func TestSplitGlobalsDoubleDashJSON(t *testing.T) {
	globals, rest := splitGlobals([]string{"-ca", "ca.pem", "ls", "-q", "关键词", "--json"})
	if strings.Join(globals, "|") != "-ca|ca.pem|--json" {
		t.Fatalf("全局选项提取错: %v", globals)
	}
	if strings.Join(rest, "|") != "ls|-q|关键词" {
		t.Fatalf("其余参数错: %v", rest)
	}
}
