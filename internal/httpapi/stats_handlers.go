package httpapi

import (
	"net/http"
	"path/filepath"
	"time"
)

// statsItems 是内容条数分解。
type statsItems struct {
	Total int `json:"total"`
	Todo  int `json:"todo"`
	Taken int `json:"taken"`
}

// statsFiles 是附件汇总。
type statsFiles struct {
	Count int   `json:"count"`
	Bytes int64 `json:"bytes"`
}

// statsDisk 是存储所在文件系统的可用空间；探测失败时只有 error。
type statsDisk struct {
	FreeBytes *int64 `json:"free_bytes,omitempty"`
	Error     string `json:"error,omitempty"`
}

// statsAdmin 是仅管理员可见的字段。
type statsAdmin struct {
	Users    int         `json:"users"`
	Disk     statsDisk   `json:"disk"`
	ItemsDir string      `json:"items_dir"`
	Listen   string      `json:"listen"`
	MDNS     *MDNSStatus `json:"mdns,omitempty"`
}

// statsOutput 是 GET /api/stats 的响应。admin 为 nil 时整个键省略。
type statsOutput struct {
	Version string      `json:"version"`
	UptimeS int64       `json:"uptime_s"`
	Items   statsItems  `json:"items"`
	Files   statsFiles  `json:"files"`
	Admin   *statsAdmin `json:"admin,omitempty"`
}

// handleStats 汇总内容、附件与（管理员）磁盘/用户/mDNS 状态。
func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	st := s.store.Stats()
	out := statsOutput{
		Version: s.version,
		UptimeS: int64(time.Since(s.startedAt).Seconds()),
		Items:   statsItems{Total: st.Items, Todo: st.Todo, Taken: st.Taken},
		Files:   statsFiles{Count: st.Files, Bytes: st.Bytes},
	}
	if u := userFrom(r); u != nil && u.IsAdmin {
		cfg := s.current()
		a := &statsAdmin{
			Users:    len(s.auth.List()),
			ItemsDir: filepath.Join(cfg.DataDir, "items"),
			Listen:   cfg.Listen,
		}
		// 磁盘探测失败只降级该字段，不影响整体统计。
		if free, err := s.store.FreeBytes(); err != nil {
			a.Disk.Error = err.Error()
		} else {
			a.Disk.FreeBytes = &free
		}
		if s.mdnsStatus != nil {
			m := s.mdnsStatus()
			a.MDNS = &m
		}
		out.Admin = a
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, out)
}
