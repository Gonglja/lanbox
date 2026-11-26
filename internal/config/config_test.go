package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDefaultsWhenFileMissing(t *testing.T) {
	c, err := Load(filepath.Join(t.TempDir(), "nope.json"))
	if err != nil {
		t.Fatalf("缺失文件不应报错: %v", err)
	}
	if c.Listen != ":8443" || c.DataDir != "./data" {
		t.Fatalf("默认监听/目录不对: %+v", c)
	}
	if c.MaxFileBytes() != 1024<<20 {
		t.Fatalf("默认单文件上限应为 1024MB，得到 %d", c.MaxFileBytes())
	}
	if c.SessionTTL() != 168*time.Hour {
		t.Fatalf("默认会话有效期应为 168h，得到 %v", c.SessionTTL())
	}
}

func TestFileOverridesDefaults(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.json")
	body := `{"listen":"0.0.0.0:9443","data_dir":"/srv/lanbox","max_file_size_mb":200,"min_free_disk_mb":50,"max_text_bytes":100,"session_ttl_hours":2,"tls":{"cert_file":"/c.pem","key_file":"/k.pem"}}`
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := Load(p)
	if err != nil {
		t.Fatalf("加载失败: %v", err)
	}
	if c.Listen != "0.0.0.0:9443" || c.DataDir != "/srv/lanbox" {
		t.Fatalf("文件覆盖未生效: %+v", c)
	}
	if c.MaxFileBytes() != 200<<20 || c.MinFreeBytes() != 50<<20 || c.MaxTextBytes != 100 {
		t.Fatalf("数值覆盖未生效: %+v", c)
	}
	if c.TLS.CertFile != "/c.pem" || c.TLS.KeyFile != "/k.pem" {
		t.Fatalf("TLS 配置未生效: %+v", c.TLS)
	}
}

func TestEnvOverridesFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.json")
	if err := os.WriteFile(p, []byte(`{"listen":":8443"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LANBOX_LISTEN", "127.0.0.1:10443")
	t.Setenv("LANBOX_MAX_FILE_SIZE_MB", "7")
	t.Setenv("LANBOX_DATA_DIR", dir)
	c, err := Load(p)
	if err != nil {
		t.Fatalf("加载失败: %v", err)
	}
	if c.Listen != "127.0.0.1:10443" || c.MaxFileSizeMB != 7 || c.DataDir != dir {
		t.Fatalf("环境变量未覆盖: %+v", c)
	}
}

func TestInvalidValuesRejected(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.json")
	cases := []string{
		`{"listen":""}`,
		`{"max_file_size_mb":0}`,
		`{"min_free_disk_mb":-1}`,
		`{"max_text_bytes":0}`,
		`{"session_ttl_hours":0}`,
		`{"tls":{"cert_file":"/c.pem"}}`,
		`not json`,
	}
	for _, body := range cases {
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(p); err == nil {
			t.Fatalf("应报错但通过了: %s", body)
		}
	}
}
