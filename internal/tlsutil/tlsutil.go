// Package tlsutil 负责确定 lanbox 使用的证书：优先用户配置，否则生成并复用自签证书。
package tlsutil

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const selfSignedValidity = 10 * 365 * 24 * time.Hour

// Ensure 返回应当使用的证书与私钥路径。
// certFile/keyFile 均非空：校验后直接使用；均为空：复用或生成 dataDir/tls/{cert.pem,key.pem}。
// generated 表示本次是否新生成。
func Ensure(dataDir, certFile, keyFile string, hosts []string) (certPath, keyPath string, generated bool, err error) {
	if (certFile == "") != (keyFile == "") {
		return "", "", false, errors.New("tls.cert_file 与 tls.key_file 必须同时配置或同时留空")
	}
	if certFile != "" {
		if _, err := tls.LoadX509KeyPair(certFile, keyFile); err != nil {
			return "", "", false, fmt.Errorf("加载证书失败（cert=%s key=%s）: %w", certFile, keyFile, err)
		}
		return certFile, keyFile, false, nil
	}

	dir := filepath.Join(dataDir, "tls")
	certPath = filepath.Join(dir, "cert.pem")
	keyPath = filepath.Join(dir, "key.pem")
	if fileExists(certPath) && fileExists(keyPath) {
		if _, err := tls.LoadX509KeyPair(certPath, keyPath); err == nil {
			return certPath, keyPath, false, nil
		}
		// 已存在的证书坏了：重新生成
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", "", false, fmt.Errorf("创建证书目录 %s 失败: %w", dir, err)
	}
	certPEM, keyPEM, err := generate(hosts, selfSignedValidity)
	if err != nil {
		return "", "", false, err
	}
	if err := os.WriteFile(certPath, certPEM, 0o644); err != nil {
		return "", "", false, fmt.Errorf("写入证书失败: %w", err)
	}
	if err := os.WriteFile(keyPath, keyPEM, 0o600); err != nil {
		return "", "", false, fmt.Errorf("写入私钥失败: %w", err)
	}
	return certPath, keyPath, true, nil
}

func generate(hosts []string, valid time.Duration) (certPEM, keyPEM []byte, err error) {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, fmt.Errorf("生成私钥失败: %w", err)
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, nil, fmt.Errorf("生成序列号失败: %w", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "lanbox", Organization: []string{"lanbox"}},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(valid),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}
	for _, h := range hosts {
		h = strings.TrimSpace(h)
		if h == "" {
			continue
		}
		if ip := net.ParseIP(h); ip != nil {
			tmpl.IPAddresses = append(tmpl.IPAddresses, ip)
		} else {
			tmpl.DNSNames = append(tmpl.DNSNames, h)
		}
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &priv.PublicKey, priv)
	if err != nil {
		return nil, nil, fmt.Errorf("签发证书失败: %w", err)
	}
	keyDER, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		return nil, nil, fmt.Errorf("序列化私钥失败: %w", err)
	}
	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	return certPEM, keyPEM, nil
}

// Hosts 汇总证书应包含的主机名/IP：localhost、回环、本机各网卡 IP、主机名、listen 中的主机部分。
func Hosts(listen string) []string {
	seen := map[string]bool{}
	var out []string
	add := func(h string) {
		h = strings.TrimSpace(h)
		if h == "" || seen[h] {
			return
		}
		seen[h] = true
		out = append(out, h)
	}
	add("localhost")
	add("127.0.0.1")
	add("::1")
	if host, _, err := net.SplitHostPort(listen); err == nil && host != "" {
		if ip := net.ParseIP(host); ip != nil {
			if !ip.IsUnspecified() {
				add(host)
			}
		} else {
			add(host)
		}
	}
	if hn, err := os.Hostname(); err == nil {
		add(hn)
	}
	if ips, err := LocalIPs(); err == nil {
		for _, ip := range ips {
			add(ip)
		}
	}
	return out
}

// LocalIPs 返回本机所有非未指定地址（含回环）。
func LocalIPs() ([]string, error) {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return nil, err
	}
	var out []string
	for _, a := range addrs {
		ipNet, ok := a.(*net.IPNet)
		if !ok || ipNet.IP.IsUnspecified() {
			continue
		}
		out = append(out, ipNet.IP.String())
	}
	return out, nil
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

// parseLeaf 解析 PEM 证书的第一块，供测试使用。
func parseLeaf(certPath string) (*x509.Certificate, error) {
	raw, err := os.ReadFile(certPath)
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(raw)
	if block == nil {
		return nil, errors.New("不是合法的 PEM")
	}
	return x509.ParseCertificate(block.Bytes)
}
