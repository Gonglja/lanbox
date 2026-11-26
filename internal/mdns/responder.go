package mdns

import (
	"context"
	"errors"
	"log"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"
)

const serviceType = "_lanbox._tcp.local"

var multicastIPv4 = &net.UDPAddr{IP: net.IPv4(224, 0, 0, 251), Port: 5353}

// Records 是一条 lanbox 服务的通告内容。
type Records struct {
	Instance string
	Host     string
	Port     int
	TXT      map[string]string
	Addrs    []net.IP
}

// Status 是通告器状态快照。
type Status struct {
	Running  bool
	Instance string
	Host     string
	Addrs    []net.IP
	Err      string
}

type names struct {
	instance string // 不含 ._lanbox._tcp.local
	host     string // 不含 .local
}

func (n names) instanceFQDN() string { return n.instance + "." + serviceType }
func (n names) hostFQDN() string     { return n.host + ".local" }

type responderConfig struct {
	ttl         uint32
	probeGap    time.Duration
	announceGap time.Duration
	rescanEvery time.Duration
	recvTimeout time.Duration
}

func defaultResponderConfig() responderConfig {
	return responderConfig{
		ttl:         120,
		probeGap:    250 * time.Millisecond,
		announceGap: time.Second,
		rescanEvery: 30 * time.Second,
		recvTimeout: 500 * time.Millisecond,
	}
}

// transport 是报文通道抽象，测试用假实现。
type transport interface {
	recv(b []byte) (int, net.Addr, error)
	send(b []byte, addr net.Addr) (int, error)
	setDeadline(t time.Time) error
	localAddrs() []net.IP
	rescan() error
	close() error
}

// Responder 是一个 mDNS 通告器。
type Responder struct {
	newTransport func(port int, addrs []net.IP) (transport, error)
	cfg          responderConfig
	logger       *log.Logger

	mu     sync.Mutex
	t      transport
	cancel context.CancelFunc
	names  names
	rec    Records
	status Status
	wg     sync.WaitGroup
}

// Start 创建并启动通告器；5353 不可用时返回 error（调用方降级）。
func Start(rec Records) (*Responder, error) {
	r := &Responder{newTransport: newTransport, cfg: defaultResponderConfig(), logger: log.Default()}
	if err := r.start(rec); err != nil {
		return nil, err
	}
	return r, nil
}

func (r *Responder) start(rec Records) error {
	if rec.Host == "" {
		return errors.New("mDNS 主机名不能为空")
	}
	if rec.Instance == "" {
		rec.Instance = rec.Host
	}
	t, err := r.newTransport(5353, rec.Addrs)
	if err != nil {
		return err
	}
	n, err := r.probe(t, rec)
	if err != nil {
		_ = t.close()
		return err
	}

	ctx, cancel := context.WithCancel(context.Background())
	r.mu.Lock()
	r.t = t
	r.cancel = cancel
	r.names = n
	r.rec = rec
	r.status = Status{Running: true, Instance: n.instance, Host: n.host, Addrs: t.localAddrs()}
	r.mu.Unlock()

	r.wg.Add(3)
	go r.readLoop(ctx, t)
	go r.beatLoop(ctx, t)
	go func() { // 第二次播报（间隔 announceGap）
		defer r.wg.Done()
		select {
		case <-ctx.Done():
		case <-time.After(r.cfg.announceGap):
			r.announce(t)
		}
	}()
	r.announce(t)
	return nil
}

// Update 用新的记录重启通告（停旧 → 重新探测 → 播报）。
func (r *Responder) Update(rec Records) error {
	r.stop()
	return r.start(rec)
}

// Status 返回当前状态。
func (r *Responder) Status() Status {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.status
}

// Close 发送 goodbye 并关闭。
func (r *Responder) Close() error {
	r.stop()
	return nil
}

func (r *Responder) stop() {
	r.mu.Lock()
	t, cancel := r.t, r.cancel
	r.t, r.cancel = nil, nil
	r.status.Running = false
	r.mu.Unlock()
	if t == nil {
		return
	}
	if cancel != nil {
		cancel()
	}
	if raw, err := r.bye(t).Pack(); err == nil {
		_, _ = t.send(raw, multicastIPv4)
	}
	r.wg.Wait()
	_ = t.close()
}

func (r *Responder) probe(t transport, rec Records) (names, error) {
	host := rec.Host
	instance := rec.Instance
	for attempt := 0; attempt < 5; attempt++ {
		n := names{instance: instance, host: host}
		conflict := r.probeOnce(t, n, rec)
		if !conflict {
			return n, nil
		}
		host = rec.Host + "-" + strconv.Itoa(attempt+2)
		if rec.Instance == rec.Host {
			instance = host
		}
	}
	return names{}, errors.New("mDNS 名称冲突：找不到可用的主机名")
}

func (r *Responder) probeOnce(t transport, n names, rec Records) bool {
	addrs := t.localAddrs()
	auth := buildRecords(n, rec, addrs, 0)
	var qs []question
	for _, rr := range auth {
		qs = append(qs, question{Name: rr.Name, Type: typeANY, Class: classINET})
	}
	raw, err := (&message{Questions: qs, Authorities: auth}).Pack()
	if err != nil {
		return false
	}
	for i := 0; i < 3; i++ {
		if _, err := t.send(raw, multicastIPv4); err != nil {
			return false
		}
		deadline := time.Now().Add(r.cfg.probeGap)
		for time.Now().Before(deadline) {
			_ = t.setDeadline(deadline)
			buf := make([]byte, maxPacket)
			k, _, err := t.recv(buf)
			if err != nil {
				break
			}
			if r.conflicts(buf[:k], n, rec, addrs) {
				return true
			}
		}
	}
	return false
}

func (r *Responder) conflicts(raw []byte, n names, rec Records, addrs []net.IP) bool {
	m, err := Parse(raw)
	if err != nil || !m.Response {
		return false
	}
	want := map[string]bool{}
	for _, rr := range buildRecords(n, rec, addrs, 0) {
		want[recordKey(rr)] = true
	}
	for _, rr := range m.Answers {
		if !strings.EqualFold(rr.Name, n.hostFQDN()) && !strings.EqualFold(rr.Name, n.instanceFQDN()) {
			continue
		}
		if rr.Type != typeA && rr.Type != typeAAAA && rr.Type != typeSRV && rr.Type != typeTXT {
			continue
		}
		if !want[recordKey(rr)] {
			return true
		}
	}
	return false
}

func (r *Responder) readLoop(ctx context.Context, t transport) {
	defer r.wg.Done()
	buf := make([]byte, maxPacket)
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		_ = t.setDeadline(time.Now().Add(r.cfg.recvTimeout))
		k, addr, err := t.recv(buf)
		if err != nil {
			continue
		}
		r.handle(t, buf[:k], addr)
	}
}

func (r *Responder) handle(t transport, raw []byte, addr net.Addr) {
	m, err := Parse(raw)
	if err != nil || m.Response || len(m.Questions) == 0 {
		return
	}
	ans := r.answer(m)
	if ans == nil {
		return
	}
	unicast := isLegacy(addr) || hasUnicastQuestion(m.Questions)
	if unicast {
		ans.ID = m.ID
	}
	out, err := ans.Pack()
	if err != nil {
		return
	}
	dest := net.Addr(multicastIPv4)
	if unicast {
		dest = addr
	}
	_, _ = t.send(out, dest)
}

func isLegacy(addr net.Addr) bool {
	ua, ok := addr.(*net.UDPAddr)
	return !ok || ua.Port != 5353
}

func hasUnicastQuestion(qs []question) bool {
	for _, q := range qs {
		if q.unicast() {
			return true
		}
	}
	return false
}

func (r *Responder) answer(m *message) *message {
	r.mu.Lock()
	n, rec := r.names, r.rec
	t := r.t
	r.mu.Unlock()
	var addrs []net.IP
	if t != nil {
		addrs = t.localAddrs()
	}
	all := buildRecords(n, rec, addrs, r.cfg.ttl)
	known := map[string]bool{}
	for _, rr := range m.Answers {
		if rr.TTL >= r.cfg.ttl/2 {
			known[recordKey(rr)] = true
		}
	}
	var out []record
	for _, q := range m.Questions {
		for _, rr := range all {
			if !strings.EqualFold(rr.Name, q.Name) {
				continue
			}
			if q.Type != typeANY && rr.Type != q.Type {
				continue
			}
			if known[recordKey(rr)] {
				continue
			}
			out = append(out, rr)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return &message{Response: true, Authoritative: true, Answers: out}
}

func (r *Responder) announce(t transport) {
	r.mu.Lock()
	n, rec := r.names, r.rec
	r.mu.Unlock()
	msg := &message{Response: true, Authoritative: true, Answers: buildRecords(n, rec, t.localAddrs(), r.cfg.ttl)}
	raw, err := msg.Pack()
	if err != nil {
		return
	}
	_, _ = t.send(raw, multicastIPv4)
}

func (r *Responder) bye(t transport) *message {
	r.mu.Lock()
	n, rec := r.names, r.rec
	r.mu.Unlock()
	return &message{Response: true, Authoritative: true, Answers: buildRecords(n, rec, t.localAddrs(), 0)}
}

func (r *Responder) beatLoop(ctx context.Context, t transport) {
	defer r.wg.Done()
	announceTick := time.NewTicker(time.Duration(r.cfg.ttl/2) * time.Second)
	defer announceTick.Stop()
	rescanTick := time.NewTicker(r.cfg.rescanEvery)
	defer rescanTick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-announceTick.C:
			r.announce(t)
		case <-rescanTick.C:
			if err := t.rescan(); err == nil {
				r.announce(t)
			}
		}
	}
}

// buildRecords 是纯函数，便于测试。
func buildRecords(n names, rec Records, addrs []net.IP, ttl uint32) []record {
	out := []record{
		{Name: serviceType, Type: typePTR, Class: classINET, TTL: ttl, Data: mustPTR(n.instanceFQDN())},
		{Name: n.instanceFQDN(), Type: typeSRV, Class: classINET | cacheFlushFlag, TTL: ttl, Data: mustSRV(uint16(rec.Port), n.hostFQDN())},
		{Name: n.instanceFQDN(), Type: typeTXT, Class: classINET | cacheFlushFlag, TTL: ttl, Data: rdataTXT(rec.TXT)},
		{Name: "_services._dns-sd._udp.local", Type: typePTR, Class: classINET, TTL: ttl, Data: mustPTR(serviceType)},
	}
	for _, ip := range addrs {
		if v4 := ip.To4(); v4 != nil {
			out = append(out, record{Name: n.hostFQDN(), Type: typeA, Class: classINET | cacheFlushFlag, TTL: ttl, Data: append([]byte(nil), v4...)})
			continue
		}
		if v6 := ip.To16(); v6 != nil {
			out = append(out, record{Name: n.hostFQDN(), Type: typeAAAA, Class: classINET | cacheFlushFlag, TTL: ttl, Data: append([]byte(nil), v6...)})
		}
	}
	return out
}
