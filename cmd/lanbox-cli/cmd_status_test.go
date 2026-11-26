package main

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"lanbox/internal/client"
)

// TestCmdStatusAdmin 断言管理员看到全部 7 行与真实计数。
func TestCmdStatusAdmin(t *testing.T) {
	e := newCmdEnv(t)
	e.putItem(t, uploadOf("a.txt", "abcd"))

	if err := cmdStatus(context.Background(), e.g, nil); err != nil {
		t.Fatalf("status 失败: %v", err)
	}
	out := e.out.String()
	for _, want := range []string{
		"服务   " + e.g.server, "可达", "身份   admin（管理员）",
		"内容   1 条（未取走 1 / 已取走 0）", "附件   1 个 / 4 B",
		"磁盘   剩余 ", "用户   1 个 · 监听 ", "存储 ",
		"mDNS   未启用",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("输出缺少 %q：\n%s", want, out)
		}
	}
}

// TestCmdStatusNonAdmin 断言普通用户没有磁盘/用户/mDNS 三行。
func TestCmdStatusNonAdmin(t *testing.T) {
	e := newCmdEnv(t)
	if err := e.am.CreateUser("bob", "bobpassword", false); err != nil {
		t.Fatalf("创建普通用户失败: %v", err)
	}
	e.loginAs(t, "bob", "bobpassword")

	if err := cmdStatus(context.Background(), e.g, nil); err != nil {
		t.Fatalf("status 失败: %v", err)
	}
	out := e.out.String()
	if !strings.Contains(out, "身份   bob（普通用户）") {
		t.Fatalf("身份行不对：\n%s", out)
	}
	if !strings.Contains(out, "内容   ") || !strings.Contains(out, "附件   ") {
		t.Fatalf("公共统计缺失：\n%s", out)
	}
	for _, banned := range []string{"磁盘", "用户   ", "mDNS", "存储 "} {
		if strings.Contains(out, banned) {
			t.Fatalf("普通用户不应看到 %q：\n%s", banned, out)
		}
	}
}

// TestCmdStatusJSON 断言 -json 输出服务端结构 + 顶层 url。
func TestCmdStatusJSON(t *testing.T) {
	e := newCmdEnv(t)
	e.putItem(t, uploadOf("a.txt", "abc"))
	e.g.json = true

	if err := cmdStatus(context.Background(), e.g, nil); err != nil {
		t.Fatalf("status --json 失败: %v", err)
	}
	var got struct {
		URL     string `json:"url"`
		Version string `json:"version"`
		UptimeS int64  `json:"uptime_s"`
		Items   struct {
			Total int `json:"total"`
			Todo  int `json:"todo"`
			Taken int `json:"taken"`
		} `json:"items"`
		Files struct {
			Count int   `json:"count"`
			Bytes int64 `json:"bytes"`
		} `json:"files"`
		Admin *struct {
			Users    int    `json:"users"`
			ItemsDir string `json:"items_dir"`
			Disk     struct {
				FreeBytes *int64 `json:"free_bytes"`
				Error     string `json:"error"`
			} `json:"disk"`
		} `json:"admin"`
	}
	if err := json.Unmarshal(e.out.Bytes(), &got); err != nil {
		t.Fatalf("输出不是合法 JSON: %v：%s", err, e.out.String())
	}
	if got.URL != e.g.server {
		t.Fatalf("url 应为 %q，得到 %q", e.g.server, got.URL)
	}
	if got.Version == "" {
		t.Fatal("version 不应为空")
	}
	if got.Items.Total != 1 || got.Items.Todo != 1 || got.Items.Taken != 0 {
		t.Fatalf("内容统计不对: %+v", got.Items)
	}
	if got.Files.Count != 1 || got.Files.Bytes != 3 {
		t.Fatalf("附件统计不对: %+v", got.Files)
	}
	if got.Admin == nil {
		t.Fatal("管理员应有 admin 块")
	}
	if got.Admin.Users != 1 || got.Admin.ItemsDir == "" {
		t.Fatalf("admin 块不对: %+v", got.Admin)
	}
	if got.Admin.Disk.FreeBytes == nil && got.Admin.Disk.Error == "" {
		t.Fatalf("磁盘字段缺失: %+v", got.Admin.Disk)
	}
}

// TestCmdStatusRequiresLogin 断言未登录走认证退出码路径。
func TestCmdStatusRequiresLogin(t *testing.T) {
	e := newCmdEnv(t)
	if err := removeSession(); err != nil {
		t.Fatalf("删除会话失败: %v", err)
	}
	e.g.server = ""
	t.Setenv("LANBOX_URL", "")
	err := cmdStatus(context.Background(), e.g, nil)
	if !errors.Is(err, errAuth) {
		t.Fatalf("未登录应返回 errAuth，得到 %v", err)
	}
}

// TestCmdStatusRejectsArgs 断言 status 不接受位置参数。
func TestCmdStatusRejectsArgs(t *testing.T) {
	e := newCmdEnv(t)
	if err := cmdStatus(context.Background(), e.g, []string{"extra"}); !errors.Is(err, errUsage) {
		t.Fatalf("多余参数应返回 errUsage，得到 %v", err)
	}
}

// TestFmtUptime 覆盖各档位。
func TestFmtUptime(t *testing.T) {
	cases := map[int64]string{
		0:      "0s",
		59:     "59s",
		60:     "1m",
		3599:   "59m",
		3600:   "1h",
		3660:   "1h1m",
		86400:  "1d",
		90000:  "1d1h",
		259200: "3d",
		-5:     "0s",
	}
	for in, want := range cases {
		if got := fmtUptime(in); got != want {
			t.Fatalf("fmtUptime(%d) = %q, want %q", in, got, want)
		}
	}
}

// TestMdnsLine 覆盖未启用/未运行/运行中三种形态。
func TestMdnsLine(t *testing.T) {
	if got := mdnsLine(nil); got != "未启用" {
		t.Fatalf("nil 应为未启用，得到 %q", got)
	}
	if got := mdnsLine(&client.StatsMDNS{Enabled: false}); got != "未启用（配置关闭）" {
		t.Fatalf("配置关闭应说明，得到 %q", got)
	}
	if got := mdnsLine(&client.StatsMDNS{Enabled: true, Running: false, Error: "绑定失败"}); got != "未运行：绑定失败" {
		t.Fatalf("未运行应带原因，得到 %q", got)
	}
	if got := mdnsLine(&client.StatsMDNS{Enabled: true, Running: false}); got != "未运行" {
		t.Fatalf("未运行应简洁，得到 %q", got)
	}
	if got := mdnsLine(&client.StatsMDNS{Enabled: true, Running: true, Host: "lanbox.local", Port: 8443}); got != "lanbox.local:8443（已运行）" {
		t.Fatalf("运行中应带主机名与端口，得到 %q", got)
	}
	if got := mdnsLine(&client.StatsMDNS{Enabled: true, Running: true}); got != "已运行" {
		t.Fatalf("没有主机名时只报已运行，得到 %q", got)
	}
}
