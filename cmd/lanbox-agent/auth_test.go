//go:build windows

package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"lanbox/internal/agent"
)

func TestNormalizeURL(t *testing.T) {
	cases := []struct{ in, want, err string }{
		{"lanbox.local", "https://lanbox.local", ""},
		{"https://192.168.1.9/", "https://192.168.1.9", ""},
		{"http://host:8080", "http://host:8080", ""},
		{"", "", "不能为空"},
		{"ftp://host", "", "http://"},
	}
	for _, c := range cases {
		got, err := normalizeURL(c.in)
		if c.err != "" {
			if err == nil || !strings.Contains(err.Error(), c.err) {
				t.Fatalf("%q 应报错含 %q，得到 %v", c.in, c.err, err)
			}
			continue
		}
		if err != nil || got != c.want {
			t.Fatalf("%q → (%q, %v)，期望 %q", c.in, got, err, c.want)
		}
	}
}

func TestPinnedClient(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	sum := sha256.Sum256(srv.Certificate().Raw)
	want := hex.EncodeToString(sum[:])

	// 指纹一致 → 请求成功
	hc := pinnedClient(want)
	resp, err := hc.Get(srv.URL)
	if err != nil {
		t.Fatalf("固定正确指纹应成功: %v", err)
	}
	resp.Body.Close()

	// 指纹不一致 → 拒绝并返回 errPinMismatch
	hc = pinnedClient(strings.Repeat("0", 64))
	if _, err := hc.Get(srv.URL); !errors.Is(err, errPinMismatch) {
		t.Fatalf("指纹不符应返回 errPinMismatch，得到 %v", err)
	}

	// 不固定指纹 → 自签证书校验失败（走系统信任库）
	hc = pinnedClient("")
	if _, err := hc.Get(srv.URL); err == nil {
		t.Fatal("未固定指纹时不信任自签证书")
	}
}

func TestProbeCert(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()
	got, err := probeCert(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("获取指纹失败: %v", err)
	}
	sum := sha256.Sum256(srv.Certificate().Raw)
	if got != hex.EncodeToString(sum[:]) {
		t.Fatalf("指纹不匹配: %s", got)
	}
}

func TestClassifyTLS(t *testing.T) {
	if got := classify(errPinMismatch); got != agent.FailureTLS {
		t.Fatalf("证书错误应分类为 FailureTLS，得到 %v", got)
	}
	if got := classify(errors.New("random")); got != agent.FailureRetry {
		t.Fatalf("普通错误应可重试，得到 %v", got)
	}
}
