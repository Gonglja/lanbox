package mdns

import (
	"encoding/binary"
	"net"
	"reflect"
	"strings"
	"testing"
)

func TestMessageRoundTrip(t *testing.T) {
	m := &message{
		Response:      true,
		Authoritative: true,
		Answers: []record{
			{Name: "_lanbox._tcp.local", Type: typePTR, Class: classINET, TTL: 120, Data: mustPTR("lanbox._lanbox._tcp.local")},
			{Name: "lanbox._lanbox._tcp.local", Type: typeSRV, Class: classINET | cacheFlushFlag, TTL: 120, Data: mustSRV(8443, "lanbox.local")},
			{Name: "lanbox._lanbox._tcp.local", Type: typeTXT, Class: classINET | cacheFlushFlag, TTL: 120, Data: rdataTXT(map[string]string{"v": "1", "fp": "abc"})},
			{Name: "lanbox.local", Type: typeA, Class: classINET | cacheFlushFlag, TTL: 120, Data: net.IPv4(192, 168, 1, 9).To4()},
		},
	}
	raw, err := m.Pack()
	if err != nil {
		t.Fatalf("打包失败: %v", err)
	}
	got, err := Parse(raw)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if !got.Response || !got.Authoritative || len(got.Answers) != len(m.Answers) {
		t.Fatalf("头部/记录数不对: %+v", got)
	}
	for i := range m.Answers {
		if got.Answers[i].Name != m.Answers[i].Name || got.Answers[i].Type != m.Answers[i].Type || got.Answers[i].TTL != m.Answers[i].TTL {
			t.Fatalf("第 %d 条记录不一致: %+v vs %+v", i, got.Answers[i], m.Answers[i])
		}
	}
	if name, err := parsePTR(got.Answers[0].Data); err != nil || name != "lanbox._lanbox._tcp.local" {
		t.Fatalf("PTR 解析不对: %q %v", name, err)
	}
	port, target, err := parseSRV(got.Answers[1].Data)
	if err != nil || port != 8443 || target != "lanbox.local" {
		t.Fatalf("SRV 解析不对: %d %q %v", port, target, err)
	}
	txt := parseTXT(got.Answers[2].Data)
	if !reflect.DeepEqual(txt, map[string]string{"v": "1", "fp": "abc"}) {
		t.Fatalf("TXT 解析不对: %+v", txt)
	}
}

func TestParseCompressedName(t *testing.T) {
	// 手工构造：header(qd=1,an=1) + 问题 "a.local" + 名字用指针指回问题名
	hdr := make([]byte, 12)
	binary.BigEndian.PutUint16(hdr[2:4], 0x8400)
	binary.BigEndian.PutUint16(hdr[4:6], 1)
	binary.BigEndian.PutUint16(hdr[6:8], 1)
	raw := append([]byte{}, hdr...)

	qnameOff := len(raw)
	raw, _ = packName(raw, "a.local")
	raw = append(raw, 0, 12, 0, 1) // PTR IN

	// answer：名字指针指回问题名，PTR，TTL 120，rdata 是 "b.local"
	raw = append(raw, 0xC0, byte(qnameOff), 0, 12, 0, 1, 0, 0, 0, 120)
	target, err := packName(nil, "b.local")
	if err != nil {
		t.Fatal(err)
	}
	raw = append(raw, 0, byte(len(target)))
	raw = append(raw, target...)

	m, err := Parse(raw)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if len(m.Questions) != 1 || m.Questions[0].Name != "a.local" {
		t.Fatalf("问题解析不对: %+v", m.Questions)
	}
	if len(m.Answers) != 1 || m.Answers[0].Name != "a.local" {
		t.Fatalf("压缩指针名字解析不对: %+v", m.Answers)
	}
	if name, err := parsePTR(m.Answers[0].Data); err != nil || name != "b.local" {
		t.Fatalf("压缩 rdata 名字未展开: %q %v", name, err)
	}
}

func TestParseRejectsBadPackets(t *testing.T) {
	cases := [][]byte{
		{},
		{0, 0},
		{0, 1, 0, 0, 0, 1, 0, 0, 0, 0, 0, 0}, // 问题数 1 但没有问题
		[]byte(strings.Repeat("\xff", 64)),   // 非法名字长度
	}
	for _, raw := range cases {
		if _, err := Parse(raw); err == nil {
			t.Fatalf("坏包应报错: %v", raw)
		}
	}
}

func TestQuestionUnicastFlag(t *testing.T) {
	q := question{Name: "x", Type: typePTR, Class: classINET | classUnicastFlag}
	if !q.unicast() || q.qclass() != classINET {
		t.Fatalf("QU 位解析不对: %+v", q)
	}
}
