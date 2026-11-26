package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"lanbox/internal/updater"
)

// UpdateState 读取服务端缓存的更新状态（不触发联网检查）。
func (c *Client) UpdateState(ctx context.Context) (*updater.State, error) {
	return c.updateReq(ctx, http.MethodGet, "/api/update", nil)
}

// CheckUpdate 让服务端立刻检查更新（仅管理员）。
func (c *Client) CheckUpdate(ctx context.Context) (*updater.State, error) {
	return c.updateReq(ctx, http.MethodPost, "/api/update/check", nil)
}

// ApplyUpdate 请求服务端升级到 version 并重启（仅管理员）。
func (c *Client) ApplyUpdate(ctx context.Context, version string) (*updater.State, error) {
	body, err := json.Marshal(map[string]string{"version": version})
	if err != nil {
		return nil, err
	}
	return c.updateReq(ctx, http.MethodPost, "/api/update/apply", body)
}

// RollbackUpdate 请求服务端回滚到上一版本并重启（仅管理员）。
func (c *Client) RollbackUpdate(ctx context.Context) (*updater.State, error) {
	return c.updateReq(ctx, http.MethodPost, "/api/update/rollback", nil)
}

// updateReq 发起更新相关请求并解析 updater.State。
func (c *Client) updateReq(ctx context.Context, method, path string, body []byte) (*updater.State, error) {
	var r io.Reader
	ctype := ""
	if body != nil {
		r = bytes.NewReader(body)
		ctype = "application/json"
	}
	resp, err := c.do(ctx, method, path, r, ctype)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var st updater.State
	if err := json.NewDecoder(resp.Body).Decode(&st); err != nil {
		return nil, fmt.Errorf("解析更新状态失败: %w", err)
	}
	return &st, nil
}
