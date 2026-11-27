package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDefaultsWhenFileMissing(t *testing.T) {
	c, err := Load(filepath.Join(t.TempDir(), "nope.json"))
	if err != nil {
		t.Fatalf("缺失文件不应报错: %v", err)
	}
	if c.Listen != ":10725" || c.DataDir != "./data" {
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

func TestDirectoryLimitsDefaults(t *testing.T) {
	c := Defaults()
	if c.MaxFilesPerItem != 500 {
		t.Fatalf("默认 max_files_per_item 应为 500，得到 %d", c.MaxFilesPerItem)
	}
	if c.MaxItemSizeMB != 2048 {
		t.Fatalf("默认 max_item_size_mb 应为 2048，得到 %d", c.MaxItemSizeMB)
	}
	if c.MaxItemBytes() != 2048<<20 {
		t.Fatalf("MaxItemBytes 换算错误: %d", c.MaxItemBytes())
	}
}

func TestDirectoryLimitsValidation(t *testing.T) {
	c := Defaults()
	c.MaxFilesPerItem = 0
	if err := c.Validate(); err == nil {
		t.Fatal("max_files_per_item 为 0 应报错")
	}
	c = Defaults()
	c.MaxFilesPerItem = 10001
	if err := c.Validate(); err == nil {
		t.Fatal("max_files_per_item 超过 10000 应报错")
	}
	c = Defaults()
	c.MaxItemSizeMB = 0
	if err := c.Validate(); err == nil {
		t.Fatal("max_item_size_mb 为 0 应报错")
	}
}

func TestDirectoryLimitsEnvOverride(t *testing.T) {
	t.Setenv("LANBOX_MAX_FILES_PER_ITEM", "42")
	t.Setenv("LANBOX_MAX_ITEM_SIZE_MB", "64")
	c, err := Load(filepath.Join(t.TempDir(), "missing.json"))
	if err != nil {
		t.Fatalf("加载配置失败: %v", err)
	}
	if c.MaxFilesPerItem != 42 || c.MaxItemSizeMB != 64 {
		t.Fatalf("环境变量未生效: %+v", c)
	}
}

func TestMDNSDefaultsAndEnv(t *testing.T) {
	c, err := Load(filepath.Join(t.TempDir(), "nope.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !c.MDNS.Enabled || c.MDNS.Hostname != "lanbox" || c.MDNS.Instance != "" {
		t.Fatalf("mDNS 默认值不对: %+v", c.MDNS)
	}
	if c.Origins["mdns.enabled"] != "default" || c.Origins["mdns.hostname"] != "default" {
		t.Fatalf("默认来源标注不对: %+v", c.Origins)
	}

	t.Setenv("LANBOX_MDNS_ENABLED", "off")
	t.Setenv("LANBOX_MDNS_HOSTNAME", "nas-1")
	t.Setenv("LANBOX_MDNS_INSTANCE", "机柜")
	c, err = Load(filepath.Join(t.TempDir(), "nope.json"))
	if err != nil {
		t.Fatal(err)
	}
	if c.MDNS.Enabled || c.MDNS.Hostname != "nas-1" || c.MDNS.Instance != "机柜" {
		t.Fatalf("环境变量未生效: %+v", c.MDNS)
	}
	for _, k := range []string{"mdns.enabled", "mdns.hostname", "mdns.instance"} {
		if c.Origins[k] != "env" {
			t.Fatalf("%s 来源应为 env，得到 %q", k, c.Origins[k])
		}
	}
}

func TestMDNSPresentKeysMarkedFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.json")
	body := `{"mdns":{"hostname":"box"},"tls":{"cert_file":"/c.pem","key_file":"/k.pem"}}`
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if c.Origins["mdns.hostname"] != "file" || c.Origins["mdns.enabled"] != "default" {
		t.Fatalf("嵌套字段来源标注不对: %+v", c.Origins)
	}
	if c.Origins["tls.cert_file"] != "file" || c.Origins["tls.key_file"] != "file" {
		t.Fatalf("tls 字段来源标注不对: %+v", c.Origins)
	}
}

func TestMDNSValidation(t *testing.T) {
	cases := []struct {
		name    string
		mdns    MDNS
		wantErr bool
	}{
		{"默认", MDNS{Enabled: true, Hostname: "lanbox"}, false},
		{"单字母", MDNS{Enabled: true, Hostname: "a"}, false},
		{"带数字连字符", MDNS{Enabled: true, Hostname: "nas-01"}, false},
		{"空主机名", MDNS{Enabled: true, Hostname: ""}, true},
		{"大写", MDNS{Enabled: true, Hostname: "NAS"}, true},
		{"下划线", MDNS{Enabled: true, Hostname: "my_nas"}, true},
		{"点号", MDNS{Enabled: true, Hostname: "a.b"}, true},
		{"首连字符", MDNS{Enabled: true, Hostname: "-a"}, true},
		{"尾连字符", MDNS{Enabled: true, Hostname: "a-"}, true},
		{"超长", MDNS{Enabled: true, Hostname: strings.Repeat("a", 64)}, true},
		{"实例名可用中文", MDNS{Enabled: true, Hostname: "lanbox", Instance: "机柜一"}, false},
		{"实例名含换行", MDNS{Enabled: true, Hostname: "lanbox", Instance: "a\nb"}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := Defaults()
			c.MDNS = tc.mdns
			err := c.Validate()
			if (err != nil) != tc.wantErr {
				t.Fatalf("wantErr=%v，得到 %v", tc.wantErr, err)
			}
		})
	}
}

func TestListenPort(t *testing.T) {
	cases := map[string]int{
		":443":         443,
		":8443":        8443,
		"0.0.0.0:9443": 9443,
		"":             10725,
		"bad":          10725,
		":notaport":    10725,
	}
	for in, want := range cases {
		c := Defaults()
		c.Listen = in
		if got := c.ListenPort(); got != want {
			t.Fatalf("ListenPort(%q)=%d，期望 %d", in, got, want)
		}
	}
}

func TestUpdateDefaultsAndEnv(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.json")
	c, err := Load(p)
	if err != nil {
		t.Fatalf("加载失败: %v", err)
	}
	if c.Update.Check != "off" || c.Update.URL != "" || c.Update.Token != "" {
		t.Fatalf("更新配置默认值不对: %+v", c.Update)
	}
	if c.Origins["update.check"] != "default" || c.Origins["update.url"] != "default" {
		t.Fatalf("来源标注不对: %v", c.Origins)
	}

	t.Setenv("LANBOX_UPDATE_CHECK", "OFF")
	t.Setenv("LANBOX_UPDATE_URL", "https://example.com/latest.json")
	t.Setenv("LANBOX_UPDATE_TOKEN", "secret")
	c, err = Load(p)
	if err != nil {
		t.Fatalf("加载失败: %v", err)
	}
	if c.Update.Check != "off" || c.Update.URL != "https://example.com/latest.json" || c.Update.Token != "secret" {
		t.Fatalf("环境变量未生效: %+v", c.Update)
	}
	if c.Origins["update.check"] != "env" || c.Origins["update.url"] != "env" {
		t.Fatalf("来源标注不对: %v", c.Origins)
	}
}

func TestUpdateConfigRejected(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.json")
	cases := map[string]string{
		`{"update":{"check":"hourly"}}`:                      "update.check",
		`{"update":{"url":"ftp://example.com/latest.json"}}`: "update.url",
		`{"update":{"url":"example.com/latest.json"}}`:       "update.url",
	}
	for body, want := range cases {
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		_, err := Load(p)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("%s 期望报错提及 %s，得到 %v", body, want, err)
		}
	}
}

func TestUpdateTokenNotSerialized(t *testing.T) {
	c := Defaults()
	c.Update.Token = "secret"
	b, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "secret") {
		t.Fatal("令牌不该出现在配置 JSON 里")
	}
}
