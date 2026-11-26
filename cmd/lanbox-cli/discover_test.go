package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"lanbox/internal/mdns"
)

func TestRenderServices(t *testing.T) {
	var buf bytes.Buffer
	services := []mdns.Service{
		{Instance: "office", Host: "nas", Port: 8443, Addrs: []net.IP{net.IPv4(192, 168, 1, 9)}, TXT: map[string]string{"v": "1", "fp": "0123456789abcdef"}},
		{Instance: "lab", Host: "pi", Port: 9443, Addrs: []net.IP{net.IPv4(10, 0, 0, 2)}, TXT: map[string]string{"v": "1"}},
	}
	if err := renderServices(&buf, services); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{"office", "https://192.168.1.9:8443", "01234567", "lab", "pi.local", "-"} {
		if !strings.Contains(out, want) {
			t.Fatalf("输出缺少 %q:\n%s", want, out)
		}
	}
}

func TestRenderServicesEmpty(t *testing.T) {
	var buf bytes.Buffer
	if err := renderServices(&buf, nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "未发现") {
		t.Fatalf("空结果应给出提示，得到 %q", buf.String())
	}
}

func TestDecideServer(t *testing.T) {
	orig := browseServices
	defer func() { browseServices = orig }()

	browseServices = func(time.Duration) ([]mdns.Service, error) { return nil, nil }
	if _, _, err := decideServer(10 * time.Millisecond); err == nil || !strings.Contains(err.Error(), "未发现") {
		t.Fatalf("零实例应给出可操作错误，得到 %v", err)
	}

	browseServices = func(time.Duration) ([]mdns.Service, error) {
		return []mdns.Service{
			{Instance: "office", Host: "nas", Port: 8443, Addrs: []net.IP{net.IPv4(192, 168, 1, 9)}},
		}, nil
	}
	base, fp, err := decideServer(10 * time.Millisecond)
	if err != nil || base != "https://192.168.1.9:8443" {
		t.Fatalf("唯一实例应直接使用，得到 %q %v", base, err)
	}
	if fp != "" {
		t.Fatalf("无 fp 时应为空，得到 %q", fp)
	}

	browseServices = func(time.Duration) ([]mdns.Service, error) {
		return []mdns.Service{
			{Instance: "a", Host: "x", Port: 8443, Addrs: []net.IP{net.IPv4(10, 0, 0, 1)}},
			{Instance: "b", Host: "y", Port: 8443, Addrs: []net.IP{net.IPv4(10, 0, 0, 2)}},
		}, nil
	}
	if _, _, err := decideServer(10 * time.Millisecond); err == nil || !strings.Contains(err.Error(), "多个") {
		t.Fatalf("多实例应要求 -s，得到 %v", err)
	}
}

func TestHTTPClientPinsCertFingerprint(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	sum := sha256.Sum256(srv.Certificate().Raw)
	fp := hex.EncodeToString(sum[:])

	var stderr bytes.Buffer
	g := &globals{pinFP: fp, stderr: &stderr}
	hc, err := httpClient(g)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := hc.Get(srv.URL)
	if err != nil {
		t.Fatalf("指纹正确时应连通: %v", err)
	}
	resp.Body.Close()
	if !strings.Contains(stderr.String(), "固定证书指纹") {
		t.Fatalf("应提示已固定指纹，得到 %q", stderr.String())
	}

	bad := &globals{pinFP: strings.Repeat("0", 64), stderr: bytes.NewBuffer(nil)}
	hc, err = httpClient(bad)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := hc.Get(srv.URL); err == nil || !strings.Contains(err.Error(), "指纹不匹配") {
		t.Fatalf("指纹不符应拒绝，得到 %v", err)
	}
}
