package main

import (
	"fmt"

	"lanbox/internal/client"
	"lanbox/internal/session"
)

// sessionPath 返回会话文件路径（实现见 internal/session，与 GUI 客户端共用）。
func sessionPath() (string, error) { return session.Path() }

// saveSession 保存会话（0600 + 原子替换，见 internal/session）。
func saveSession(s *client.Session) error { return session.Save(s) }

// loadSessionOptional 读取会话；没有会话文件时返回 (nil, nil)。
func loadSessionOptional() (*client.Session, error) { return session.Load() }

// loadSession 读取会话；没有会话时返回 errAuth 并以"尚未登录"提示。
func loadSession() (*client.Session, error) {
	s, err := loadSessionOptional()
	if err != nil {
		return nil, err
	}
	if s == nil {
		return nil, fmt.Errorf("%w: 尚未登录，请先运行 lanbox-cli login -s <地址>", errAuth)
	}
	return s, nil
}

// removeSession 删除本地会话文件。
func removeSession() error { return session.Remove() }
