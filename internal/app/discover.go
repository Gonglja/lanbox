package app

import (
	"context"
	"sort"
	"strconv"
	"strings"
	"time"

	"lanbox/internal/config"
	"lanbox/internal/httpapi"
	"lanbox/internal/mdns"
)

// browseTimeout 是浏览局域网实例的等待时间：一次往返足够，再长界面就该等急了。
const browseTimeout = 1500 * time.Millisecond

// DiscoverServers 把 mDNS 浏览结果适配成 /api/discover 的响应。
// httpapi 不 import internal/mdns，这个适配层放在这里。
func DiscoverServers(m *MDNSRunner, live *config.Live) httpapi.DiscoverFunc {
	return func(ctx context.Context) ([]httpapi.DiscoveredServer, error) {
		svcs, err := mdns.Browse(ctx, browseTimeout)
		if err != nil {
			return nil, err
		}
		self := m.Status(live.Current()).Instance
		out := make([]httpapi.DiscoveredServer, 0, len(svcs))
		for _, svc := range svcs {
			host := strings.TrimSuffix(strings.TrimSuffix(svc.Host, "."), ".local")
			name := svc.TXT["name"]
			if name == "" {
				name = svc.Instance
			}
			suffix := ""
			if svc.Port != 0 && svc.Port != 443 {
				suffix = ":" + strconv.Itoa(svc.Port)
			}
			srv := httpapi.DiscoveredServer{
				Name:        name,
				Host:        host,
				Port:        svc.Port,
				Proto:       svc.TXT["v"],
				Fingerprint: svc.TXT["fp"],
				Current:     self != "" && svc.Instance == self,
			}
			if host != "" {
				srv.URLs = append(srv.URLs, "https://"+host+".local"+suffix)
			}
			for _, ip := range svc.Addrs {
				srv.URLs = append(srv.URLs, "https://"+ip.String()+suffix)
			}
			out = append(out, srv)
		}
		sort.SliceStable(out, func(i, j int) bool {
			if out[i].Current != out[j].Current {
				return out[i].Current // 本机排最前
			}
			return out[i].Name < out[j].Name
		})
		return out, nil
	}
}
