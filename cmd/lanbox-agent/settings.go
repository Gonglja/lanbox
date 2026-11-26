//go:build windows

package main

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"lanbox/internal/agent"
	"lanbox/internal/session"
	"lanbox/internal/winclip"
)

// UIClipSettings 是界面可见的剪贴板配置。
type UIClipSettings struct {
	Enabled       bool   `json:"enabled"`
	Paused        bool   `json:"paused"`
	Text          bool   `json:"text"`
	Files         bool   `json:"files"`
	Images        bool   `json:"images"`
	DedupeSeconds int    `json:"dedupe_seconds"`
	MaxTextBytes  int    `json:"max_text_bytes"`
	MaxImageMB    int    `json:"max_image_mb"`
	MaxTotalMB    int    `json:"max_total_mb"`
	MaxFiles      int    `json:"max_files"`
	TextPrefix    string `json:"text_prefix"`
}

// UIAppSettings 是界面可见的应用配置。
type UIAppSettings struct {
	Autostart        bool `json:"autostart"`
	CloseToTray      bool `json:"close_to_tray"`
	HistorySize      int  `json:"history_size"`
	RememberPassword bool `json:"remember_password"`
}

// ServerSettings 是当前服务器与账号（密码只存 DPAPI 密文）。
type ServerSettings struct {
	URL      string `json:"url"`
	Username string `json:"username"`
	PinFP    string `json:"pin_fp"`
	Password string `json:"password"`
}

// Settings 是 agent.json 的结构。
type Settings struct {
	Version   int            `json:"version"`
	Clipboard UIClipSettings `json:"clipboard"`
	App       UIAppSettings  `json:"app"`
	Server    ServerSettings `json:"server"`
}

// defaultSettings 返回带默认值的配置。
func defaultSettings() Settings {
	return Settings{
		Version: 1,
		Clipboard: UIClipSettings{
			Enabled:       true,
			Text:          true,
			Files:         true,
			Images:        false,
			DedupeSeconds: 30,
			MaxTextBytes:  1 << 20,
			MaxImageMB:    5,
			MaxTotalMB:    2048,
			MaxFiles:      500,
		},
		App: UIAppSettings{
			CloseToTray:      true,
			HistorySize:      50,
			RememberPassword: true,
		},
	}
}

// settingsPath 返回 %AppData%\lanbox\agent.json。
func settingsPath() (string, error) {
	dir, err := session.Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "agent.json"), nil
}

// loadSettings 读取配置；文件不存在时返回默认值。
// 反序列化到默认值上，缺失字段保留默认，未知字段忽略。
func loadSettings() (Settings, error) {
	s := defaultSettings()
	path, err := settingsPath()
	if err != nil {
		return s, err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return s, nil
		}
		return s, fmt.Errorf("读取配置失败: %w", err)
	}
	if err := json.Unmarshal(raw, &s); err != nil {
		return defaultSettings(), fmt.Errorf("配置文件已损坏，已使用默认值: %w", err)
	}
	s.clamp()
	return s, nil
}

// save 原子写入配置文件（0600）。
func (s Settings) save() error {
	s.clamp()
	path, err := settingsPath()
	if err != nil {
		return err
	}
	raw, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("创建配置目录失败: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return fmt.Errorf("写入配置失败: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("保存配置失败: %w", err)
	}
	return nil
}

// clamp 把越界值收敛到安全范围。
func (s *Settings) clamp() {
	if s.App.HistorySize < 5 || s.App.HistorySize > 200 {
		s.App.HistorySize = 50
	}
	if s.Clipboard.DedupeSeconds < 0 || s.Clipboard.DedupeSeconds > 3600 {
		s.Clipboard.DedupeSeconds = 30
	}
	if s.Clipboard.MaxTextBytes < 1024 {
		s.Clipboard.MaxTextBytes = 1 << 20
	}
	if s.Clipboard.MaxImageMB < 1 || s.Clipboard.MaxImageMB > 512 {
		s.Clipboard.MaxImageMB = 5
	}
	if s.Clipboard.MaxTotalMB < 1 {
		s.Clipboard.MaxTotalMB = 2048
	}
	if s.Clipboard.MaxFiles < 1 || s.Clipboard.MaxFiles > 10000 {
		s.Clipboard.MaxFiles = 500
	}
	s.Clipboard.TextPrefix = strings.TrimSpace(s.Clipboard.TextPrefix)
	if len(s.Clipboard.TextPrefix) > 200 {
		s.Clipboard.TextPrefix = s.Clipboard.TextPrefix[:200]
	}
}

// agentSettings 转换成平台无关的决策配置。
func (s Settings) agentSettings() agent.Settings {
	return agent.Settings{
		Enabled:       s.Clipboard.Enabled,
		Paused:        s.Clipboard.Paused,
		Text:          s.Clipboard.Text,
		Files:         s.Clipboard.Files,
		Images:        s.Clipboard.Images,
		DedupeSeconds: s.Clipboard.DedupeSeconds,
		MaxTextBytes:  s.Clipboard.MaxTextBytes,
		MaxImageMB:    s.Clipboard.MaxImageMB,
		MaxTotalMB:    s.Clipboard.MaxTotalMB,
		MaxFiles:      s.Clipboard.MaxFiles,
		TextPrefix:    s.Clipboard.TextPrefix,
	}
}

// setPassword 用 DPAPI 加密密码并保存密文；空密码表示清除。
func (s *Settings) setPassword(plain string) error {
	if plain == "" {
		s.Server.Password = ""
		return nil
	}
	enc, err := winclip.Protect([]byte(plain))
	if err != nil {
		return fmt.Errorf("加密密码失败: %w", err)
	}
	s.Server.Password = base64.StdEncoding.EncodeToString(enc)
	return nil
}

// password 解密保存的密码；没有密码或解密失败时返回空串。
func (s Settings) password() string {
	if s.Server.Password == "" {
		return ""
	}
	raw, err := base64.StdEncoding.DecodeString(s.Server.Password)
	if err != nil {
		return ""
	}
	plain, err := winclip.Unprotect(raw)
	if err != nil {
		return ""
	}
	return string(plain)
}
