package config

import (
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"
)

// Result 描述一次配置更新的影响面。
type Result struct {
	Changed         []string `json:"changed"`
	Applied         []string `json:"applied"`
	RestartRequired []string `json:"restart_required"`
}

var (
	// ErrInvalidTLS 表示新的证书/私钥无法加载。
	ErrInvalidTLS = errors.New("证书无法加载")
	// ErrListenUnavailable 表示新的监听地址绑定失败。
	ErrListenUnavailable = errors.New("监听地址不可用")
)

// restartFields 是修改后必须重启才能生效的字段。
var restartFields = []string{"listen", "data_dir", "tls.cert_file", "tls.key_file"}

// RestartFields 返回必须重启才能生效的字段名。
func RestartFields() []string { return append([]string(nil), restartFields...) }

func needsRestart(key string) bool {
	for _, k := range restartFields {
		if k == key {
			return true
		}
	}
	return false
}

// Hooks 是 Live 对进程其它部件的回调。
type Hooks struct {
	Apply   func(prev, next Config) error // 热更
	Restart func()                        // 请求自重启（非阻塞）
}

// Live 持有当前生效配置，负责落盘与回调。
type Live struct {
	cfgPath string
	webPath string
	base    Config
	hooks   Hooks

	mu          sync.RWMutex
	cur         Config
	lastRestart time.Time
	cooldown    time.Duration
	now         func() time.Time
}

// OpenLive 载入配置并接管后续变更。
func OpenLive(cfgPath string, hooks Hooks) (*Live, error) {
	cur, err := Load(cfgPath)
	if err != nil {
		return nil, err
	}
	base, err := Baseline(cfgPath)
	if err != nil {
		return nil, err
	}
	return &Live{cfgPath: cfgPath, webPath: WebConfigPath(base), base: base, hooks: hooks,
		cur: cur, cooldown: 10 * time.Second, now: time.Now}, nil
}

// Current 返回当前配置快照。
func (l *Live) Current() Config {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.cur
}

// BaselineConfig 返回"不含网页覆盖"的基线配置（供网页显示"恢复默认"的基准值）。
func (l *Live) BaselineConfig() Config {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.base
}

// Paths 返回配置文件与覆盖文件路径。
func (l *Live) Paths() (configFile, webConfig string) { return l.cfgPath, l.webPath }

// Update 校验 desired（完整配置）→ 落盘覆盖 → 热更并返回影响面。
func (l *Live) Update(desired Config) (Result, error) {
	var res Result
	desired.Origins = nil
	if err := desired.Validate(); err != nil {
		return res, err
	}

	prev := l.Current()
	if desired.TLS != prev.TLS {
		if _, err := tls.LoadX509KeyPair(desired.TLS.CertFile, desired.TLS.KeyFile); err != nil {
			return res, fmt.Errorf("%w: %v", ErrInvalidTLS, err)
		}
	}
	if desired.Listen != prev.Listen {
		ln, err := net.Listen("tcp", desired.Listen)
		if err != nil {
			return res, fmt.Errorf("%w: %v", ErrListenUnavailable, err)
		}
		_ = ln.Close()
	}

	base, err := Baseline(l.cfgPath)
	if err != nil {
		return res, err
	}
	ov := diff(desired, base)
	if err := saveOverrides(l.webPath, ov); err != nil {
		return res, err
	}

	origins := make(map[string]string, len(base.Origins))
	for k, v := range base.Origins {
		origins[k] = v
	}
	ov.apply(&desired, origins)
	desired.Origins = origins

	res.Changed = changedKeys(prev, desired)
	for _, k := range res.Changed {
		if needsRestart(k) {
			res.RestartRequired = append(res.RestartRequired, k)
		} else {
			res.Applied = append(res.Applied, k)
		}
	}

	l.mu.Lock()
	l.cur = desired
	l.base = base
	l.mu.Unlock()

	if len(res.Applied) > 0 && l.hooks.Apply != nil {
		if err := l.hooks.Apply(prev, desired); err != nil {
			return res, err
		}
	}
	return res, nil
}

// DropWebOverride 清除单个字段的网页覆盖（启动自愈与"恢复默认"用）。
func (l *Live) DropWebOverride(key string) (Result, error) {
	var res Result
	prev := l.Current()
	ov, err := loadOverrides(l.webPath)
	if err != nil {
		return res, err
	}
	if !clearOverride(&ov, key) {
		return res, nil
	}
	if err := saveOverrides(l.webPath, ov); err != nil {
		return res, err
	}
	next, err := Load(l.cfgPath)
	if err != nil {
		return res, err
	}
	res.Changed = changedKeys(prev, next)
	for _, k := range res.Changed {
		if needsRestart(k) {
			res.RestartRequired = append(res.RestartRequired, k)
		} else {
			res.Applied = append(res.Applied, k)
		}
	}
	base, err := Baseline(l.cfgPath)
	if err != nil {
		return res, err
	}
	l.mu.Lock()
	l.cur = next
	l.base = base
	l.mu.Unlock()
	if len(res.Applied) > 0 && l.hooks.Apply != nil {
		if err := l.hooks.Apply(prev, next); err != nil {
			return res, err
		}
	}
	return res, nil
}

// RequestRestart 触发一次自重启，冷却期内返回 false。
func (l *Live) RequestRestart() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	if !l.lastRestart.IsZero() && now.Sub(l.lastRestart) < l.cooldown {
		return false
	}
	l.lastRestart = now
	if l.hooks.Restart != nil {
		l.hooks.Restart()
	}
	return true
}

func clearOverride(o *Overrides, key string) bool {
	switch key {
	case "listen":
		if o.Listen != nil {
			o.Listen = nil
			return true
		}
	case "data_dir":
		if o.DataDir != nil {
			o.DataDir = nil
			return true
		}
	case "tls.cert_file", "tls.key_file":
		if o.TLS != nil {
			o.TLS = nil
			return true
		}
	case "max_file_size_mb":
		if o.MaxFileSizeMB != nil {
			o.MaxFileSizeMB = nil
			return true
		}
	case "max_item_size_mb":
		if o.MaxItemSizeMB != nil {
			o.MaxItemSizeMB = nil
			return true
		}
	case "min_free_disk_mb":
		if o.MinFreeDiskMB != nil {
			o.MinFreeDiskMB = nil
			return true
		}
	case "max_files_per_item":
		if o.MaxFilesPerItem != nil {
			o.MaxFilesPerItem = nil
			return true
		}
	case "max_text_bytes":
		if o.MaxTextBytes != nil {
			o.MaxTextBytes = nil
			return true
		}
	case "session_ttl_hours":
		if o.SessionTTLHours != nil {
			o.SessionTTLHours = nil
			return true
		}
	case "mdns.enabled", "mdns.hostname", "mdns.instance":
		if o.MDNS != nil {
			o.MDNS = nil
			return true
		}
	}
	return false
}

func changedKeys(prev, next Config) []string {
	var out []string
	add := func(k string, changed bool) {
		if changed {
			out = append(out, k)
		}
	}
	add("listen", prev.Listen != next.Listen)
	add("data_dir", prev.DataDir != next.DataDir)
	add("tls.cert_file", prev.TLS.CertFile != next.TLS.CertFile)
	add("tls.key_file", prev.TLS.KeyFile != next.TLS.KeyFile)
	add("max_file_size_mb", prev.MaxFileSizeMB != next.MaxFileSizeMB)
	add("max_item_size_mb", prev.MaxItemSizeMB != next.MaxItemSizeMB)
	add("min_free_disk_mb", prev.MinFreeDiskMB != next.MinFreeDiskMB)
	add("max_files_per_item", prev.MaxFilesPerItem != next.MaxFilesPerItem)
	add("max_text_bytes", prev.MaxTextBytes != next.MaxTextBytes)
	add("session_ttl_hours", prev.SessionTTLHours != next.SessionTTLHours)
	add("mdns.enabled", prev.MDNS.Enabled != next.MDNS.Enabled)
	add("mdns.hostname", prev.MDNS.Hostname != next.MDNS.Hostname)
	add("mdns.instance", prev.MDNS.Instance != next.MDNS.Instance)
	return out
}
