package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"lanbox/internal/config"
)

// MDNSStatus 是 mDNS 通告状态（由 main 适配，避免 httpapi 依赖 internal/mdns）。
type MDNSStatus struct {
	Running  bool     `json:"running"`
	Instance string   `json:"instance"`
	Host     string   `json:"host"`
	Port     int      `json:"port"`
	Addrs    []string `json:"addrs"`
	Error    string   `json:"error"`
}

type configValues struct {
	Listen          *string      `json:"listen"`
	DataDir         *string      `json:"data_dir"`
	TLS             *config.TLS  `json:"tls"`
	MaxFileSizeMB   *int64       `json:"max_file_size_mb"`
	MaxItemSizeMB   *int64       `json:"max_item_size_mb"`
	MinFreeDiskMB   *int64       `json:"min_free_disk_mb"`
	MaxFilesPerItem *int         `json:"max_files_per_item"`
	MaxTextBytes    *int         `json:"max_text_bytes"`
	SessionTTLHours *int         `json:"session_ttl_hours"`
	MDNS            *config.MDNS `json:"mdns"`
}

func (v configValues) applyTo(c config.Config) config.Config {
	if v.Listen != nil {
		c.Listen = *v.Listen
	}
	if v.DataDir != nil {
		c.DataDir = *v.DataDir
	}
	if v.TLS != nil {
		c.TLS = *v.TLS
	}
	if v.MaxFileSizeMB != nil {
		c.MaxFileSizeMB = *v.MaxFileSizeMB
	}
	if v.MaxItemSizeMB != nil {
		c.MaxItemSizeMB = *v.MaxItemSizeMB
	}
	if v.MinFreeDiskMB != nil {
		c.MinFreeDiskMB = *v.MinFreeDiskMB
	}
	if v.MaxFilesPerItem != nil {
		c.MaxFilesPerItem = *v.MaxFilesPerItem
	}
	if v.MaxTextBytes != nil {
		c.MaxTextBytes = *v.MaxTextBytes
	}
	if v.SessionTTLHours != nil {
		c.SessionTTLHours = *v.SessionTTLHours
	}
	if v.MDNS != nil {
		c.MDNS = *v.MDNS
	}
	return c
}

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":       true,
		"name":     "lanbox",
		"version":  s.version,
		"uptime_s": int(time.Since(s.startedAt).Seconds()),
	})
}

func (s *Server) handleGetConfig(w http.ResponseWriter, r *http.Request) {
	cur := s.current()
	baseline := cur
	configFile, webConfig := "", ""
	if s.live != nil {
		configFile, webConfig = s.live.Paths()
		baseline = s.live.BaselineConfig()
	}
	resp := map[string]any{
		"values":         cur,
		"baseline":       baseline,
		"origins":        cur.Origins,
		"editable":       config.EditableKeys(),
		"restart_fields": config.RestartFields(),
		"paths":          map[string]string{"config_file": configFile, "web_config": webConfig},
	}
	if s.mdnsStatus != nil {
		resp["mdns"] = s.mdnsStatus()
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handlePutConfig(w http.ResponseWriter, r *http.Request) {
	if s.live == nil {
		writeError(w, http.StatusServiceUnavailable, "config_unavailable", "配置服务未就绪")
		return
	}
	var in struct {
		Values  *configValues `json:"values"`
		Restart bool          `json:"restart"`
	}
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil || in.Values == nil {
		writeError(w, http.StatusBadRequest, "bad_request", "请求体不是合法配置")
		return
	}
	desired := in.Values.applyTo(s.current())
	res, err := s.live.Update(desired)
	if err != nil {
		switch {
		case errors.Is(err, config.ErrInvalidTLS):
			writeError(w, http.StatusBadRequest, "invalid_tls", err.Error())
		case errors.Is(err, config.ErrListenUnavailable):
			writeError(w, http.StatusBadRequest, "listen_unavailable", err.Error())
		default:
			writeError(w, http.StatusBadRequest, "invalid_config", err.Error())
		}
		return
	}
	s.SetConfig(s.live.Current())
	restarting := false
	if in.Restart && len(res.RestartRequired) > 0 {
		restarting = s.live.RequestRestart()
	}
	s.log.Printf("info: %s 更新配置 %v（热更 %v，需重启 %v）", userFrom(r).Username, res.Changed, res.Applied, res.RestartRequired)
	writeJSON(w, http.StatusOK, map[string]any{
		"changed":          res.Changed,
		"applied":          res.Applied,
		"restart_required": res.RestartRequired,
		"restarting":       restarting,
	})
}

func (s *Server) handleRestart(w http.ResponseWriter, r *http.Request) {
	if s.live == nil {
		writeError(w, http.StatusServiceUnavailable, "config_unavailable", "配置服务未就绪")
		return
	}
	if !s.live.RequestRestart() {
		writeError(w, http.StatusTooManyRequests, "restart_cooldown", "重启过于频繁，请稍后再试")
		return
	}
	s.log.Printf("info: %s 请求重启服务", userFrom(r).Username)
	writeJSON(w, http.StatusAccepted, map[string]bool{"restarting": true})
}
