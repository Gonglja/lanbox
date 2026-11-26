// Package mdns 实现 lanbox 需要的 mDNS 通告与浏览（仅标准库）。
package mdns

import (
	"encoding/binary"
	"errors"
	"sort"
	"strconv"
	"strings"
)

const (
	typeA    uint16 = 1
	typePTR  uint16 = 12
	typeTXT  uint16 = 16
	typeAAAA uint16 = 28
	typeSRV  uint16 = 33
	typeANY  uint16 = 255

	classINET        uint16 = 1
	classUnicastFlag uint16 = 0x8000
	cacheFlushFlag   uint16 = 0x8000
)

const (
	maxNameLen = 255
	maxPacket  = 9000
	maxJumps   = 16
)

var errMalformed = errors.New("mDNS 报文格式错误")

type question struct {
	Name  string
	Type  uint16
	Class uint16 // 含 QU 位
}

func (q question) unicast() bool  { return q.Class&classUnicastFlag != 0 }
func (q question) qclass() uint16 { return q.Class &^ classUnicastFlag }

type record struct {
	Name  string
	Type  uint16
	Class uint16 // 含 cache-flush 位
	TTL   uint32
	Data  []byte // 自包含 rdata（PTR/SRV 里的名字已解压并重新编码为不压缩形式）
}

type message struct {
	ID            uint16
	Response      bool
	Authoritative bool
	Questions     []question
	Answers       []record
	Authorities   []record
	Additionals   []record
}

// Parse 解析 mDNS 报文。
func Parse(raw []byte) (*message, error) {
	if len(raw) < 12 || len(raw) > maxPacket {
		return nil, errMalformed
	}
	flags := binary.BigEndian.Uint16(raw[2:4])
	m := &message{ID: binary.BigEndian.Uint16(raw[0:2]), Response: flags&0x8000 != 0, Authoritative: flags&0x0400 != 0}
	qd := int(binary.BigEndian.Uint16(raw[4:6]))
	an := int(binary.BigEndian.Uint16(raw[6:8]))
	ns := int(binary.BigEndian.Uint16(raw[8:10]))
	ar := int(binary.BigEndian.Uint16(raw[10:12]))
	off := 12
	for i := 0; i < qd; i++ {
		name, next, err := unpackName(raw, off)
		if err != nil {
			return nil, err
		}
		if next+4 > len(raw) {
			return nil, errMalformed
		}
		m.Questions = append(m.Questions, question{
			Name:  name,
			Type:  binary.BigEndian.Uint16(raw[next : next+2]),
			Class: binary.BigEndian.Uint16(raw[next+2 : next+4]),
		})
		off = next + 4
	}
	for _, section := range []struct {
		dst *[]record
		n   int
	}{
		{&m.Answers, an},
		{&m.Authorities, ns},
		{&m.Additionals, ar},
	} {
		for i := 0; i < section.n; i++ {
			rr, next, err := parseRecord(raw, off)
			if err != nil {
				return nil, err
			}
			*section.dst = append(*section.dst, rr)
			off = next
		}
	}
	return m, nil
}

func parseRecord(msg []byte, off int) (record, int, error) {
	name, next, err := unpackName(msg, off)
	if err != nil {
		return record{}, 0, err
	}
	if next+10 > len(msg) {
		return record{}, 0, errMalformed
	}
	rr := record{
		Name:  name,
		Type:  binary.BigEndian.Uint16(msg[next : next+2]),
		Class: binary.BigEndian.Uint16(msg[next+2 : next+4]),
		TTL:   binary.BigEndian.Uint32(msg[next+4 : next+8]),
	}
	rdlen := int(binary.BigEndian.Uint16(msg[next+8 : next+10]))
	rdoff := next + 10
	if rdoff+rdlen > len(msg) {
		return record{}, 0, errMalformed
	}
	data := append([]byte(nil), msg[rdoff:rdoff+rdlen]...)
	switch rr.Type {
	case typePTR:
		target, _, err := unpackName(msg, rdoff)
		if err != nil {
			return record{}, 0, err
		}
		data = mustPTR(target)
	case typeSRV:
		if rdlen < 6 {
			return record{}, 0, errMalformed
		}
		target, _, err := unpackName(msg, rdoff+6)
		if err != nil {
			return record{}, 0, err
		}
		data = append(data[:6], mustPTR(target)...)
	}
	rr.Data = data
	return rr, rdoff + rdlen, nil
}

// Pack 编码 mDNS 报文（名字不压缩）。
func (m *message) Pack() ([]byte, error) {
	if m == nil {
		return nil, errMalformed
	}
	raw := make([]byte, 12, 512)
	binary.BigEndian.PutUint16(raw[0:2], m.ID)
	var flags uint16
	if m.Response {
		flags |= 0x8000
	}
	if m.Authoritative {
		flags |= 0x0400
	}
	binary.BigEndian.PutUint16(raw[2:4], flags)
	binary.BigEndian.PutUint16(raw[4:6], uint16(len(m.Questions)))
	binary.BigEndian.PutUint16(raw[6:8], uint16(len(m.Answers)))
	binary.BigEndian.PutUint16(raw[8:10], uint16(len(m.Authorities)))
	binary.BigEndian.PutUint16(raw[10:12], uint16(len(m.Additionals)))
	var err error
	for _, q := range m.Questions {
		if raw, err = packName(raw, q.Name); err != nil {
			return nil, err
		}
		raw = binary.BigEndian.AppendUint16(raw, q.Type)
		raw = binary.BigEndian.AppendUint16(raw, q.Class)
	}
	for _, list := range [][]record{m.Answers, m.Authorities, m.Additionals} {
		for _, rr := range list {
			if raw, err = packName(raw, rr.Name); err != nil {
				return nil, err
			}
			raw = binary.BigEndian.AppendUint16(raw, rr.Type)
			raw = binary.BigEndian.AppendUint16(raw, rr.Class)
			raw = binary.BigEndian.AppendUint32(raw, rr.TTL)
			raw = binary.BigEndian.AppendUint16(raw, uint16(len(rr.Data)))
			raw = append(raw, rr.Data...)
		}
	}
	if len(raw) > maxPacket {
		return nil, errMalformed
	}
	return raw, nil
}

// packName 把点分名字编码为不压缩的 label 序列。
func packName(dst []byte, name string) ([]byte, error) {
	name = strings.TrimSuffix(strings.TrimSpace(name), ".")
	if name == "" {
		return append(dst, 0), nil
	}
	if len(name) > maxNameLen {
		return nil, errMalformed
	}
	for _, label := range strings.Split(name, ".") {
		if label == "" || len(label) > 63 {
			return nil, errMalformed
		}
		dst = append(dst, byte(len(label)))
		dst = append(dst, label...)
	}
	return append(dst, 0), nil
}

// packLabels 返回 name 编码后的 label 序列（含长度前缀、不含结尾 0），供测试拼接。
func packLabels(name string) []byte {
	out, err := packName(nil, name)
	if err != nil || len(out) == 0 {
		return nil
	}
	return out[:len(out)-1]
}

// unpackName 解析（可能带压缩指针的）名字。
func unpackName(msg []byte, off int) (string, int, error) {
	var labels []string
	next := -1
	jumped := false
	jumps := 0
	for {
		if off < 0 || off >= len(msg) {
			return "", 0, errMalformed
		}
		l := int(msg[off])
		switch {
		case l == 0:
			if !jumped {
				next = off + 1
			}
			name := strings.Join(labels, ".")
			if len(name) > maxNameLen {
				return "", 0, errMalformed
			}
			return name, next, nil
		case l&0xC0 == 0xC0:
			if off+1 >= len(msg) {
				return "", 0, errMalformed
			}
			ptr := int(binary.BigEndian.Uint16(msg[off:off+2]) & 0x3FFF)
			if !jumped {
				next = off + 2
				jumped = true
			}
			jumps++
			if jumps > maxJumps || ptr >= len(msg) {
				return "", 0, errMalformed
			}
			off = ptr
		case l&0xC0 != 0:
			return "", 0, errMalformed
		default:
			if off+1+l > len(msg) {
				return "", 0, errMalformed
			}
			labels = append(labels, string(msg[off+1:off+1+l]))
			off += 1 + l
			if !jumped {
				next = off
			}
		}
	}
}

func mustPTR(name string) []byte {
	out, err := packName(nil, name)
	if err != nil {
		return nil
	}
	return out
}

func mustSRV(port uint16, target string) []byte {
	out := make([]byte, 6)
	binary.BigEndian.PutUint16(out[4:6], port)
	name, err := packName(nil, target)
	if err != nil {
		return nil
	}
	return append(out, name...)
}

// rdataTXT 把键值对编码为 TXT（键排序，格式 k=v）。
func rdataTXT(kv map[string]string) []byte {
	keys := make([]string, 0, len(kv))
	for k := range kv {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var out []byte
	for _, k := range keys {
		item := k + "=" + kv[k]
		if len(item) > 255 {
			item = item[:255]
		}
		out = append(out, byte(len(item)))
		out = append(out, item...)
	}
	if len(out) == 0 {
		out = append(out, 0)
	}
	return out
}

func parsePTR(data []byte) (string, error) {
	name, _, err := unpackName(data, 0)
	return name, err
}

func parseSRV(data []byte) (uint16, string, error) {
	if len(data) < 6 {
		return 0, "", errMalformed
	}
	port := binary.BigEndian.Uint16(data[4:6])
	target, _, err := unpackName(data, 6)
	return port, target, err
}

func parseTXT(data []byte) map[string]string {
	out := map[string]string{}
	for off := 0; off < len(data); {
		l := int(data[off])
		off++
		if off+l > len(data) {
			break
		}
		item := string(data[off : off+l])
		off += l
		if item == "" {
			continue
		}
		if k, v, ok := strings.Cut(item, "="); ok {
			out[k] = v
		} else {
			out[item] = ""
		}
	}
	return out
}

// recordKey 用于已知答案抑制与冲突比较。
func recordKey(rr record) string {
	return strings.ToLower(rr.Name) + "|" + strconv.Itoa(int(rr.Type)) + "|" + string(rr.Data)
}
