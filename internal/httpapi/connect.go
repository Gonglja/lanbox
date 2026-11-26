package httpapi

import (
	"context"
	"net"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"lanbox/internal/qr"
)

// ConnectURL 是一条可以连到本服务的地址。
type ConnectURL struct {
	URL   string `json:"url"`
	Label string `json:"label"`
	Kind  string `json:"kind"` // mdns / request / interface
}

// ConnectInfo 描述「怎么连到这台服务器」，供网页生成二维码、客户端列出候选。
type ConnectInfo struct {
	Name      string       `json:"name"`
	Version   string       `json:"version"`
	URLs      []ConnectURL `json:"urls"`
	Preferred string       `json:"preferred"`
}

const (
	kindMDNS      = "mdns"
	kindRequest   = "request"
	kindInterface = "interface"
)

// connectInfo 汇总可用地址，按「手机最可能连得上」的顺序排列：
// mDNS 名 → 当前访问地址（非回环）→ 各网卡地址 → 当前访问地址（回环，仅作提示）。
//
// 客户端扫码后直接拿这些地址去填输入框；不需要任何额外权限。
func (s *Server) connectInfo(r *http.Request) ConnectInfo {
	cfg := s.current()
	port := cfg.ListenPort()
	suffix := ""
	if port != 0 && port != 443 {
		suffix = ":" + strconv.Itoa(port)
	}

	info := ConnectInfo{Name: "lanbox", Version: s.version}
	switch {
	case cfg.MDNS.Instance != "":
		info.Name = cfg.MDNS.Instance
	case cfg.MDNS.Hostname != "":
		info.Name = cfg.MDNS.Hostname
	}

	seen := map[string]bool{}
	add := func(url, label, kind string) {
		if url == "" || seen[url] {
			return
		}
		seen[url] = true
		info.URLs = append(info.URLs, ConnectURL{URL: url, Label: label, Kind: kind})
	}

	if cfg.MDNS.Enabled && cfg.MDNS.Hostname != "" {
		add("https://"+cfg.MDNS.Hostname+".local"+suffix, "局域网名称", kindMDNS)
	}

	if url := requestURL(r); url != "" && !isLoopbackHost(strings.TrimPrefix(url, "https://")) {
		add(url, "当前访问地址", kindRequest)
	}

	if s.ifaceAddrs != nil {
		for _, raw := range s.ifaceAddrs() {
			ip := net.ParseIP(strings.TrimSpace(raw))
			if ip == nil || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
				continue
			}
			host := ip.String()
			if ip.To4() == nil {
				host = "[" + host + "]"
			}
			add("https://"+host+suffix, "本机地址", kindInterface)
		}
	}
	if s.ifaceAddrs != nil {
		for _, raw := range s.ifaceAddrs() {
			ip := net.ParseIP(strings.TrimSpace(raw))
			if ip == nil || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
				continue
			}
			host := ip.String()
			if ip.To4() == nil {
				host = "[" + host + "]"
			}
			add("https://"+host+suffix, "本机地址", kindInterface)
		}
	}
	for _, iface := range interfaceURLs(suffix) {
		add(iface[1], iface[0], kindInterface)
	}

	// 回环地址放最后：手机连不上，但能提示「你是用本机地址打开的」
	if url := requestURL(r); url != "" {
		add(url, "当前访问地址（仅本机）", kindRequest)
	}

	if len(info.URLs) > 0 {
		info.Preferred = info.URLs[0].URL
	}
	return info
}

// requestURL 用请求的 Host 还原出外部访问地址（对外一律按 HTTPS 处理）。
func requestURL(r *http.Request) string {
	host := strings.TrimSpace(r.Host)
	if host == "" {
		return ""
	}
	return "https://" + host
}

// isLoopbackHost 判断 Host（可带端口）是否指向本机。
func isLoopbackHost(host string) bool {
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.Trim(host, "[]")
	if strings.EqualFold(host, "localhost") {
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}
	return false
}

// interfaceURLs 返回各网卡上的可用地址（[标签, URL]），跳过回环与链路本地地址。
func interfaceURLs(suffix string) [][2]string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	var out [][2]string
	for _, ifi := range ifaces {
		if ifi.Flags&net.FlagUp == 0 || ifi.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := ifi.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			ipnet, ok := a.(*net.IPNet)
			if !ok {
				continue
			}
			ip := ipnet.IP
			if ip.IsLoopback() || ip.IsLinkLocalUnicast() || !ip.IsGlobalUnicast() {
				continue
			}
			host := ip.String()
			if ip.To4() == nil {
				host = "[" + host + "]"
			}
			out = append(out, [2]string{"网卡 " + ifi.Name, "https://" + host + suffix})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i][1] < out[j][1] })
	return out
}

// handleConnect 返回本服务可用的连接地址。
func (s *Server) handleConnect(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.connectInfo(r))
}

// handleConnectQR 把某个连接地址画成二维码（SVG）。
//
// 只接受 /api/connect 里出现过的地址，避免被当成任意内容的二维码生成器。
func (s *Server) handleConnectQR(w http.ResponseWriter, r *http.Request) {
	info := s.connectInfo(r)
	target := info.Preferred
	if want := r.URL.Query().Get("url"); want != "" {
		target = ""
		for _, u := range info.URLs {
			if u.URL == want {
				target = want
				break
			}
		}
		if target == "" {
			writeError(w, http.StatusBadRequest, "bad_url", "该地址不在可用列表里")
			return
		}
	}
	if target == "" {
		writeError(w, http.StatusNotFound, "no_url", "没有可用的连接地址（请检查监听地址配置）")
		return
	}
	code, err := qr.Encode(target)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "qr_failed", "二维码生成失败")
		return
	}
	h := w.Header()
	h.Set("Content-Type", "image/svg+xml; charset=utf-8")
	h.Set("Cache-Control", "no-store")
	// SVG 是文档类型：即使只由服务端生成，也按不可执行文档沙箱化
	h.Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; sandbox")
	_, _ = w.Write([]byte(code.SVG(4)))
}

// ---------- 局域网发现 ----------

// DiscoveredServer 是 /api/discover 返回的一个局域网实例。
type DiscoveredServer struct {
	Name        string   `json:"name"`
	Host        string   `json:"host"`
	Port        int      `json:"port"`
	URLs        []string `json:"urls"`
	Proto       string   `json:"proto,omitempty"`
	Fingerprint string   `json:"fingerprint,omitempty"`
	Current     bool     `json:"current,omitempty"`
}

// DiscoverFunc 由 main 注入（内部用 internal/mdns 浏览），避免 httpapi 依赖 mdns。
type DiscoverFunc func(ctx context.Context) ([]DiscoveredServer, error)

const discoverCacheTTL = 3 * time.Second

// handleDiscover 列出局域网内的 lanbox 实例。
//
// 浏览要发组播并等回复（约 1.5 秒），所以结果缓存 3 秒，
// 连续点按界面按钮不会每次都扫一遍。
func (s *Server) handleDiscover(w http.ResponseWriter, r *http.Request) {
	if s.discover == nil {
		writeError(w, http.StatusNotImplemented, "discover_unavailable", "本服务未启用局域网发现")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	s.discoverMu.Lock()
	cached, at := s.discoverCache, s.discoverAt
	s.discoverMu.Unlock()
	if cached != nil && time.Since(at) < discoverCacheTTL {
		writeJSON(w, http.StatusOK, map[string]any{"servers": cached})
		return
	}

	servers, err := s.discover(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "discover_failed", "局域网发现失败："+err.Error())
		return
	}
	if servers == nil {
		servers = []DiscoveredServer{}
	}
	s.discoverMu.Lock()
	s.discoverCache, s.discoverAt = servers, time.Now()
	s.discoverMu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"servers": servers})
}

// discoverCacheState 供测试观察缓存是否生效。
func (s *Server) discoverCacheState() (int, time.Time) {
	s.discoverMu.Lock()
	defer s.discoverMu.Unlock()
	return len(s.discoverCache), s.discoverAt
}
