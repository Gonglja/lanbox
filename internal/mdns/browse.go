package mdns

import (
	"context"
	"net"
	"sort"
	"strings"
	"time"
)

// Service 是一个被发现的 lanbox 实例。
type Service struct {
	Instance string            `json:"instance"`
	Host     string            `json:"host"`
	Addrs    []net.IP          `json:"addrs"`
	Port     int               `json:"port"`
	TXT      map[string]string `json:"txt"`
}

// Browse 在局域网内查找 lanbox 实例。
func Browse(ctx context.Context, timeout time.Duration) ([]Service, error) {
	t, err := newTransport(0, nil)
	if err != nil {
		return nil, err
	}
	defer t.close()
	return browseWith(ctx, t, timeout)
}

func browseWith(ctx context.Context, t transport, timeout time.Duration) ([]Service, error) {
	deadline := time.Now().Add(timeout)
	st := newBrowseState()
	ptrQuery, err := (&message{Questions: []question{{Name: serviceType, Type: typePTR, Class: classINET}}}).Pack()
	if err != nil {
		return nil, err
	}
	nextQuery := time.Now()
	buf := make([]byte, maxPacket)
	for {
		if ctx.Err() != nil || time.Now().After(deadline) {
			break
		}
		if !time.Now().Before(nextQuery) {
			_, _ = t.send(ptrQuery, multicastIPv4)
			nextQuery = time.Now().Add(time.Second)
		}
		wait := time.Until(deadline)
		if d := time.Until(nextQuery); d < wait {
			wait = d
		}
		if wait <= 0 {
			continue
		}
		_ = t.setDeadline(time.Now().Add(wait))
		n, _, err := t.recv(buf)
		if err != nil {
			continue
		}
		m, err := Parse(buf[:n])
		if err != nil || !m.Response {
			continue
		}
		for _, inst := range st.consume(m) {
			raw, err := (&message{Questions: []question{{
				Name: inst + "." + serviceType, Type: typeANY, Class: classINET,
			}}}).Pack()
			if err == nil {
				_, _ = t.send(raw, multicastIPv4)
			}
		}
	}
	return st.services(), nil
}

type browseState struct {
	instances map[string]*Service
	asked     map[string]bool
	hostAddrs map[string][]net.IP
}

func newBrowseState() *browseState {
	return &browseState{instances: map[string]*Service{}, asked: map[string]bool{}, hostAddrs: map[string][]net.IP{}}
}

// consume 吸收一条响应，返回需要补查的实例名。
func (s *browseState) consume(m *message) []string {
	var pending []string
	for _, rr := range m.Answers {
		name := strings.ToLower(rr.Name)
		switch rr.Type {
		case typePTR:
			if name != serviceType {
				continue
			}
			target, err := parsePTR(rr.Data)
			if err != nil {
				continue
			}
			inst := trimServiceSuffix(target)
			if inst == "" {
				continue
			}
			if s.instances[inst] == nil {
				s.instances[inst] = &Service{Instance: inst, Addrs: []net.IP{}, TXT: map[string]string{}}
			}
			if !s.asked[inst] {
				s.asked[inst] = true
				pending = append(pending, inst)
			}
		case typeSRV:
			inst := trimServiceSuffix(rr.Name)
			if inst == "" {
				continue
			}
			svc := s.instances[inst]
			if svc == nil {
				svc = &Service{Instance: inst, Addrs: []net.IP{}, TXT: map[string]string{}}
				s.instances[inst] = svc
			}
			port, target, err := parseSRV(rr.Data)
			if err != nil {
				continue
			}
			svc.Port = int(port)
			svc.Host = strings.TrimSuffix(strings.ToLower(target), ".local")
		case typeTXT:
			inst := trimServiceSuffix(rr.Name)
			if inst == "" {
				continue
			}
			if svc := s.instances[inst]; svc != nil {
				svc.TXT = parseTXT(rr.Data)
			}
		case typeA, typeAAAA:
			host := strings.TrimSuffix(name, ".local")
			if len(rr.Data) == 4 || len(rr.Data) == 16 {
				s.hostAddrs[host] = appendUniqueIP(s.hostAddrs[host], net.IP(append([]byte(nil), rr.Data...)))
			}
		}
	}
	return pending
}

func (s *browseState) services() []Service {
	out := make([]Service, 0, len(s.instances))
	for _, svc := range s.instances {
		svc.Addrs = append([]net.IP(nil), s.hostAddrs[strings.ToLower(svc.Host)]...)
		sort.Slice(svc.Addrs, func(i, j int) bool { return svc.Addrs[i].String() < svc.Addrs[j].String() })
		if svc.TXT == nil {
			svc.TXT = map[string]string{}
		}
		out = append(out, *svc)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Instance < out[j].Instance })
	return out
}

func trimServiceSuffix(name string) string {
	name = strings.TrimSuffix(strings.ToLower(name), ".")
	if !strings.HasSuffix(name, "."+serviceType) {
		return ""
	}
	return strings.TrimSuffix(name, "."+serviceType)
}

func appendUniqueIP(list []net.IP, ip net.IP) []net.IP {
	for _, have := range list {
		if have.Equal(ip) {
			return list
		}
	}
	return append(list, ip)
}
