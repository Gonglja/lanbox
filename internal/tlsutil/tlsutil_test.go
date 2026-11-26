package tlsutil

import (
	"crypto/tls"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestEnsureGeneratesAndReuses(t *testing.T) {
	dir := t.TempDir()
	certPath, keyPath, generated, err := Ensure(dir, "", "", []string{"localhost", "127.0.0.1", "10.0.0.5"})
	if err != nil {
		t.Fatalf("生成失败: %v", err)
	}
	if !generated {
		t.Fatal("首次调用应标记为已生成")
	}
	pair, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		t.Fatalf("证书不可加载: %v", err)
	}
	// LoadX509KeyPair 不填 Leaf，这里手动解析
	leaf, err := parseLeaf(certPath)
	if err != nil {
		t.Fatalf("解析证书失败: %v", err)
	}
	_ = pair
	if len(leaf.DNSNames) != 1 || leaf.DNSNames[0] != "localhost" {
		t.Fatalf("DNSNames 不对: %v", leaf.DNSNames)
	}
	if len(leaf.IPAddresses) != 2 {
		t.Fatalf("IPAddresses 数量不对: %v", leaf.IPAddresses)
	}

	before, err := os.ReadFile(certPath)
	if err != nil {
		t.Fatal(err)
	}
	_, _, generated2, err := Ensure(dir, "", "", []string{"localhost"})
	if err != nil {
		t.Fatalf("复用失败: %v", err)
	}
	if generated2 {
		t.Fatal("第二次调用不应重新生成")
	}
	after, err := os.ReadFile(certPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("已存在的证书被覆盖了")
	}

	info, err := os.Stat(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	// Windows 上没有 Unix 的权限位模型（Go 一律报 0666/0777），
	// 私钥收紧到 0600 只在类 Unix 平台可断言。
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("私钥权限应为 0600，得到 %v", info.Mode().Perm())
	}
}

func TestEnsureUsesConfiguredPair(t *testing.T) {
	dir := t.TempDir()
	certPath, keyPath, _, err := Ensure(dir, "", "", []string{"localhost"})
	if err != nil {
		t.Fatal(err)
	}
	gotCert, gotKey, generated, err := Ensure(dir, certPath, keyPath, nil)
	if err != nil {
		t.Fatalf("配置证书时应能加载: %v", err)
	}
	if generated || gotCert != certPath || gotKey != keyPath {
		t.Fatalf("不应重新生成: generated=%v %s %s", generated, gotCert, gotKey)
	}
	if _, _, _, err := Ensure(dir, certPath, "", nil); err == nil {
		t.Fatal("只配 cert 应报错")
	}
	if _, _, _, err := Ensure(dir, filepath.Join(dir, "missing.pem"), keyPath, nil); err == nil {
		t.Fatal("证书文件不存在应报错")
	}
}

func TestLocalIPsNonEmpty(t *testing.T) {
	ips, err := LocalIPs()
	if err != nil {
		t.Fatalf("收集本机 IP 失败: %v", err)
	}
	if len(ips) == 0 {
		t.Fatal("至少应有一个本机 IP")
	}
}

func TestHostsIncludesLoopback(t *testing.T) {
	hosts := Hosts(":8443")
	seen := map[string]bool{}
	for _, h := range hosts {
		seen[h] = true
	}
	if !seen["localhost"] || !seen["127.0.0.1"] {
		t.Fatalf("Hosts 应包含回环地址: %v", hosts)
	}
	hosts = Hosts("192.168.1.9:8443")
	found := false
	for _, h := range hosts {
		if h == "192.168.1.9" {
			found = true
		}
	}
	if !found {
		t.Fatalf("Hosts 应包含 listen 中的主机: %v", hosts)
	}
}
