package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// writeFile 写配置文件。路径按 JSON 字符串转义，Windows 的反斜杠才不会破坏 JSON。
func writeFile(t *testing.T, p, body string) {
	t.Helper()
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// jsonPath 把文件路径转成可嵌入 JSON 字面量的形式。
func jsonPath(p string) string {
	raw, err := json.Marshal(p)
	if err != nil {
		panic(err)
	}
	return string(raw)
}

func TestWebOverrideBeatsFileAndEnv(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	writeFile(t, cfgPath, `{"data_dir":`+jsonPath(dir)+`,"max_text_bytes":100,"listen":":8443","mdns":{"hostname":"from-file"}}`)
	t.Setenv("LANBOX_MAX_TEXT_BYTES", "200")

	base, err := Baseline(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if base.MaxTextBytes != 200 {
		t.Fatalf("基线应为环境变量的 200，得到 %d", base.MaxTextBytes)
	}
	if err := saveOverrides(WebConfigPath(base), Overrides{
		MaxTextBytes: ptrInt(500),
		MDNS:         &MDNS{Enabled: false, Hostname: "from-web"},
	}); err != nil {
		t.Fatal(err)
	}

	c, err := Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if c.MaxTextBytes != 500 {
		t.Fatalf("网页覆盖应最高优先级，得到 %d", c.MaxTextBytes)
	}
	if c.MDNS.Enabled || c.MDNS.Hostname != "from-web" {
		t.Fatalf("mdns 覆盖未生效: %+v", c.MDNS)
	}
	if c.Origins["max_text_bytes"] != "web" || c.Origins["mdns.hostname"] != "web" {
		t.Fatalf("来源标注应为 web: %+v", c.Origins)
	}
	if c.Origins["listen"] != "file" {
		t.Fatalf("未被覆盖的字段仍应是 file: %+v", c.Origins)
	}
}

func TestWebConfigPathUsesBaselineDataDir(t *testing.T) {
	dir := t.TempDir()
	other := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	writeFile(t, cfgPath, `{"data_dir":`+jsonPath(dir)+`}`)

	base, err := Baseline(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := saveOverrides(WebConfigPath(base), Overrides{DataDir: ptrStr(other)}); err != nil {
		t.Fatal(err)
	}

	c, err := Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if c.DataDir != other {
		t.Fatalf("data_dir 覆盖未生效: %s", c.DataDir)
	}
	if _, err := os.Stat(filepath.Join(dir, "webconfig.json")); err != nil {
		t.Fatalf("覆盖文件应仍在基线 data_dir 里: %v", err)
	}
	if _, err := os.Stat(filepath.Join(other, "webconfig.json")); !os.IsNotExist(err) {
		t.Fatalf("新 data_dir 不应有覆盖文件: %v", err)
	}
}

func TestEmptyOverridesDeleteFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "webconfig.json")
	if err := saveOverrides(p, Overrides{MaxTextBytes: ptrInt(9)}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p); err != nil {
		t.Fatalf("应有覆盖文件: %v", err)
	}
	if err := saveOverrides(p, Overrides{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Fatalf("空覆盖应删除文件: %v", err)
	}
}

func TestDiffOnlyKeepsChangedFields(t *testing.T) {
	base := Defaults()
	want := base
	want.MaxTextBytes = 123
	want.MDNS.Hostname = "box"

	ov := diff(want, base)
	if ov.MaxTextBytes == nil || *ov.MaxTextBytes != 123 {
		t.Fatalf("max_text_bytes 应进入覆盖: %+v", ov)
	}
	if ov.MDNS == nil || ov.MDNS.Hostname != "box" {
		t.Fatalf("mdns 应整体进入覆盖: %+v", ov.MDNS)
	}
	if ov.Listen != nil || ov.DataDir != nil || ov.TLS != nil || ov.MaxFileSizeMB != nil {
		t.Fatalf("未变化字段不应进入覆盖: %+v", ov)
	}
}

func ptrInt(n int) *int       { return &n }
func ptrStr(s string) *string { return &s }
