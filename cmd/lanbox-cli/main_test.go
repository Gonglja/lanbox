package main

import (
	"bytes"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"strings"
	"testing"

	"lanbox/internal/client"
)

// TestExitCodeMapping 断言各类错误映射到稳定的退出码。
func TestExitCodeMapping(t *testing.T) {
	if got := exitCode(nil); got != exitOK {
		t.Fatalf("nil 应 0，得到 %d", got)
	}
	if got := exitCode(fmt.Errorf("%w: x", errUsage)); got != exitUsage {
		t.Fatalf("用法应 2，得到 %d", got)
	}
	if got := exitCode(fmt.Errorf("%w: x", errAuth)); got != exitAuth {
		t.Fatalf("认证应 4，得到 %d", got)
	}
	if got := exitCode(&client.Error{Status: 401, Code: "unauthorized"}); got != exitAuth {
		t.Fatalf("401 应 4，得到 %d", got)
	}
	if got := exitCode(&client.Error{Status: 403}); got != exitAuth {
		t.Fatalf("403 应 4，得到 %d", got)
	}
	if got := exitCode(&client.Error{Status: 500}); got != exitFail {
		t.Fatalf("500 应 1，得到 %d", got)
	}
	var ua x509.UnknownAuthorityError
	if got := exitCode(ua); got != exitNetwork {
		t.Fatalf("证书错误应 3，得到 %d", got)
	}
	if got := exitCode(&net.OpError{Op: "dial", Err: errors.New("refused")}); got != exitNetwork {
		t.Fatalf("网络错误应 3，得到 %d", got)
	}
	if got := exitCode(errors.New("boom")); got != exitFail {
		t.Fatalf("一般错误应 1，得到 %d", got)
	}
}

// TestPrintAndCodeCertHint 断言证书错误会打印三条解决办法并返回网络退出码。
func TestPrintAndCodeCertHint(t *testing.T) {
	var buf bytes.Buffer
	var ua x509.UnknownAuthorityError
	if code := printAndCode(ua, &buf); code != exitNetwork {
		t.Fatalf("证书错误应返回 exitNetwork(%d)，得到 %d", exitNetwork, code)
	}
	for _, want := range []string{"-ca", "信任库", "-insecure"} {
		if !strings.Contains(buf.String(), want) {
			t.Fatalf("提示应包含 %q，得到：%s", want, buf.String())
		}
	}
}

// TestPrintAndCodeNilIsSilent 断言没有错误时不往 stderr 写东西。
func TestPrintAndCodeNilIsSilent(t *testing.T) {
	var buf bytes.Buffer
	if code := printAndCode(nil, &buf); code != exitOK {
		t.Fatalf("nil 应返回 exitOK，得到 %d", code)
	}
	if buf.Len() != 0 {
		t.Fatalf("nil 时不应打印，得到 %q", buf.String())
	}
}
