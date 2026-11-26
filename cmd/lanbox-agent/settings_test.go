//go:build windows

package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSettingsDefaultsAndRoundTrip(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	s, err := loadSettings()
	if err != nil {
		t.Fatalf("加载默认配置失败: %v", err)
	}
	if !s.Clipboard.Enabled || !s.Clipboard.Text || !s.Clipboard.Files || s.Clipboard.Images {
		t.Fatalf("默认开关不符合预期: %+v", s.Clipboard)
	}
	if !s.App.CloseToTray || s.App.HistorySize != 50 {
		t.Fatalf("默认应用配置不符合预期: %+v", s.App)
	}

	s.Clipboard.TextPrefix = "📋 PC"
	s.Clipboard.MaxImageMB = 8
	s.Server.URL = "https://lanbox.local"
	s.Server.Username = "admin"
	s.Server.PinFP = "abcd"
	if err := s.save(); err != nil {
		t.Fatalf("保存失败: %v", err)
	}
	got, err := loadSettings()
	if err != nil {
		t.Fatalf("重新加载失败: %v", err)
	}
	if got.Clipboard.TextPrefix != "📋 PC" || got.Clipboard.MaxImageMB != 8 ||
		got.Server.URL != "https://lanbox.local" || got.Server.Username != "admin" || got.Server.PinFP != "abcd" {
		t.Fatalf("配置往返不一致: %+v", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "lanbox", "agent.json.tmp")); !os.IsNotExist(err) {
		t.Fatal("不应残留临时文件")
	}
}

func TestSettingsMissingFileUsesDefaults(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	s, err := loadSettings()
	if err != nil {
		t.Fatalf("缺文件不应报错: %v", err)
	}
	if s.Version != 1 {
		t.Fatalf("版本号错误: %d", s.Version)
	}
}

func TestSettingsClamp(t *testing.T) {
	s := defaultSettings()
	s.App.HistorySize = 9999
	s.Clipboard.DedupeSeconds = -5
	s.Clipboard.MaxTextBytes = 10
	s.Clipboard.MaxFiles = 0
	s.Clipboard.TextPrefix = "  spaced  "
	s.clamp()
	if s.App.HistorySize != 50 || s.Clipboard.DedupeSeconds != 30 || s.Clipboard.MaxTextBytes != 1<<20 || s.Clipboard.MaxFiles != 500 {
		t.Fatalf("越界值未收敛: %+v", s)
	}
	if s.Clipboard.TextPrefix != "spaced" {
		t.Fatalf("前缀未去空白: %q", s.Clipboard.TextPrefix)
	}
}

func TestAgentSettingsMapping(t *testing.T) {
	s := defaultSettings()
	s.Clipboard.Images = true
	s.Clipboard.Paused = true
	s.Clipboard.MaxTotalMB = 100
	as := s.agentSettings()
	if !as.Images || !as.Paused || as.MaxTotalMB != 100 || !as.Enabled {
		t.Fatalf("映射错误: %+v", as)
	}
}

func TestPasswordDPAPIRoundTrip(t *testing.T) {
	var s Settings
	if err := s.setPassword("s3cret-密码"); err != nil {
		t.Fatalf("加密失败: %v", err)
	}
	if s.Server.Password == "" {
		t.Fatal("密文不应为空")
	}
	if got := s.password(); got != "s3cret-密码" {
		t.Fatalf("解密结果不一致: %q", got)
	}
	if err := s.setPassword(""); err != nil {
		t.Fatal(err)
	}
	if s.password() != "" {
		t.Fatal("清空后不应还能解密")
	}
}
