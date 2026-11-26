//go:build linux

package mdns

import (
	"context"
	"errors"
	"net"
	"sync"
	"syscall"
	"time"
)

// soReusePort 是 Linux 的 SO_REUSEPORT（syscall 包未导出）。
const soReusePort = 0x0f

type linuxTransport struct {
	mu     sync.Mutex
	conn   *net.UDPConn
	ifaces []net.Interface
	addrs  []net.IP
	joined map[int]bool
}

// newTransport 绑定 port（0 表示随机端口），并加入所有合格网卡的组播组。
// 优先用 REUSEADDR+REUSEPORT；若与只设 REUSEADDR 的进程（如 avahi）冲突，则回落为只设 REUSEADDR。
func newTransport(port int, addrs []net.IP) (transport, error) {
	addr := "0.0.0.0:0"
	if port > 0 {
		addr = "0.0.0.0:" + itoa(port)
	}
	pc, err := listenUDP(addr, true)
	if err != nil {
		pc, err = listenUDP(addr, false)
	}
	if err != nil {
		return nil, err
	}
	t := &linuxTransport{conn: pc, joined: map[int]bool{}}
	if err := t.rescan(); err != nil {
		_ = t.conn.Close()
		return nil, err
	}
	if port > 0 {
		_ = setMulticastOpts(t.conn)
	}
	return t, nil
}

func listenUDP(addr string, reusePort bool) (*net.UDPConn, error) {
	lc := net.ListenConfig{Control: func(network, address string, c syscall.RawConn) error {
		var serr error
		if err := c.Control(func(fd uintptr) {
			if serr = syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_REUSEADDR, 1); serr != nil {
				return
			}
			if reusePort {
				serr = syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, soReusePort, 1)
			}
		}); err != nil {
			return err
		}
		return serr
	}}
	pc, err := lc.ListenPacket(context.Background(), "udp4", addr)
	if err != nil {
		return nil, err
	}
	return pc.(*net.UDPConn), nil
}

func setMulticastOpts(conn *net.UDPConn) error {
	raw, err := conn.SyscallConn()
	if err != nil {
		return err
	}
	var serr error
	if err := raw.Control(func(fd uintptr) {
		if serr = syscall.SetsockoptInt(int(fd), syscall.IPPROTO_IP, syscall.IP_MULTICAST_TTL, 255); serr != nil {
			return
		}
		serr = syscall.SetsockoptInt(int(fd), syscall.IPPROTO_IP, syscall.IP_MULTICAST_LOOP, 1)
	}); err != nil {
		return err
	}
	return serr
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [6]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

func (t *linuxTransport) rescan() error {
	ifaces, err := net.Interfaces()
	if err != nil {
		return err
	}
	var usable []net.Interface
	var addrs []net.IP
	for _, ifi := range ifaces {
		if ifi.Flags&net.FlagUp == 0 || ifi.Flags&net.FlagMulticast == 0 || ifi.Flags&net.FlagLoopback != 0 {
			continue
		}
		usable = append(usable, ifi)
		ifa, err := ifi.Addrs()
		if err != nil {
			continue
		}
		for _, a := range ifa {
			if ipn, ok := a.(*net.IPNet); ok && ipn.IP.To16() != nil {
				addrs = append(addrs, ipn.IP)
			}
		}
	}
	t.mu.Lock()
	t.ifaces = usable
	t.addrs = addrs
	for _, ifi := range usable {
		if t.joined[ifi.Index] {
			continue
		}
		if t.join(ifi.Index) == nil {
			t.joined[ifi.Index] = true
		}
	}
	t.mu.Unlock()
	return nil
}

func (t *linuxTransport) join(idx int) error {
	raw, err := t.conn.SyscallConn()
	if err != nil {
		return err
	}
	var serr error
	if err := raw.Control(func(fd uintptr) {
		mreq := &syscall.IPMreqn{Ifindex: int32(idx)}
		copy(mreq.Multiaddr[:], []byte{224, 0, 0, 251})
		serr = syscall.SetsockoptIPMreqn(int(fd), syscall.IPPROTO_IP, syscall.IP_ADD_MEMBERSHIP, mreq)
	}); err != nil {
		return err
	}
	return serr
}

func (t *linuxTransport) recv(b []byte) (int, net.Addr, error) {
	return t.conn.ReadFromUDP(b)
}

func (t *linuxTransport) send(b []byte, addr net.Addr) (int, error) {
	ua, ok := addr.(*net.UDPAddr)
	if !ok {
		return 0, errors.New("mDNS 只支持 UDP 地址")
	}
	if !ua.IP.IsMulticast() {
		return t.conn.WriteToUDP(b, ua)
	}
	t.mu.Lock()
	ifaces := append([]net.Interface(nil), t.ifaces...)
	t.mu.Unlock()
	if len(ifaces) == 0 {
		return t.conn.WriteToUDP(b, ua)
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	n := 0
	for _, ifi := range ifaces {
		if err := t.setMulticastIfaceLocked(ifi.Index); err != nil {
			continue
		}
		if k, err := t.conn.WriteToUDP(b, ua); err == nil {
			n = k
		}
	}
	return n, nil
}

// setMulticastIfaceLocked 把后续组播发送固定到指定网卡（Linux ip_mreqn）。
func (t *linuxTransport) setMulticastIfaceLocked(idx int) error {
	raw, err := t.conn.SyscallConn()
	if err != nil {
		return err
	}
	var serr error
	if err := raw.Control(func(fd uintptr) {
		mreq := &syscall.IPMreqn{Ifindex: int32(idx)}
		serr = syscall.SetsockoptIPMreqn(int(fd), syscall.IPPROTO_IP, syscall.IP_MULTICAST_IF, mreq)
	}); err != nil {
		return err
	}
	return serr
}

func (t *linuxTransport) setDeadline(d time.Time) error { return t.conn.SetReadDeadline(d) }

func (t *linuxTransport) localAddrs() []net.IP {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]net.IP(nil), t.addrs...)
}

func (t *linuxTransport) close() error {
	t.mu.Lock()
	conn := t.conn
	t.conn = nil
	t.mu.Unlock()
	if conn == nil {
		return nil
	}
	return conn.Close()
}
