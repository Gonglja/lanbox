//go:build windows

package main

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"lanbox/internal/agent"
	"lanbox/internal/client"
)

// errPinMismatch 表示服务端证书与固定的指纹不一致（可能是中间人）。
var errPinMismatch = errors.New("服务端证书指纹与固定值不一致，已拒绝连接")

// normalizeURL 补齐协议并去掉末尾斜杠。
func normalizeURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", errors.New("服务器地址不能为空")
	}
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "", fmt.Errorf("服务器地址无效: %q", raw)
	}
	if u.Scheme != "https" && u.Scheme != "http" {
		return "", fmt.Errorf("服务器地址必须以 http:// 或 https:// 开头")
	}
	return strings.TrimRight(u.String(), "/"), nil
}

// pinnedClient 构造 HTTP 客户端：fp 非空时严格比对叶证书 SHA-256。
func pinnedClient(fp string) *http.Client {
	tr := &http.Transport{
		Proxy:               http.ProxyFromEnvironment,
		DialContext:         (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		TLSHandshakeTimeout: 10 * time.Second,
		ForceAttemptHTTP2:   true,
		TLSClientConfig:     &tls.Config{MinVersion: tls.VersionTLS12},
	}
	if fp != "" {
		// 只信任固定指纹：先关掉系统信任链校验，再由下面的回调做指纹比对。
		tr.TLSClientConfig.InsecureSkipVerify = true
		want := strings.ToLower(strings.TrimSpace(fp))
		tr.TLSClientConfig.VerifyPeerCertificate = func(raw [][]byte, _ [][]*x509.Certificate) error {
			if len(raw) == 0 {
				return errors.New("服务端没有提供证书")
			}
			sum := sha256.Sum256(raw[0])
			if hex.EncodeToString(sum[:]) != want {
				return errPinMismatch
			}
			return nil
		}
	}
	return &http.Client{Transport: tr, Timeout: 0}
}

// probeCert 连接服务器并返回叶证书的 SHA-256（hex），用于首次手动信任。
func probeCert(ctx context.Context, baseURL string) (string, error) {
	normalized, err := normalizeURL(baseURL)
	if err != nil {
		return "", err
	}
	u, err := url.Parse(normalized)
	if err != nil {
		return "", err
	}
	host := u.Hostname()
	port := u.Port()
	if port == "" {
		if u.Scheme == "http" {
			port = "80"
		} else {
			port = "443"
		}
	}
	d := &net.Dialer{Timeout: 8 * time.Second}
	conn, err := tls.DialWithDialer(d, "tcp", net.JoinHostPort(host, port), &tls.Config{
		InsecureSkipVerify: true, // 只为取指纹，不做信任判断
		ServerName:         host,
		MinVersion:         tls.VersionTLS12,
	})
	if err != nil {
		return "", fmt.Errorf("连接服务器失败: %w", err)
	}
	defer conn.Close()
	certs := conn.ConnectionState().PeerCertificates
	if len(certs) == 0 {
		return "", errors.New("服务端没有提供证书")
	}
	sum := sha256.Sum256(certs[0].Raw)
	return hex.EncodeToString(sum[:]), nil
}

// doLogin 登录并返回会话与 HTTP 客户端。
func doLogin(ctx context.Context, baseURL, username, password, fp string) (*client.Session, *http.Client, error) {
	normalized, err := normalizeURL(baseURL)
	if err != nil {
		return nil, nil, err
	}
	hc := pinnedClient(fp)
	sess, err := client.Login(ctx, normalized, username, password, hc)
	if err != nil {
		return nil, nil, err
	}
	sess.PinFP = fp
	return sess, hc, nil
}

// classify 把上传错误分类成 agent.Failure。
func classify(err error) agent.Failure {
	if err == nil {
		return agent.FailurePermanent
	}
	if errors.Is(err, context.Canceled) {
		return agent.FailureCanceled
	}
	var ce *client.Error
	if errors.As(err, &ce) {
		switch {
		case ce.Status == http.StatusUnauthorized || ce.Status == http.StatusForbidden:
			return agent.FailureAuth
		case ce.Status == http.StatusRequestEntityTooLarge:
			return agent.FailureTooLarge
		case ce.Status == http.StatusInsufficientStorage:
			return agent.FailureNoSpace
		case ce.Code == "text_too_long" || ce.Code == "too_many_files":
			return agent.FailureTooLarge
		case ce.Status >= 500:
			return agent.FailureRetry
		default:
			return agent.FailurePermanent
		}
	}
	if isCertErr(err) {
		return agent.FailureTLS
	}
	return agent.FailureRetry
}

// isCertErr 判断是否证书类错误（不重试，提示用户重新固定指纹）。
func isCertErr(err error) bool {
	if errors.Is(err, errPinMismatch) {
		return true
	}
	var unknown x509.UnknownAuthorityError
	if errors.As(err, &unknown) {
		return true
	}
	var invalid x509.CertificateInvalidError
	if errors.As(err, &invalid) {
		return true
	}
	var verr *tls.CertificateVerificationError
	if errors.As(err, &verr) {
		return true
	}
	var hostErr x509.HostnameError
	if errors.As(err, &hostErr) {
		return true
	}
	return false
}
