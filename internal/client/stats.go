package client

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

// StatsItems 是内容条数分解。
type StatsItems struct {
	Total int `json:"total"`
	Todo  int `json:"todo"`
	Taken int `json:"taken"`
}

// StatsFiles 是附件汇总。
type StatsFiles struct {
	Count int   `json:"count"`
	Bytes int64 `json:"bytes"`
}

// StatsDisk 是存储所在文件系统的可用空间。
type StatsDisk struct {
	FreeBytes *int64 `json:"free_bytes,omitempty"`
	Error     string `json:"error,omitempty"`
}

// StatsMDNS 是服务端 mDNS 通告状态。
type StatsMDNS struct {
	Enabled  bool     `json:"enabled"`
	Running  bool     `json:"running"`
	Instance string   `json:"instance"`
	Host     string   `json:"host"`
	Port     int      `json:"port"`
	Addrs    []string `json:"addrs"`
	Error    string   `json:"error"`
}

// StatsAdmin 是仅管理员可见的字段；非管理员的响应里没有该块。
type StatsAdmin struct {
	Users    int        `json:"users"`
	Disk     StatsDisk  `json:"disk"`
	ItemsDir string     `json:"items_dir"`
	Listen   string     `json:"listen"`
	MDNS     *StatsMDNS `json:"mdns,omitempty"`
}

// Stats 是 GET /api/stats 的响应。
type Stats struct {
	Version string      `json:"version"`
	UptimeS int64       `json:"uptime_s"`
	Items   StatsItems  `json:"items"`
	Files   StatsFiles  `json:"files"`
	Admin   *StatsAdmin `json:"admin,omitempty"`
}

// Stats 拉取全站统计（需要已登录）。
func (c *Client) Stats(ctx context.Context) (*Stats, error) {
	resp, err := c.do(ctx, http.MethodGet, "/api/stats", nil, "")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var st Stats
	if err := json.NewDecoder(resp.Body).Decode(&st); err != nil {
		return nil, fmt.Errorf("解析统计响应失败: %w", err)
	}
	return &st, nil
}
