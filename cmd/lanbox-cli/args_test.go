package main

import (
	"bytes"
	"flag"
	"testing"
)

// TestParseArgsInterspersed 断言选项可以写在位置参数之后，且布尔选项不吞值。
func TestParseArgsInterspersed(t *testing.T) {
	fs := flag.NewFlagSet("t", flag.ContinueOnError)
	out := fs.String("o", ".", "")
	force := fs.Bool("force", false, "")
	pos, err := parseArgs(fs, []string{"ID1", "-o", "dir", "--force"})
	if err != nil {
		t.Fatal(err)
	}
	if len(pos) != 1 || pos[0] != "ID1" || *out != "dir" || !*force {
		t.Fatalf("解析不对: %v %q %v", pos, *out, *force)
	}
}

// TestParseArgsDoubleDash 断言 "--" 之后的参数原样作为位置参数。
func TestParseArgsDoubleDash(t *testing.T) {
	fs := flag.NewFlagSet("t", flag.ContinueOnError)
	rec := fs.Bool("r", false, "")
	pos, err := parseArgs(fs, []string{"-r", "--", "-s", "file"})
	if err != nil {
		t.Fatal(err)
	}
	if !*rec || len(pos) != 2 || pos[0] != "-s" || pos[1] != "file" {
		t.Fatalf("-- 之后应原样保留: %v", pos)
	}
}

// TestParseArgsDashValue 断言取值以 - 开头时仍被当作值，而不是新选项。
func TestParseArgsDashValue(t *testing.T) {
	fs := flag.NewFlagSet("t", flag.ContinueOnError)
	fs.String("m", "", "")
	if _, err := parseArgs(fs, []string{"-m", "-s"}); err != nil {
		t.Fatalf("-m 的取值以 - 开头应被接受: %v", err)
	}
}

// TestParseArgsUnknownFlag 断言未知选项报错。
func TestParseArgsUnknownFlag(t *testing.T) {
	fs := flag.NewFlagSet("t", flag.ContinueOnError)
	if _, err := parseArgs(fs, []string{"-nope"}); err == nil {
		t.Fatal("未知选项应报错")
	}
}

// TestParseArgsMissingValue 断言非布尔选项缺值时报错。
func TestParseArgsMissingValue(t *testing.T) {
	fs := flag.NewFlagSet("t", flag.ContinueOnError)
	fs.String("o", "", "")
	if _, err := parseArgs(fs, []string{"-o"}); err == nil {
		t.Fatal("缺值应报错")
	}
}

// TestParseArgsEqualsForm 断言 -name=value / --name=value 按等号拆分。
func TestParseArgsEqualsForm(t *testing.T) {
	fs := flag.NewFlagSet("t", flag.ContinueOnError)
	out := fs.String("o", ".", "")
	rec := fs.Bool("r", false, "")
	pos, err := parseArgs(fs, []string{"--o=/tmp/x", "-r=true", "tail"})
	if err != nil {
		t.Fatal(err)
	}
	if *out != "/tmp/x" || !*rec || len(pos) != 1 || pos[0] != "tail" {
		t.Fatalf("等号写法的解析不对: %q %v %v", *out, *rec, pos)
	}
}

// TestParseArgsSingleDashIsPositional 断言单独的 "-" 是位置参数（标准输入约定）。
func TestParseArgsSingleDashIsPositional(t *testing.T) {
	fs := flag.NewFlagSet("t", flag.ContinueOnError)
	fs.Bool("r", false, "")
	pos, err := parseArgs(fs, []string{"-", "-r"})
	if err != nil {
		t.Fatal(err)
	}
	if len(pos) != 1 || pos[0] != "-" {
		t.Fatalf("- 应作为位置参数保留: %v", pos)
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

// TestRunGlobalsBeforeCommand 断言子命令之前的全局选项也生效。
func TestRunGlobalsBeforeCommand(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("LANBOX_PASSWORD", "x")
	var out, errOut bytes.Buffer
	code := run([]string{"-insecure", "-s", "https://127.0.0.1:1", "login"}, &out, &errOut)
	if code != exitNetwork {
		t.Fatalf("应返回 exitNetwork(%d)，得到 %d：%s", exitNetwork, code, errOut.String())
	}
}

// TestRunUnknownGlobalFlagIsUsage 断言子命令之前的未知选项报用法错误。
func TestRunUnknownGlobalFlagIsUsage(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := run([]string{"-nope", "ls"}, &out, &errOut); code != exitUsage {
		t.Fatalf("未知全局选项应返回 exitUsage(%d)，得到 %d：%s", exitUsage, code, errOut.String())
	}
}
