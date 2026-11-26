//go:build windows

package mdns

import (
	"context"
	"errors"
	"net"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

const (
	ipMulticastIF   = 9
	ipMulticastTTL  = 10
	ipMulticastLoop = 11
	ipAddMembership = 12
)

type windowsTransport struct {
	mu     sync.Mutex
	conn   *net.UDPConn
	ifaces []net.Interface
	addrs  []net.IP
	joined map[int]bool
}

func newTransport(port int, _ []net.IP) (transport, error) {
	addr := "0.0.0.0:0"
	if port > 0 {
		addr = "0.0.0.0:" + itoa(port)
	}
	conn, err := listenWindowsUDP(addr)
	if err != nil {
		return nil, err
	}
	t := &windowsTransport{conn: conn, joined: map[int]bool{}}
	if err := t.rescan(); err != nil {
		_ = conn.Close()
		return nil, err
	}
	_ = t.setMulticastOpts()
	return t, nil
}

func listenWindowsUDP(addr string) (*net.UDPConn, error) {
	lc := net.ListenConfig{Control: func(network, address string, c syscall.RawConn) error {
		var serr error
		if err := c.Control(func(fd uintptr) {
			serr = syscall.SetsockoptInt(syscall.Handle(fd), syscall.SOL_SOCKET, syscall.SO_REUSEADDR, 1)
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

func (t *windowsTransport) setMulticastOpts() error {
	raw, err := t.conn.SyscallConn()
	if err != nil {
		return err
	}
	var serr error
	if err := raw.Control(func(fd uintptr) {
		h := syscall.Handle(fd)
		if serr = syscall.SetsockoptInt(h, syscall.IPPROTO_IP, ipMulticastTTL, 255); serr != nil {
			return
		}
		serr = syscall.SetsockoptInt(h, syscall.IPPROTO_IP, ipMulticastLoop, 1)
	}); err != nil {
		return err
	}
	return serr
}

func (t *windowsTransport) rescan() error {
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
		ifa, err := ifi.Addrs()
		if err != nil {
			continue
		}
		hasV4 := false
		for _, a := range ifa {
			ipn, ok := a.(*net.IPNet)
			if !ok {
				continue
			}
			if ipn.IP.To4() != nil {
				hasV4 = true
			}
			addrs = append(addrs, ipn.IP)
		}
		if !hasV4 {
			continue
		}
		usable = append(usable, ifi)
	}
	t.mu.Lock()
	t.ifaces = usable
	t.addrs = addrs
	for _, ifi := range usable {
		if t.joined[ifi.Index] {
			continue
		}
		if t.join(ifi) == nil {
			t.joined[ifi.Index] = true
		}
	}
	t.mu.Unlock()
	return nil
}

func (t *windowsTransport) join(ifi net.Interface) error {
	ip := firstIPv4(ifi)
	if ip == nil {
		return errors.New("mDNS: 网卡没有 IPv4 地址")
	}
	raw, err := t.conn.SyscallConn()
	if err != nil {
		return err
	}
	var serr error
	if err := raw.Control(func(fd uintptr) {
		mreq := &syscall.IPMreq{}
		copy(mreq.Multiaddr[:], []byte{224, 0, 0, 251})
		copy(mreq.Interface[:], ip.To4())
		serr = syscall.SetsockoptIPMreq(syscall.Handle(fd), syscall.IPPROTO_IP, ipAddMembership, mreq)
	}); err != nil {
		return err
	}
	return serr
}

func (t *windowsTransport) recv(b []byte) (int, net.Addr, error) {
	return t.conn.ReadFromUDP(b)
}

func (t *windowsTransport) send(b []byte, addr net.Addr) (int, error) {
	ua, ok := addr.(*net.UDPAddr)
	if !ok {
		return 0, errors.New("mDNS 只支持 UDP 地址")
	}
	if !ua.IP.IsMulticast() {
		return t.conn.WriteToUDP(b, ua)
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	raw, err := t.conn.SyscallConn()
	if err != nil {
		return 0, err
	}
	ifaces := append([]net.Interface(nil), t.ifaces...)
	if len(ifaces) == 0 {
		return t.conn.WriteToUDP(b, ua)
	}
	n := 0
	var lastErr error
	for _, ifi := range ifaces {
		ip := firstIPv4(ifi)
		if ip == nil {
			continue
		}
		var serr error
		if cerr := raw.Control(func(fd uintptr) {
			mif := [4]byte{}
			copy(mif[:], ip.To4())
			serr = syscall.SetsockoptInt(syscall.Handle(fd), syscall.IPPROTO_IP, ipMulticastIF,
				int(*(*int32)(unsafe.Pointer(&mif))))
		}); cerr != nil || serr != nil {
			if cerr != nil {
				lastErr = cerr
			} else {
				lastErr = serr
			}
			continue
		}
		k, werr := t.conn.WriteToUDP(b, ua)
		if werr != nil {
			lastErr = werr
			continue
		}
		n = k
	}
	if n == 0 && lastErr != nil {
		return 0, lastErr
	}
	return n, nil
}

func (t *windowsTransport) setDeadline(d time.Time) error { return t.conn.SetReadDeadline(d) }

func (t *windowsTransport) localAddrs() []net.IP {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]net.IP(nil), t.addrs...)
}

func (t *windowsTransport) close() error {
	t.mu.Lock()
	conn := t.conn
	t.conn = nil
	t.mu.Unlock()
	if conn == nil {
		return nil
	}
	return conn.Close()
}

func firstIPv4(ifi net.Interface) net.IP {
	ifa, err := ifi.Addrs()
	if err != nil {
		return nil
	}
	var fallback net.IP
	for _, a := range ifa {
		ipn, ok := a.(*net.IPNet)
		if !ok {
			continue
		}
		ip4 := ipn.IP.To4()
		if ip4 == nil {
			continue
		}
		if !ipn.IP.IsLinkLocalUnicast() {
			return ip4
		}
		if fallback == nil {
			fallback = ip4
		}
	}
	return fallback
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
