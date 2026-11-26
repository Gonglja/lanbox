package mdns

import (
	"context"
	"net"
	"sort"
	"testing"
	"time"
)

func TestBrowseStateConsume(t *testing.T) {
	st := newBrowseState()
	ptr := &message{Response: true, Answers: []record{{
		Name: serviceType, Type: typePTR, Class: classINET, TTL: 120,
		Data: mustPTR("office." + serviceType),
	}}}
	if pending := st.consume(ptr); len(pending) != 1 || pending[0] != "office."+serviceType {
		t.Fatalf("应产生 office 待查询，得到 %+v", pending)
	}
	srv := &message{Response: true, Answers: []record{
		{Name: "office." + serviceType, Type: typeSRV, Class: classINET, TTL: 120, Data: mustSRV(9443, "nas.local")},
		{Name: "office." + serviceType, Type: typeTXT, Class: classINET, TTL: 120, Data: rdataTXT(map[string]string{"v": "1", "fp": "abc123"})},
		{Name: "nas.local", Type: typeA, Class: classINET, TTL: 120, Data: net.IPv4(192, 168, 1, 9).To4()},
	}}
	if pending := st.consume(srv); len(pending) != 1 || pending[0] != "nas.local" {
		t.Fatalf("应补查 SRV 目标主机，得到 %+v", pending)
	}

	services := st.services()
	if len(services) != 1 {
		t.Fatalf("应聚合出 1 个服务，得到 %+v", services)
	}
	s := services[0]
	if s.Instance != "office" || s.Host != "nas" || s.Port != 9443 || s.TXT["fp"] != "abc123" {
		t.Fatalf("服务字段不对: %+v", s)
	}
	if len(s.Addrs) != 1 || s.Addrs[0].String() != "192.168.1.9" {
		t.Fatalf("地址聚合不对: %+v", s.Addrs)
	}
}

func TestBrowseWithFakeTransport(t *testing.T) {
	ft := &fakeTransport{addr: &net.UDPAddr{IP: net.IPv4(192, 168, 1, 9), Port: 5353}}
	resp := &message{Response: true, Answers: []record{
		{Name: serviceType, Type: typePTR, Class: classINET, TTL: 120, Data: mustPTR("office." + serviceType)},
		{Name: "office." + serviceType, Type: typeSRV, Class: classINET, TTL: 120, Data: mustSRV(8443, "lanbox.local")},
		{Name: "lanbox.local", Type: typeAAAA, Class: classINET, TTL: 120, Data: net.ParseIP("fe80::1").To16()},
		{Name: "lanbox.local", Type: typeA, Class: classINET, TTL: 120, Data: net.IPv4(192, 168, 1, 9).To4()},
	}}
	raw, _ := resp.Pack()
	ft.push(raw)

	got, err := browseWith(context.Background(), ft, 60*time.Millisecond)
	if err != nil {
		t.Fatalf("浏览失败: %v", err)
	}
	if len(got) != 1 || got[0].Instance != "office" || got[0].Port != 8443 {
		t.Fatalf("结果不对: %+v", got)
	}
	addrs := make([]string, 0, len(got[0].Addrs))
	for _, ip := range got[0].Addrs {
		addrs = append(addrs, ip.String())
	}
	sort.Strings(addrs)
	if len(addrs) != 2 {
		t.Fatalf("应保留 A 与 AAAA 两个地址: %v", addrs)
	}
}

func TestBrowseTimeoutsWithoutTraffic(t *testing.T) {
	ft := &fakeTransport{addr: &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 5353}}
	got, err := browseWith(context.Background(), ft, 20*time.Millisecond)
	if err != nil || len(got) != 0 {
		t.Fatalf("无响应时应返回空结果，得到 %+v %v", got, err)
	}
}
