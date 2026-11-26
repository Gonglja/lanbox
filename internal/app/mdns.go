package app

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/pem"
	"log"
	"os"
	"sync"

	"lanbox/internal/config"
	"lanbox/internal/httpapi"
	"lanbox/internal/mdns"
)

// MDNSRunner 负责通告器的启动、热更与状态适配。
type MDNSRunner struct {
	Log      *log.Logger
	CertPath string

	mu       sync.Mutex
	r        *mdns.Responder
	lastErr  string
	instance string
	host     string
	addrs    []string
}

// Apply 按配置（重新）发布 mDNS 通告。
func (m *MDNSRunner) Apply(cfg config.Config) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !cfg.MDNS.Enabled {
		if m.r != nil {
			_ = m.r.Close()
			m.r = nil
		}
		m.lastErr = ""
		m.instance, m.host, m.addrs = "", "", nil
		return
	}
	rec := mdns.Records{
		Instance: cfg.MDNS.Instance,
		Host:     cfg.MDNS.Hostname,
		Port:     cfg.ListenPort(),
		TXT: map[string]string{
			"v":    "1",
			"tls":  "1",
			"auth": "1",
		},
	}
	if rec.Instance == "" {
		rec.Instance = rec.Host
	}
	rec.TXT["name"] = rec.Instance
	if fp := certFingerprint(m.CertPath); fp != "" {
		rec.TXT["fp"] = fp
	}
	if m.r == nil {
		r, err := mdns.Start(rec)
		if err != nil {
			m.lastErr = err.Error()
			m.Log.Printf("warn: mDNS 未启用：%v", err)
			return
		}
		m.r = r
	} else if err := m.r.Update(rec); err != nil {
		m.lastErr = err.Error()
		m.Log.Printf("warn: mDNS 更新失败：%v", err)
		return
	}
	m.lastErr = ""
	st := m.r.Status()
	m.instance, m.host = st.Instance, st.Host
	m.addrs = nil
	for _, ip := range st.Addrs {
		m.addrs = append(m.addrs, ip.String())
	}
	m.Log.Printf("info: mDNS 已通告 %s.local（实例 %s）", st.Host, st.Instance)
}

// Status 返回适配成 HTTP 层结构的通告状态。
func (m *MDNSRunner) Status(cfg config.Config) httpapi.MDNSStatus {
	m.mu.Lock()
	defer m.mu.Unlock()
	return httpapi.MDNSStatus{
		Enabled:  cfg.MDNS.Enabled,
		Running:  m.r != nil,
		Instance: m.instance,
		Host:     m.host,
		Port:     cfg.ListenPort(),
		Addrs:    append([]string(nil), m.addrs...),
		Error:    m.lastErr,
	}
}

// Close 停止通告。
func (m *MDNSRunner) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.r != nil {
		_ = m.r.Close()
		m.r = nil
	}
}

// certFingerprint 返回叶证书 DER 的 sha256 hex；读不到时返回空串。
func certFingerprint(certPath string) string {
	data, err := os.ReadFile(certPath)
	if err != nil {
		return ""
	}
	block, _ := pem.Decode(data)
	if block == nil || block.Type != "CERTIFICATE" {
		return ""
	}
	sum := sha256.Sum256(block.Bytes)
	return hex.EncodeToString(sum[:])
}
