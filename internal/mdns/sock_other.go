//go:build !linux && !windows

package mdns

import (
	"net"
	"time"
)

type udpTransport struct {
	conn *net.UDPConn
}

// newTransport 在非 Linux 上用标准库的组播绑定（仅默认网卡，供开发机使用）。
func newTransport(port int, addrs []net.IP) (transport, error) {
	if port == 0 {
		conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero, Port: 0})
		if err != nil {
			return nil, err
		}
		return &udpTransport{conn: conn}, nil
	}
	conn, err := net.ListenMulticastUDP("udp4", nil, &net.UDPAddr{IP: net.IPv4(224, 0, 0, 251), Port: port})
	if err != nil {
		return nil, err
	}
	return &udpTransport{conn: conn}, nil
}

func (t *udpTransport) recv(b []byte) (int, net.Addr, error) { return t.conn.ReadFromUDP(b) }

func (t *udpTransport) send(b []byte, addr net.Addr) (int, error) {
	ua, ok := addr.(*net.UDPAddr)
	if !ok {
		return 0, nil
	}
	return t.conn.WriteToUDP(b, ua)
}

func (t *udpTransport) setDeadline(d time.Time) error { return t.conn.SetReadDeadline(d) }
func (t *udpTransport) rescan() error                 { return nil }

func (t *udpTransport) localAddrs() []net.IP {
	ifa, err := net.InterfaceAddrs()
	if err != nil {
		return nil
	}
	var out []net.IP
	for _, a := range ifa {
		if ipn, ok := a.(*net.IPNet); ok {
			out = append(out, ipn.IP)
		}
	}
	return out
}

func (t *udpTransport) close() error { return t.conn.Close() }
