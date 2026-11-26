package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"lanbox/internal/client"
)

// configDir 返回配置目录（$XDG_CONFIG_HOME/lanbox 或 ~/.config/lanbox）。
func configDir() (string, error) {
	if d := os.Getenv("XDG_CONFIG_HOME"); d != "" {
		return filepath.Join(d, "lanbox"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("无法确定用户主目录: %w", err)
	}
	return filepath.Join(home, ".config", "lanbox"), nil
}

func sessionPath() (string, error) {
	dir, err := configDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "session.json"), nil
}

// saveSession 以 0600 写入会话文件，目录 0700。
func saveSession(s *client.Session) error {
	path, err := sessionPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("创建配置目录失败: %w", err)
	}
	raw, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, append(raw, '\n'), 0o600); err != nil {
		return fmt.Errorf("写入会话失败: %w", err)
	}
	// WriteFile 对已存在文件不改权限，这里强制纠正。
	return os.Chmod(path, 0o600)
}

func loadSession() (*client.Session, error) {
	path, err := sessionPath()
	if err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("尚未登录，请先运行 lanbox-cli login -s <地址>")
		}
		return nil, fmt.Errorf("读取会话失败: %w", err)
	}
	var s client.Session
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, fmt.Errorf("会话文件已损坏，请重新登录: %w", err)
	}
	if s.SID == "" || s.URL == "" {
		return nil, fmt.Errorf("会话文件不完整，请重新登录")
	}
	return &s, nil
}

func removeSession() error {
	path, err := sessionPath()
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("删除会话失败: %w", err)
	}
	return nil
}
