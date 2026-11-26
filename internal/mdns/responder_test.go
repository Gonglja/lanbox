package mdns

import (
	"io"
	"log"
	"net"
	"os"
	"sync"
	"testing"
	"time"
)

// fakeTransport 是测试用的报文通道。
type fakeTransport struct {
	mu       sync.Mutex
	addr     *net.UDPAddr
	inbox    [][]byte
	sent     [][]byte
	deadline time.Time
}

func (f *fakeTransport) push(raw []byte) {
	f.mu.Lock()
	f.inbox = append(f.inbox, raw)
	f.mu.Unlock()
}

func (f *fakeTransport) recv(b []byte) (int, net.Addr, error) {
	f.mu.Lock()
	if len(f.inbox) > 0 {
		raw := f.inbox[0]
		f.inbox = f.inbox[1:]
		f.mu.Unlock()
		return copy(b, raw), f.addr, nil
	}
	dl := f.deadline
	f.mu.Unlock()
	if !dl.IsZero() {
		if d := time.Until(dl); d > 0 {
			time.Sleep(d)
		}
	} else {
		time.Sleep(time.Millisecond)
	}
	return 0, nil, os.ErrDeadlineExceeded
}

func (f *fakeTransport) send(b []byte, addr net.Addr) (int, error) {
	f.mu.Lock()
	f.sent = append(f.sent, append([]byte(nil), b...))
	f.mu.Unlock()
	return len(b), nil
}

func (f *fakeTransport) setDeadline(t time.Time) error {
	f.mu.Lock()
	f.deadline = t
	f.mu.Unlock()
	return nil
}

func (f *fakeTransport) localAddrs() []net.IP { return []net.IP{net.IPv4(192, 168, 1, 9)} }
func (f *fakeTransport) rescan() error        { return nil }
func (f *fakeTransport) close() error         { return nil }

func (f *fakeTransport) sentMessages(t *testing.T) []*message {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []*message
	for _, raw := range f.sent {
		m, err := Parse(raw)
		if err != nil {
			t.Fatalf("发出的报文无法解析: %v", err)
		}
		out = append(out, m)
	}
	return out
}

func newTestResponder(t *testing.T, ft *fakeTransport) *Responder {
	t.Helper()
	return &Responder{
		newTransport: func(int, []net.IP) (transport, error) { return ft, nil },
		cfg:          responderConfig{ttl: 120, probeGap: 2 * time.Millisecond, announceGap: time.Millisecond, rescanEvery: time.Hour, recvTimeout: 5 * time.Millisecond},
		logger:       log.New(io.Discard, "", 0),
	}
}

func testRecords() Records {
	return Records{
		Instance: "lanbox",
		Host:     "lanbox",
		Port:     8443,
		TXT:      map[string]string{"v": "1", "fp": "deadbeef"},
	}
}

func TestResponderAnnouncesAndAnswersPTR(t *testing.T) {
	ft := &fakeTransport{addr: &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 5353}}
	r := newTestResponder(t, ft)
	if err := r.start(testRecords()); err != nil {
		t.Fatalf("启动失败: %v", err)
	}
	defer r.Close()

	if st := r.Status(); !st.Running || st.Host != "lanbox" || st.Instance != "lanbox" {
		t.Fatalf("状态不对: %+v", st)
	}

	time.Sleep(20 * time.Millisecond)
	if len(ft.sentMessages(t)) == 0 {
		t.Fatal("应至少发出一次通告")
	}

	q := &message{Questions: []question{{Name: serviceType, Type: typePTR, Class: classINET}}}
	raw, err := q.Pack()
	if err != nil {
		t.Fatal(err)
	}
	ft.push(raw)
	time.Sleep(60 * time.Millisecond)

	found := false
	for _, m := range ft.sentMessages(t) {
		if !m.Response {
			continue
		}
		if len(m.Answers) >= 4 {
			found = true
		}
	}
	if !found {
		t.Fatal("PTR 查询应得到包含 4 条以上记录的应答")
	}
}

func TestResponderLegacyUnicast(t *testing.T) {
	ft := &fakeTransport{addr: &net.UDPAddr{IP: net.IPv4(10, 1, 2, 3), Port: 40000}}
	r := newTestResponder(t, ft)
	if err := r.start(testRecords()); err != nil {
		t.Fatal(err)
	}
	defer r.Close()

	q := &message{ID: 0x1234, Questions: []question{{Name: serviceType, Type: typePTR, Class: classINET}}}
	raw, _ := q.Pack()
	ft.push(raw)
	time.Sleep(60 * time.Millisecond)

	found := false
	for _, m := range ft.sentMessages(t) {
		if m.Response && m.ID == 0x1234 {
			found = true
		}
	}
	if !found {
		t.Fatal("单播应答应回填查询 ID 0x1234")
	}
}

func TestResponderConflictRenamesHost(t *testing.T) {
	ft := &fakeTransport{addr: &net.UDPAddr{IP: net.IPv4(10, 9, 9, 9), Port: 5353}}
	conflict := &message{Response: true, Answers: []record{{
		Name: "lanbox.local", Type: typeA, Class: classINET, TTL: 120,
		Data: net.IPv4(10, 9, 9, 9).To4(),
	}}}
	raw, _ := conflict.Pack()
	for i := 0; i < 5; i++ {
		ft.push(raw)
	}

	r := newTestResponder(t, ft)
	if err := r.start(testRecords()); err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if host := r.Status().Host; host != "lanbox-2" {
		t.Fatalf("冲突后应改名为 lanbox-2，得到 %q", host)
	}
}

func TestResponderCloseSendsGoodbye(t *testing.T) {
	ft := &fakeTransport{addr: &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 5353}}
	r := newTestResponder(t, ft)
	if err := r.start(testRecords()); err != nil {
		t.Fatal(err)
	}
	r.Close()
	sawZeroTTL := false
	for _, m := range ft.sentMessages(t) {
		for _, rr := range m.Answers {
			if rr.TTL == 0 {
				sawZeroTTL = true
			}
		}
	}
	if !sawZeroTTL {
		t.Fatal("关闭时应发 TTL=0 的 goodbye")
	}
}
