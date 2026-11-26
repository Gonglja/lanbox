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

// saveSession 以 0600 写入会话文件，目录 0700，并通过临时文件 + rename 原子替换。
func saveSession(s *client.Session) error {
	path, err := sessionPath()
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("创建配置目录失败: %w", err)
	}
	// MkdirAll 对已存在的目录不改权限，旧版本可能留下 0755，这里强制收紧。
	if err := os.Chmod(dir, 0o700); err != nil {
		return fmt.Errorf("设置配置目录权限失败: %w", err)
	}
	raw, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')

	// 先写临时文件再 rename：避免写一半崩溃时留下半个会话文件。
	tmp := path + ".tmp"
	if err := writeFileSync(tmp, raw, 0o600); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("写入会话失败: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("保存会话失败: %w", err)
	}
	return nil
}

// writeFileSync 写文件并落盘（0600 由调用方指定）。
func writeFileSync(path string, data []byte, perm os.FileMode) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, perm)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

func loadSession() (*client.Session, error) {
	path, err := sessionPath()
	if err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("%w: 尚未登录，请先运行 lanbox-cli login -s <地址>", errAuth)
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
