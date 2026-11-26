package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"lanbox/internal/mdns"
)

// browseServices 可被测试替换。
var browseServices = func(timeout time.Duration) ([]mdns.Service, error) {
	return mdns.Browse(context.Background(), timeout)
}

// urlOf 把发现结果转成一个可访问的 URL（不做连通性探测，用于列表展示）。
func urlOf(s mdns.Service) string { return buildURL(pickHost(s.Addrs), s.Port) }

// reachableURL 在多网卡（含 docker0 等虚拟网卡）场景下挑一个真正连得上的地址。
func reachableURL(s mdns.Service) string {
	hosts := candidateHosts(s.Addrs)
	if len(hosts) == 0 || s.Port <= 0 {
		return "-"
	}
	for _, h := range hosts {
		raw := strings.Trim(h, "[]")
		conn, err := net.DialTimeout("tcp", net.JoinHostPort(raw, strconv.Itoa(s.Port)), 300*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return buildURL(h, s.Port)
		}
	}
	return buildURL(hosts[0], s.Port)
}

func buildURL(host string, port int) string {
	if host == "" || port <= 0 {
		return "-"
	}
	return fmt.Sprintf("https://%s:%d", host, port)
}

// candidateHosts 返回可尝试的主机地址：非链路本地 IPv4 优先，其后 IPv6。
func candidateHosts(addrs []net.IP) []string {
	var v4, v6 []string
	for _, ip := range addrs {
		if ip.IsLinkLocalUnicast() {
			continue
		}
		if ip.To4() != nil {
			v4 = append(v4, ip.String())
		} else {
			v6 = append(v6, "["+ip.String()+"]")
		}
	}
	return append(v4, v6...)
}

// pickHost 优先选非链路本地的 IPv4，其次 IPv6。
func pickHost(addrs []net.IP) string {
	hosts := candidateHosts(addrs)
	if len(hosts) == 0 {
		return ""
	}
	return hosts[0]
}

func cmdDiscover(g *globals, args []string) error {
	fs := g.subFS("discover")
	timeout := fs.Duration("timeout", 2*time.Second, "发现超时")
	if _, err := parseArgs(fs, args); err != nil {
		return err
	}
	services, err := browseServices(*timeout)
	if err != nil {
		return fmt.Errorf("局域网发现失败: %w", err)
	}
	if g.json {
		return json.NewEncoder(g.stdout).Encode(services)
	}
	return renderServices(g.stdout, services)
}

// decideServer 在局域网内选定唯一的 lanbox 实例。
func decideServer(timeout time.Duration) (baseURL, fp string, err error) {
	services, err := browseServices(timeout)
	if err != nil {
		return "", "", fmt.Errorf("%w: 局域网发现失败: %v", errUsage, err)
	}
	switch len(services) {
	case 0:
		return "", "", fmt.Errorf("%w: 请用 -s 指定服务地址，例如 -s https://192.168.1.9:8443（局域网内未发现 lanbox）", errUsage)
	case 1:
		s := services[0]
		url := reachableURL(s)
		if url == "-" {
			return "", "", fmt.Errorf("%w: 发现 %s 但地址不完整，请用 -s 指定", errUsage, s.Instance)
		}
		return url, s.TXT["fp"], nil
	default:
		var b strings.Builder
		for _, s := range services {
			fmt.Fprintf(&b, "\n  %s @ %s", s.Instance, urlOf(s))
		}
		return "", "", fmt.Errorf("%w: 发现多个 lanbox 实例，请用 -s 指定：%s", errUsage, b.String())
	}
}
