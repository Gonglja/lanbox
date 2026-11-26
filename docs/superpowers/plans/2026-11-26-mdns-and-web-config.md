# lanbox mDNS 发现与网页配置实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让 lanbox 通过 mDNS 在局域网可被发现（服务端通告 + CLI 发现与证书指纹固定），并把全部配置项搬进网页管理员设置页（热更 / 保存并重启）。

**Architecture:** 新增零依赖的 `internal/mdns`（DNS 线格式、通告器、浏览）。配置新增 `mdns` 组与第四层"网页覆盖"（`<data_dir>/webconfig.json`），由 `config.Live` 统一负责校验、落盘、热更回调与重启冷却。`httpapi` 增加配置读写与 `healthz`；`main` 改为显式监听、启动自愈、自重启；网页管理抽屉扩成"用户 / 设置"两个标签页；CLI 增加 `discover`、无 `-s` 时自动发现并按 TXT 指纹固定 TLS。

**Tech Stack:** Go 1.25（**仅标准库**：`net`/`syscall`/`encoding/binary`/`crypto/tls`/`crypto/sha256`/`encoding/json`）、原生 ES 模块前端、`//go:embed`。

**规格来源：** `docs/superpowers/specs/2026-11-26-mdns-and-web-config-design.md`（实现时逐节对照）

---

## 文件结构

| 文件 | 责任 |
|---|---|
| `internal/config/config.go` | 修改：`MDNS` 字段、`Origins`、`loadBase`、env、校验、`ListenPort`、`EditableKeys` |
| `internal/config/overrides.go` | 新增：`Overrides`、`Baseline`、`WebConfigPath`、`loadOverrides`/`apply`/`diff`/`saveOverrides` |
| `internal/config/live.go` | 新增：`Live`（`Update`/`RequestRestart`/`DropWebOverride`）、`Result`、`RestartFields`、错误哨兵 |
| `internal/mdns/wire.go` | 新增：DNS 报文解析/打包、名字压缩、rdata 编解码 |
| `internal/mdns/responder.go` | 新增：通告器（探测/改名/播报/应答/bye）、`transport` 接口 |
| `internal/mdns/browse.go` | 新增：浏览与结果聚合 |
| `internal/mdns/sock_linux.go` | 新增：Linux 5353 套接字（REUSEADDR/REUSEPORT、逐网卡加组、逐网卡发送） |
| `internal/mdns/sock_other.go` | 新增：非 Linux 回落（`ListenMulticastUDP`） |
| `internal/store/store.go`、`files.go`、`items.go` | 修改：限额改为 `atomic.Pointer` 快照 + `SetLimits` |
| `internal/auth/auth.go` | 修改：`SetSessionTTL`，`m.ttl` 读取持锁 |
| `internal/httpapi/server.go`、`helpers.go`、`items_handlers.go`、`middleware.go` | 修改：当前配置快照 `current()`、`Live`/`Restart`/`MDNSStatus` 注入、`healthz`、访问日志跳过 |
| `internal/httpapi/config_handlers.go` | 新增：`GET/PUT /api/config`、`POST /api/config/restart` |
| `cmd/lanbox/main.go` | 修改：`OpenLive`、显式 `net.Listen`、启动自愈、mDNS 启动、重启分支、`.local` 地址 |
| `cmd/lanbox/mdnsglue.go` | 新增：`mdnsRunner`（启动/热更/状态适配） |
| `cmd/lanbox/restart_unix.go`、`restart_other.go` | 新增：`syscall.Exec` 自重启 |
| `cmd/lanbox-cli/cmd_discover.go` | 新增：`discover` 子命令 + 自动发现决策 |
| `cmd/lanbox-cli/main.go`、`render.go` | 修改：`pinFP`、`httpClient` 指纹固定、`resolveServer` 兜底、`renderServices`、usage |
| `web/index.html`、`admin.js`、`api.js`、`settings.js`、`style.css` | 修改/新增：设置标签页与重启遮罩 |
| `README.md` | 修改：mDNS、配置项、webconfig 语义、救援步骤、CLI discover、验收清单 |

**边界规则（不得违反）：**
- `internal/mdns` 不 import `internal/config`/`internal/httpapi`；只吃字符串、端口、`net.IP`。
- `internal/httpapi` 不拼磁盘路径，不 import `internal/mdns`（状态用 DTO 由 `main` 适配）。
- `internal/config` 不 import 其它内部包；`Live` 的重启/热更行为由 `main` 以钩子注入。
- `store` 仍不 import `net/http`；`auth` 不依赖 `httpapi`。
- 前端不得内联脚本（CSP `default-src 'self'`）。

---

## Task 1: 配置结构与来源标注（`mdns` 组）

**Files:**
- Modify: `internal/config/config.go`
- Test: `internal/config/config_test.go`

- [ ] **Step 1: 写失败测试**

追加到 `internal/config/config_test.go`：

```go
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
		{"首尾连字符", MDNS{Enabled: true, Hostname: "-a"}, true},
		{"结尾连字符", MDNS{Enabled: true, Hostname: "a-"}, true},
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
		":8443":            8443,
		"0.0.0.0:9443":     9443,
		"[::1]:443":        443,
		"":                 8443,
		"bad":              8443,
		":notaport":        8443,
	}
	for in, want := range cases {
		c := Defaults()
		c.Listen = in
		if got := c.ListenPort(); got != want {
			t.Fatalf("ListenPort(%q)=%d，期望 %d", in, got, want)
		}
	}
}
```

（`config_test.go` 需补 `strings` 导入。）

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/config -run 'TestMDNS|TestListenPort' -v`
Expected: FAIL —— `undefined: MDNS` / `c.Origins` / `c.ListenPort`

- [ ] **Step 3: 实现**

`internal/config/config.go` 顶部导入补 `net`、`strings`、`unicode/utf8`（`errors`/`fmt`/`strconv`/`time`/`os`/`encoding/json` 已在）。

在 `TLS` 之后追加：

```go
// MDNS 是局域网发现配置。
type MDNS struct {
	Enabled  bool   `json:"enabled"`
	Hostname string `json:"hostname"`
	Instance string `json:"instance"`
}
```

`Config` 结构体末尾追加两个字段：

```go
	MDNS    MDNS              `json:"mdns"`
	Origins map[string]string `json:"-"`
```

`Defaults()` 中追加 `MDNS: MDNS{Enabled: true, Hostname: "lanbox"}`，并在 `return` 前加 `c.Origins = defaultOrigins()`（改成先 `c := Config{...}` 再返回，见下）。

`Defaults` 改为：

```go
// Defaults 返回可直接使用的默认配置。
func Defaults() Config {
	c := Config{
		Listen:          ":8443",
		DataDir:         "./data",
		MaxFileSizeMB:   1024,
		MaxItemSizeMB:   2048,
		MinFreeDiskMB:   1024,
		MaxFilesPerItem: 500,
		MaxTextBytes:    65536,
		SessionTTLHours: 168,
		MDNS:            MDNS{Enabled: true, Hostname: "lanbox"},
	}
	c.Origins = defaultOrigins()
	return c
}
```

把现有 `Load` 拆成 `loadBase` + `Load`，并给 `applyEnv` 增加来源标注：

```go
// loadBase 读取"默认值 → 配置文件"，并标注来源（不含环境变量与网页覆盖）。
func loadBase(path string) (Config, map[string]string, error) {
	c := Defaults()
	origins := defaultOrigins()
	raw, err := os.ReadFile(path)
	switch {
	case err == nil:
		if err := json.Unmarshal(raw, &c); err != nil {
			return c, origins, fmt.Errorf("解析配置文件 %s 失败: %w", path, err)
		}
		for _, k := range presentKeys(raw) {
			if _, ok := origins[k]; ok {
				origins[k] = "file"
			}
		}
	case errors.Is(err, os.ErrNotExist):
		// 允许没有配置文件
	default:
		return c, origins, fmt.Errorf("读取配置文件 %s 失败: %w", path, err)
	}
	c.Origins = nil // 由调用方在全部层应用后设置
	return c, origins, nil
}

// Load 读取 path（不存在则仅用默认值），再应用环境变量、网页覆盖并校验。
func Load(path string) (Config, error) {
	c, origins, err := loadBase(path)
	if err != nil {
		return c, err
	}
	if err := applyEnv(&c, origins); err != nil {
		return c, err
	}
	c.Origins = origins
	if err := c.Validate(); err != nil {
		return c, err
	}
	return c, nil
}
```

新增来源工具函数：

```go
// editableKeys 是网页可读写的字段（点号路径）。
var editableKeys = []string{
	"listen", "data_dir", "tls.cert_file", "tls.key_file",
	"max_file_size_mb", "max_item_size_mb", "min_free_disk_mb",
	"max_files_per_item", "max_text_bytes", "session_ttl_hours",
	"mdns.enabled", "mdns.hostname", "mdns.instance",
}

// EditableKeys 返回网页可读写字段名（副本）。
func EditableKeys() []string { return append([]string(nil), editableKeys...) }

func defaultOrigins() map[string]string {
	m := make(map[string]string, len(editableKeys))
	for _, k := range editableKeys {
		m[k] = "default"
	}
	return m
}

// presentKeys 返回 JSON 文件里显式出现的字段（点号路径，嵌套一层）。
func presentKeys(raw []byte) []string {
	var top map[string]json.RawMessage
	if err := json.Unmarshal(raw, &top); err != nil {
		return nil
	}
	var out []string
	for k, v := range top {
		if k == "tls" || k == "mdns" {
			var nested map[string]json.RawMessage
			if err := json.Unmarshal(v, &nested); err == nil {
				for nk := range nested {
					out = append(out, k+"."+nk)
				}
				continue
			}
		}
		out = append(out, k)
	}
	return out
}
```

`applyEnv` 整段替换为（接收 `origins` 并在每个分支写来源）：

```go
func applyEnv(c *Config, origins map[string]string) error {
	mark := func(k string) { origins[k] = "env" }
	if v, ok := os.LookupEnv("LANBOX_LISTEN"); ok {
		c.Listen = v
		mark("listen")
	}
	if v, ok := os.LookupEnv("LANBOX_DATA_DIR"); ok {
		c.DataDir = v
		mark("data_dir")
	}
	if v, ok := os.LookupEnv("LANBOX_TLS_CERT_FILE"); ok {
		c.TLS.CertFile = v
		mark("tls.cert_file")
		mark("tls.key_file")
	}
	if v, ok := os.LookupEnv("LANBOX_TLS_KEY_FILE"); ok {
		c.TLS.KeyFile = v
		mark("tls.cert_file")
		mark("tls.key_file")
	}
	int64s := []struct {
		env string
		key string
		dst *int64
	}{
		{"LANBOX_MAX_FILE_SIZE_MB", "max_file_size_mb", &c.MaxFileSizeMB},
		{"LANBOX_MAX_ITEM_SIZE_MB", "max_item_size_mb", &c.MaxItemSizeMB},
		{"LANBOX_MIN_FREE_DISK_MB", "min_free_disk_mb", &c.MinFreeDiskMB},
	}
	for _, it := range int64s {
		v, ok := os.LookupEnv(it.env)
		if !ok {
			continue
		}
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			return fmt.Errorf("环境变量 %s 不是整数: %q", it.env, v)
		}
		*it.dst = n
		mark(it.key)
	}
	ints := []struct {
		env string
		key string
		dst *int
	}{
		{"LANBOX_MAX_FILES_PER_ITEM", "max_files_per_item", &c.MaxFilesPerItem},
		{"LANBOX_MAX_TEXT_BYTES", "max_text_bytes", &c.MaxTextBytes},
		{"LANBOX_SESSION_TTL_HOURS", "session_ttl_hours", &c.SessionTTLHours},
	}
	for _, it := range ints {
		v, ok := os.LookupEnv(it.env)
		if !ok {
			continue
		}
		n, err := strconv.Atoi(v)
		if err != nil {
			return fmt.Errorf("环境变量 %s 不是整数: %q", it.env, v)
		}
		*it.dst = n
		mark(it.key)
	}
	if v, ok := os.LookupEnv("LANBOX_MDNS_ENABLED"); ok {
		b, err := parseBool(v)
		if err != nil {
			return fmt.Errorf("环境变量 LANBOX_MDNS_ENABLED 不是布尔值: %q", v)
		}
		c.MDNS.Enabled = b
		mark("mdns.enabled")
	}
	if v, ok := os.LookupEnv("LANBOX_MDNS_HOSTNAME"); ok {
		c.MDNS.Hostname = v
		mark("mdns.hostname")
	}
	if v, ok := os.LookupEnv("LANBOX_MDNS_INSTANCE"); ok {
		c.MDNS.Instance = v
		mark("mdns.instance")
	}
	return nil
}

func parseBool(v string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true, nil
	case "0", "false", "no", "off":
		return false, nil
	}
	return false, errors.New("不是布尔值")
}
```

`Validate()` 末尾（`tls` 互斥检查之后）追加：

```go
	if err := validateLabel(c.MDNS.Hostname, "mdns.hostname"); err != nil {
		return err
	}
	if c.MDNS.Instance != "" {
		if n := utf8.RuneCountInString(c.MDNS.Instance); n > 63 {
			return errors.New("mdns.instance 不能超过 63 个字符")
		}
		if strings.ContainsAny(c.MDNS.Instance, "\x00\r\n") {
			return errors.New("mdns.instance 不能包含控制字符")
		}
	}
```

新增：

```go
// validateLabel 校验 RFC 1123 的 DNS label（小写字母、数字、连字符）。
func validateLabel(s, field string) error {
	if s == "" {
		return fmt.Errorf("%s 不能为空", field)
	}
	if len(s) > 63 {
		return fmt.Errorf("%s 不能超过 63 个字符", field)
	}
	for i := 0; i < len(s); i++ {
		ch := s[i]
		switch {
		case ch >= 'a' && ch <= 'z', ch >= '0' && ch <= '9':
		case ch == '-':
			if i == 0 || i == len(s)-1 {
				return fmt.Errorf("%s 不能以连字符开头或结尾", field)
			}
		default:
			return fmt.Errorf("%s 只能包含小写字母、数字和连字符", field)
		}
	}
	return nil
}

// ListenPort 从 listen 取端口，解析失败回落 8443。
func (c Config) ListenPort() int {
	_, port, err := net.SplitHostPort(c.Listen)
	if err != nil {
		return 8443
	}
	n, err := strconv.Atoi(port)
	if err != nil || n <= 0 || n > 65535 {
		return 8443
	}
	return n
}
```

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/config -count=1`
Expected: PASS（同时确认原有的 `TestEnvOverridesFile` 等仍然通过；若旧测试直接构造 `Config{...}` 再调 `Validate`，需在构造后补 `c.Origins = defaultOrigins()` 或忽略）

- [ ] **Step 5: 提交**

```bash
git add internal/config/config.go internal/config/config_test.go
git commit -m "feat(config): mdns 配置组与字段来源标注"
```

---

## Task 2: 网页覆盖层 `webconfig.json`

**Files:**
- Create: `internal/config/overrides.go`
- Modify: `internal/config/config.go`（`Load` 应用覆盖）
- Test: `internal/config/overrides_test.go`

- [ ] **Step 1: 写失败测试**

```go
package config

import (
	"os"
	"path/filepath"
	"testing"
)

func writeFile(t *testing.T, p, body string) {
	t.Helper()
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestWebOverrideBeatsFileAndEnv(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	writeFile(t, cfgPath, `{"data_dir":"`+dir+`","max_text_bytes":100,"listen":":8443","mdns":{"hostname":"from-file"}}`)
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
	writeFile(t, cfgPath, `{"data_dir":"`+dir+`"}`)

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
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/config -run 'TestWeb|TestEmpty|TestDiff' -v`
Expected: FAIL —— `undefined: Baseline` / `Overrides` / `saveOverrides`

- [ ] **Step 3: 实现 `internal/config/overrides.go`**

```go
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Overrides 是网页保存的稀疏覆盖值（未设置的字段为 nil）。
type Overrides struct {
	Listen          *string `json:"listen,omitempty"`
	DataDir         *string `json:"data_dir,omitempty"`
	TLS             *TLS    `json:"tls,omitempty"`
	MaxFileSizeMB   *int64  `json:"max_file_size_mb,omitempty"`
	MaxItemSizeMB   *int64  `json:"max_item_size_mb,omitempty"`
	MinFreeDiskMB   *int64  `json:"min_free_disk_mb,omitempty"`
	MaxFilesPerItem *int    `json:"max_files_per_item,omitempty"`
	MaxTextBytes    *int    `json:"max_text_bytes,omitempty"`
	SessionTTLHours *int    `json:"session_ttl_hours,omitempty"`
	MDNS            *MDNS   `json:"mdns,omitempty"`
}

// WebConfigPath 返回网页覆盖文件路径（固定在基线 data_dir 下）。
func WebConfigPath(base Config) string { return filepath.Join(base.DataDir, "webconfig.json") }

// Baseline 返回"默认值 → 文件 → 环境变量"的配置（不含网页覆盖）。
func Baseline(path string) (Config, error) {
	c, origins, err := loadBase(path)
	if err != nil {
		return Defaults(), err
	}
	if err := applyEnv(&c, origins); err != nil {
		return c, err
	}
	c.Origins = origins
	if err := c.Validate(); err != nil {
		return c, err
	}
	return c, nil
}

func loadOverrides(path string) (Overrides, error) {
	var o Overrides
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return o, nil
	}
	if err != nil {
		return o, fmt.Errorf("读取 %s 失败: %w", path, err)
	}
	if err := json.Unmarshal(raw, &o); err != nil {
		return o, fmt.Errorf("解析 %s 失败: %w", path, err)
	}
	return o, nil
}

func (o Overrides) empty() bool {
	return o.Listen == nil && o.DataDir == nil && o.TLS == nil &&
		o.MaxFileSizeMB == nil && o.MaxItemSizeMB == nil && o.MinFreeDiskMB == nil &&
		o.MaxFilesPerItem == nil && o.MaxTextBytes == nil && o.SessionTTLHours == nil &&
		o.MDNS == nil
}

func (o Overrides) apply(c *Config, origins map[string]string) {
	mark := func(k string) { origins[k] = "web" }
	if o.Listen != nil {
		c.Listen = *o.Listen
		mark("listen")
	}
	if o.DataDir != nil {
		c.DataDir = *o.DataDir
		mark("data_dir")
	}
	if o.TLS != nil {
		c.TLS = *o.TLS
		mark("tls.cert_file")
		mark("tls.key_file")
	}
	if o.MaxFileSizeMB != nil {
		c.MaxFileSizeMB = *o.MaxFileSizeMB
		mark("max_file_size_mb")
	}
	if o.MaxItemSizeMB != nil {
		c.MaxItemSizeMB = *o.MaxItemSizeMB
		mark("max_item_size_mb")
	}
	if o.MinFreeDiskMB != nil {
		c.MinFreeDiskMB = *o.MinFreeDiskMB
		mark("min_free_disk_mb")
	}
	if o.MaxFilesPerItem != nil {
		c.MaxFilesPerItem = *o.MaxFilesPerItem
		mark("max_files_per_item")
	}
	if o.MaxTextBytes != nil {
		c.MaxTextBytes = *o.MaxTextBytes
		mark("max_text_bytes")
	}
	if o.SessionTTLHours != nil {
		c.SessionTTLHours = *o.SessionTTLHours
		mark("session_ttl_hours")
	}
	if o.MDNS != nil {
		c.MDNS = *o.MDNS
		mark("mdns.enabled")
		mark("mdns.hostname")
		mark("mdns.instance")
	}
}

// diff 求 c 相对 baseline 的差集。
func diff(c, baseline Config) Overrides {
	var o Overrides
	if c.Listen != baseline.Listen {
		v := c.Listen
		o.Listen = &v
	}
	if c.DataDir != baseline.DataDir {
		v := c.DataDir
		o.DataDir = &v
	}
	if c.TLS != baseline.TLS {
		v := c.TLS
		o.TLS = &v
	}
	if c.MaxFileSizeMB != baseline.MaxFileSizeMB {
		v := c.MaxFileSizeMB
		o.MaxFileSizeMB = &v
	}
	if c.MaxItemSizeMB != baseline.MaxItemSizeMB {
		v := c.MaxItemSizeMB
		o.MaxItemSizeMB = &v
	}
	if c.MinFreeDiskMB != baseline.MinFreeDiskMB {
		v := c.MinFreeDiskMB
		o.MinFreeDiskMB = &v
	}
	if c.MaxFilesPerItem != baseline.MaxFilesPerItem {
		v := c.MaxFilesPerItem
		o.MaxFilesPerItem = &v
	}
	if c.MaxTextBytes != baseline.MaxTextBytes {
		v := c.MaxTextBytes
		o.MaxTextBytes = &v
	}
	if c.SessionTTLHours != baseline.SessionTTLHours {
		v := c.SessionTTLHours
		o.SessionTTLHours = &v
	}
	if c.MDNS != baseline.MDNS {
		v := c.MDNS
		o.MDNS = &v
	}
	return o
}

// saveOverrides 原子写入覆盖；无覆盖时删除文件。
func saveOverrides(path string, o Overrides) error {
	if o.empty() {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("删除 %s 失败: %w", path, err)
		}
		return nil
	}
	raw, err := json.MarshalIndent(o, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("创建目录 %s 失败: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, ".webconfig-*.json")
	if err != nil {
		return fmt.Errorf("创建临时文件失败: %w", err)
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(raw); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("替换 %s 失败: %w", path, err)
	}
	if d, err := os.Open(dir); err == nil {
		_ = d.Sync()
		_ = d.Close()
	}
	return nil
}
```

`Load` 改为应用覆盖：

```go
// Load 读取 path（不存在则仅用默认值），再应用环境变量、网页覆盖并校验。
func Load(path string) (Config, error) {
	c, origins, err := loadBase(path)
	if err != nil {
		return c, err
	}
	if err := applyEnv(&c, origins); err != nil {
		return c, err
	}
	ov, err := loadOverrides(WebConfigPath(c))
	if err != nil {
		return c, err
	}
	ov.apply(&c, origins)
	c.Origins = origins
	if err := c.Validate(); err != nil {
		return c, err
	}
	return c, nil
}
```

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/config -count=1`
Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add internal/config/overrides.go internal/config/overrides_test.go internal/config/config.go
git commit -m "feat(config): 网页覆盖层 webconfig.json（优先级最高、可删可回滚）"
```

---

## Task 3: `config.Live`（校验、落盘、热更、重启冷却）

**Files:**
- Create: `internal/config/live.go`
- Test: `internal/config/live_test.go`

- [ ] **Step 1: 写失败测试**

```go
package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLiveUpdateHotAndRestartFields(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	writeFile(t, cfgPath, `{"data_dir":"`+dir+`"}`)

	var gotPrev, gotNext Config
	applied := 0
	l, err := OpenLive(cfgPath, Hooks{Apply: func(prev, next Config) error {
		gotPrev, gotNext = prev, next
		applied++
		return nil
	}})
	if err != nil {
		t.Fatal(err)
	}

	desired := l.Current()
	desired.MaxTextBytes = 2048
	desired.Listen = ":9443"
	res, err := l.Update(desired)
	if err != nil {
		t.Fatalf("Update 失败: %v", err)
	}
	if applied != 1 || gotNext.MaxTextBytes != 2048 || gotPrev.MaxTextBytes == gotNext.MaxTextBytes {
		t.Fatalf("热更钩子未收到正确 prev/next: %+v -> %+v", gotPrev, gotNext)
	}
	if len(res.Applied) != 1 || res.Applied[0] != "max_text_bytes" {
		t.Fatalf("applied 不对: %+v", res.Applied)
	}
	if len(res.RestartRequired) != 1 || res.RestartRequired[0] != "listen" {
		t.Fatalf("restart_required 不对: %+v", res.RestartRequired)
	}
	if l.Current().Origins["listen"] != "web" || l.Current().Origins["max_text_bytes"] != "web" {
		t.Fatalf("来源应标为 web: %+v", l.Current().Origins)
	}

	// 清掉覆盖后应回到基线
	if _, err := l.DropWebOverride("listen"); err != nil {
		t.Fatal(err)
	}
	if l.Current().Listen != ":8443" {
		t.Fatalf("DropWebOverride 后 listen 应回到默认: %s", l.Current().Listen)
	}
}

func TestLiveRejectsInvalidValuesWithoutWriting(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	writeFile(t, cfgPath, `{"data_dir":"`+dir+`"}`)
	l, err := OpenLive(cfgPath, Hooks{})
	if err != nil {
		t.Fatal(err)
	}
	bad := l.Current()
	bad.MaxFilesPerItem = 0
	if _, err := l.Update(bad); err == nil {
		t.Fatal("非法值应报错")
	}
	if _, err := os.Stat(filepath.Join(dir, "webconfig.json")); !os.IsNotExist(err) {
		t.Fatal("校验失败不应落盘")
	}
}

func TestLiveRestartCooldown(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	writeFile(t, cfgPath, `{"data_dir":"`+dir+`"}`)
	calls := 0
	now := time.Now()
	l, err := OpenLive(cfgPath, Hooks{Restart: func() { calls++ }})
	if err != nil {
		t.Fatal(err)
	}
	l.now = func() time.Time { return now }
	l.cooldown = 10 * time.Second

	if !l.RequestRestart() {
		t.Fatal("首次重启应允许")
	}
	if l.RequestRestart() {
		t.Fatal("冷却期内应拒绝")
	}
	now = now.Add(11 * time.Second)
	if !l.RequestRestart() {
		t.Fatal("冷却结束后应允许")
	}
	if calls != 2 {
		t.Fatalf("Restart 钩子应调用 2 次，得到 %d", calls)
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/config -run TestLive -v`
Expected: FAIL —— `undefined: OpenLive`

- [ ] **Step 3: 实现 `internal/config/live.go`**

```go
package config

import (
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"
)

// Result 描述一次配置更新的影响面。
type Result struct {
	Changed         []string `json:"changed"`
	Applied         []string `json:"applied"`
	RestartRequired []string `json:"restart_required"`
}

var (
	// ErrInvalidTLS 表示新的证书/私钥无法加载。
	ErrInvalidTLS = errors.New("证书无法加载")
	// ErrListenUnavailable 表示新的监听地址绑定失败。
	ErrListenUnavailable = errors.New("监听地址不可用")
)

// restartFields 是修改后必须重启才能生效的字段。
var restartFields = []string{"listen", "data_dir", "tls.cert_file", "tls.key_file"}

// RestartFields 返回必须重启才能生效的字段名。
func RestartFields() []string { return append([]string(nil), restartFields...) }

func needsRestart(key string) bool {
	for _, k := range restartFields {
		if k == key {
			return true
		}
	}
	return false
}

// Hooks 是 Live 对进程其它部件的回调。
type Hooks struct {
	Apply   func(prev, next Config) error // 热更
	Restart func()                        // 请求自重启（非阻塞）
}

// Live 持有当前生效配置，负责落盘与回调。
type Live struct {
	cfgPath string
	webPath string
	base    Config
	hooks   Hooks

	mu          sync.RWMutex
	cur         Config
	lastRestart time.Time
	cooldown    time.Duration
	now         func() time.Time
}

// OpenLive 载入配置并接管后续变更。
func OpenLive(cfgPath string, hooks Hooks) (*Live, error) {
	cur, err := Load(cfgPath)
	if err != nil {
		return nil, err
	}
	base, err := Baseline(cfgPath)
	if err != nil {
		return nil, err
	}
	return &Live{cfgPath: cfgPath, webPath: WebConfigPath(base), base: base, hooks: hooks,
		cur: cur, cooldown: 10 * time.Second, now: time.Now}, nil
}

// BaselineConfig 返回"不含网页覆盖"的基线配置（供网页显示"恢复默认"的基准值）。
func (l *Live) BaselineConfig() Config {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.base
}

// Current 返回当前配置快照。
func (l *Live) Current() Config {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.cur
}

// Paths 返回配置文件与覆盖文件路径。
func (l *Live) Paths() (configFile, webConfig string) { return l.cfgPath, l.webPath }

// Update 校验 desired（完整配置）→ 落盘覆盖 → 热更并返回影响面。
func (l *Live) Update(desired Config) (Result, error) {
	var res Result
	desired.Origins = nil
	if err := desired.Validate(); err != nil {
		return res, err
	}

	prev := l.Current()
	if desired.TLS != prev.TLS {
		if _, err := tls.LoadX509KeyPair(desired.TLS.CertFile, desired.TLS.KeyFile); err != nil {
			return res, fmt.Errorf("%w: %v", ErrInvalidTLS, err)
		}
	}
	if desired.Listen != prev.Listen {
		ln, err := net.Listen("tcp", desired.Listen)
		if err != nil {
			return res, fmt.Errorf("%w: %v", ErrListenUnavailable, err)
		}
		_ = ln.Close()
	}

	base, err := Baseline(l.cfgPath)
	if err != nil {
		return res, err
	}
	if err := saveOverrides(l.webPath, diff(desired, base)); err != nil {
		return res, err
	}

	origins := make(map[string]string, len(base.Origins))
	for k, v := range base.Origins {
		origins[k] = v
	}
	ov := diff(desired, base)
	ov.apply(&desired, origins)
	desired.Origins = origins

	res.Changed = changedKeys(prev, desired)
	for _, k := range res.Changed {
		if needsRestart(k) {
			res.RestartRequired = append(res.RestartRequired, k)
		} else {
			res.Applied = append(res.Applied, k)
		}
	}

	l.mu.Lock()
	l.cur = desired
	l.mu.Unlock()

	if len(res.Applied) > 0 && l.hooks.Apply != nil {
		if err := l.hooks.Apply(prev, desired); err != nil {
			return res, err
		}
	}
	return res, nil
}

// DropWebOverride 清除单个字段的网页覆盖（启动自愈与"恢复默认"用）。
func (l *Live) DropWebOverride(key string) (Result, error) {
	var res Result
	prev := l.Current()
	ov, err := loadOverrides(l.webPath)
	if err != nil {
		return res, err
	}
	if !clearOverride(&ov, key) {
		return res, nil
	}
	if err := saveOverrides(l.webPath, ov); err != nil {
		return res, err
	}
	next, err := Load(l.cfgPath)
	if err != nil {
		return res, err
	}
	res.Changed = changedKeys(prev, next)
	for _, k := range res.Changed {
		if needsRestart(k) {
			res.RestartRequired = append(res.RestartRequired, k)
		} else {
			res.Applied = append(res.Applied, k)
		}
	}
	l.mu.Lock()
	l.cur = next
	l.mu.Unlock()
	if len(res.Applied) > 0 && l.hooks.Apply != nil {
		if err := l.hooks.Apply(prev, next); err != nil {
			return res, err
		}
	}
	return res, nil
}

// RequestRestart 触发一次自重启，冷却期内返回 false。
func (l *Live) RequestRestart() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	if !l.lastRestart.IsZero() && now.Sub(l.lastRestart) < l.cooldown {
		return false
	}
	l.lastRestart = now
	if l.hooks.Restart != nil {
		l.hooks.Restart()
	}
	return true
}

func clearOverride(o *Overrides, key string) bool {
	switch key {
	case "listen":
		if o.Listen != nil {
			o.Listen = nil
			return true
		}
	case "data_dir":
		if o.DataDir != nil {
			o.DataDir = nil
			return true
		}
	case "tls.cert_file", "tls.key_file":
		if o.TLS != nil {
			o.TLS = nil
			return true
		}
	case "max_file_size_mb":
		if o.MaxFileSizeMB != nil {
			o.MaxFileSizeMB = nil
			return true
		}
	case "max_item_size_mb":
		if o.MaxItemSizeMB != nil {
			o.MaxItemSizeMB = nil
			return true
		}
	case "min_free_disk_mb":
		if o.MinFreeDiskMB != nil {
			o.MinFreeDiskMB = nil
			return true
		}
	case "max_files_per_item":
		if o.MaxFilesPerItem != nil {
			o.MaxFilesPerItem = nil
			return true
		}
	case "max_text_bytes":
		if o.MaxTextBytes != nil {
			o.MaxTextBytes = nil
			return true
		}
	case "session_ttl_hours":
		if o.SessionTTLHours != nil {
			o.SessionTTLHours = nil
			return true
		}
	case "mdns.enabled", "mdns.hostname", "mdns.instance":
		if o.MDNS != nil {
			o.MDNS = nil
			return true
		}
	}
	return false
}

func changedKeys(prev, next Config) []string {
	var out []string
	add := func(k string, changed bool) {
		if changed {
			out = append(out, k)
		}
	}
	add("listen", prev.Listen != next.Listen)
	add("data_dir", prev.DataDir != next.DataDir)
	add("tls.cert_file", prev.TLS.CertFile != next.TLS.CertFile)
	add("tls.key_file", prev.TLS.KeyFile != next.TLS.KeyFile)
	add("max_file_size_mb", prev.MaxFileSizeMB != next.MaxFileSizeMB)
	add("max_item_size_mb", prev.MaxItemSizeMB != next.MaxItemSizeMB)
	add("min_free_disk_mb", prev.MinFreeDiskMB != next.MinFreeDiskMB)
	add("max_files_per_item", prev.MaxFilesPerItem != next.MaxFilesPerItem)
	add("max_text_bytes", prev.MaxTextBytes != next.MaxTextBytes)
	add("session_ttl_hours", prev.SessionTTLHours != next.SessionTTLHours)
	add("mdns.enabled", prev.MDNS.Enabled != next.MDNS.Enabled)
	add("mdns.hostname", prev.MDNS.Hostname != next.MDNS.Hostname)
	add("mdns.instance", prev.MDNS.Instance != next.MDNS.Instance)
	return out
}
```

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/config -count=1`
Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add internal/config/live.go internal/config/live_test.go
git commit -m "feat(config): Live 统一校验/落盘/热更/重启冷却"
```

---

## Task 4: mDNS 线格式（`internal/mdns/wire.go`）

**Files:**
- Create: `internal/mdns/wire.go`
- Test: `internal/mdns/wire_test.go`

- [ ] **Step 1: 写失败测试**

```go
package mdns

import (
	"net"
	"reflect"
	"strings"
	"testing"
)

func TestMessageRoundTrip(t *testing.T) {
	m := &message{
		Response:      true,
		Authoritative: true,
		Answers: []record{
			{Name: "_lanbox._tcp.local", Type: typePTR, Class: classINET, TTL: 120, Data: mustPTR("lanbox._lanbox._tcp.local")},
			{Name: "lanbox._lanbox._tcp.local", Type: typeSRV, Class: classINET | cacheFlushFlag, TTL: 120, Data: mustSRV(8443, "lanbox.local")},
			{Name: "lanbox._lanbox._tcp.local", Type: typeTXT, Class: classINET | cacheFlushFlag, TTL: 120, Data: rdataTXT(map[string]string{"v": "1", "fp": "abc"})},
			{Name: "lanbox.local", Type: typeA, Class: classINET | cacheFlushFlag, TTL: 120, Data: net.IPv4(192, 168, 1, 9).To4()},
		},
	}
	raw, err := m.Pack()
	if err != nil {
		t.Fatalf("打包失败: %v", err)
	}
	got, err := Parse(raw)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if !got.Response || !got.Authoritative || len(got.Answers) != len(m.Answers) {
		t.Fatalf("头部/记录数不对: %+v", got)
	}
	for i := range m.Answers {
		if got.Answers[i].Name != m.Answers[i].Name || got.Answers[i].Type != m.Answers[i].Type || got.Answers[i].TTL != m.Answers[i].TTL {
			t.Fatalf("第 %d 条记录不一致: %+v vs %+v", i, got.Answers[i], m.Answers[i])
		}
	}
	if name, err := parsePTR(got.Answers[0].Data); err != nil || name != "lanbox._lanbox._tcp.local" {
		t.Fatalf("PTR 解析不对: %q %v", name, err)
	}
	port, target, err := parseSRV(got.Answers[1].Data)
	if err != nil || port != 8443 || target != "lanbox.local" {
		t.Fatalf("SRV 解析不对: %d %q %v", port, target, err)
	}
	txt := parseTXT(got.Answers[2].Data)
	if !reflect.DeepEqual(txt, map[string]string{"v": "1", "fp": "abc"}) {
		t.Fatalf("TXT 解析不对: %+v", txt)
	}
}

func TestParseCompressedName(t *testing.T) {
	// 手工构造：header(qd=1,an=1) + 问题 "a.local" + 名字用指针指回问题名
	hdr := make([]byte, 12)
	binary.BigEndian.PutUint16(hdr[2:4], 0x8400)
	binary.BigEndian.PutUint16(hdr[4:6], 1)
	binary.BigEndian.PutUint16(hdr[6:8], 1)
	raw := append([]byte{}, hdr...)

	qnameOff := len(raw)
	raw, _ = packName(raw, "a.local")
	raw = append(raw, 0, 12, 0, 1) // PTR IN

	// answer：名字指针指回问题名，PTR，TTL 120，rdata 是 "b.local"
	raw = append(raw, 0xC0, byte(qnameOff), 0, 12, 0, 1, 0, 0, 0, 120)
	target, err := packName(nil, "b.local")
	if err != nil {
		t.Fatal(err)
	}
	raw = append(raw, 0, byte(len(target)))
	raw = append(raw, target...)

	m, err := Parse(raw)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if len(m.Questions) != 1 || m.Questions[0].Name != "a.local" {
		t.Fatalf("问题解析不对: %+v", m.Questions)
	}
	if len(m.Answers) != 1 || m.Answers[0].Name != "a.local" {
		t.Fatalf("压缩指针名字解析不对: %+v", m.Answers)
	}
	if name, err := parsePTR(m.Answers[0].Data); err != nil || name != "b.local" {
		t.Fatalf("压缩 rdata 名字未展开: %q %v", name, err)
	}
}

func TestParseRejectsBadPackets(t *testing.T) {
	cases := [][]byte{
		{},
		{0, 0},
		{0, 1, 0, 0, 0, 1, 0, 0, 0, 0, 0, 0}, // 问题数 1 但没有问题
		[]byte(strings.Repeat("\xff", 64)),   // 非法名字长度
	}
	for _, raw := range cases {
		if _, err := Parse(raw); err == nil {
			t.Fatalf("坏包应报错: %v", raw)
		}
	}
}

func TestQuestionUnicastFlag(t *testing.T) {
	q := question{Name: "x", Type: typePTR, Class: classINET | classUnicastFlag}
	if !q.unicast() || q.qclass() != classINET {
		t.Fatalf("QU 位解析不对: %+v", q)
	}
}
```

测试中用到辅助 `packLabels`，实现里提供（测试可直接调用）。

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/mdns -run Test -v`
Expected: FAIL —— 包内没有这些类型

- [ ] **Step 3: 实现 `internal/mdns/wire.go`**

```go
// Package mdns 实现 lanbox 需要的 mDNS 通告与浏览（仅标准库）。
package mdns

import (
	"encoding/binary"
	"errors"
	"sort"
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

func (q question) unicast() bool { return q.Class&classUnicastFlag != 0 }
func (q question) qclass() uint16 { return q.Class &^ classUnicastFlag }

type record struct {
	Name  string
	Type  uint16
	Class uint16 // 含 cache-flush 位
	TTL   uint32
	Data  []byte // 自包含 rdata（PTR/SRV 里的名字已解压后重新编码为不压缩形式）
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
	for _, dst := range []*[]record{&m.Answers, &m.Authorities, &m.Additionals} {
		n := an
		switch dst {
		case &m.Authorities:
			n = ns
		case &m.Additionals:
			n = ar
		}
		for i := 0; i < n; i++ {
			rr, next, err := parseRecord(raw, off)
			if err != nil {
				return nil, err
			}
			*dst = append(*dst, rr)
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
	next := off
	jumps := 0
	for {
		if off < 0 || off >= len(msg) {
			return "", 0, errMalformed
		}
		l := int(msg[off])
		switch {
		case l == 0:
			if next == off {
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
			if next == off {
				next = off + 2
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
	out := make([]byte, 6, 6+len(target)+2)
	binary.BigEndian.PutUint16(out[4:6], port)
	name, err := packName(nil, target)
	if err != nil {
		return nil
	}
	return append(out, name...)
}

func rdataA(ip []byte) []byte {
	if v4 := ipTo4(ip); v4 != nil {
		return v4
	}
	return nil
}

func rdataAAAA(ip []byte) []byte {
	if ip == nil {
		return nil
	}
	out := make([]byte, 16)
	copy(out, ip)
	return out
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

func ipTo4(ip []byte) []byte {
	if len(ip) == 4 {
		return append([]byte(nil), ip...)
	}
	if len(ip) == 16 && isV4Mapped(ip) {
		return append([]byte(nil), ip[12:]...)
	}
	return nil
}

func isV4Mapped(ip []byte) bool {
	for i := 0; i < 10; i++ {
		if ip[i] != 0 {
			return false
		}
	}
	return ip[10] == 0xff && ip[11] == 0xff
}

// recordKey 用于已知答案抑制与冲突比较。
func recordKey(rr record) string {
	return strings.ToLower(rr.Name) + "|" + string(rune(rr.Type)) + "|" + string(rr.Data)
}
```

注意：`packLabels` 上面的实现有笔误（去掉了首字节长度），请实现为**返回 `"local"` 编码后的完整 label 序列（含长度前缀、不含结尾 0）**：

```go
func packLabels(name string) []byte {
	out, err := packName(nil, name)
	if err != nil || len(out) == 0 {
		return nil
	}
	return out[:len(out)-1] // 去掉结尾的 0
}
```

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/mdns -count=1`
Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add internal/mdns/wire.go internal/mdns/wire_test.go
git commit -m "feat(mdns): DNS 报文编解码（A/AAAA/PTR/SRV/TXT、名字压缩）"
```

---

## Task 5: mDNS 通告器（探测/改名/播报/应答/bye）

**Files:**
- Create: `internal/mdns/responder.go`
- Test: `internal/mdns/responder_test.go`

- [ ] **Step 1: 写失败测试**

```go
package mdns

import (
	"io"
	"log"
	"net"
	"os"
	"sync"
	"testing"
	"time"
)

// fakeTransport 是测试用的报文通道。
type fakeTransport struct {
	mu       sync.Mutex
	addr     *net.UDPAddr
	inbox    [][]byte
	sent     [][]byte
	deadline time.Time
}

func (f *fakeTransport) push(raw []byte) {
	f.mu.Lock()
	f.inbox = append(f.inbox, raw)
	f.mu.Unlock()
}

func (f *fakeTransport) recv(b []byte) (int, net.Addr, error) {
	f.mu.Lock()
	if len(f.inbox) > 0 {
		raw := f.inbox[0]
		f.inbox = f.inbox[1:]
		f.mu.Unlock()
		return copy(b, raw), f.addr, nil
	}
	dl := f.deadline
	f.mu.Unlock()
	if !dl.IsZero() {
		if d := time.Until(dl); d > 0 {
			time.Sleep(d)
		}
	} else {
		time.Sleep(time.Millisecond)
	}
	return 0, nil, os.ErrDeadlineExceeded
}

func (f *fakeTransport) send(b []byte, addr net.Addr) (int, error) {
	f.mu.Lock()
	f.sent = append(f.sent, append([]byte(nil), b...))
	f.mu.Unlock()
	return len(b), nil
}

func (f *fakeTransport) setDeadline(t time.Time) error {
	f.mu.Lock()
	f.deadline = t
	f.mu.Unlock()
	return nil
}

func (f *fakeTransport) localAddrs() []net.IP { return []net.IP{net.IPv4(192, 168, 1, 9)} }
func (f *fakeTransport) rescan() error        { return nil }
func (f *fakeTransport) close() error         { return nil }

func (f *fakeTransport) sentMessages(t *testing.T) []*message {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []*message
	for _, raw := range f.sent {
		m, err := Parse(raw)
		if err != nil {
			t.Fatalf("发出的报文无法解析: %v", err)
		}
		out = append(out, m)
	}
	return out
}

func newTestResponder(t *testing.T, ft *fakeTransport) *Responder {
	t.Helper()
	r := &Responder{
		newTransport: func([]net.IP) (transport, error) { return ft, nil },
		cfg:          responderConfig{ttl: 120, probeGap: 2 * time.Millisecond, announceGap: time.Millisecond, rescanEvery: time.Hour, recvTimeout: 5 * time.Millisecond},
		logger:       log.New(io.Discard, "", 0),
	}
	return r
}

func testRecords() Records {
	return Records{
		Instance: "lanbox",
		Host:     "lanbox",
		Port:     8443,
		TXT:      map[string]string{"v": "1", "fp": "deadbeef"},
	}
}

func TestResponderAnnouncesAndAnswersPTR(t *testing.T) {
	ft := &fakeTransport{addr: &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 5353}}
	r := newTestResponder(t, ft)
	if err := r.start(testRecords()); err != nil {
		t.Fatalf("启动失败: %v", err)
	}
	defer r.Close()

	if st := r.Status(); !st.Running || st.Host != "lanbox" || st.Instance != "lanbox" {
		t.Fatalf("状态不对: %+v", st)
	}

	// 等首轮通告
	time.Sleep(20 * time.Millisecond)
	if len(ft.sentMessages(t)) == 0 {
		t.Fatal("应至少发出一次通告")
	}

	// 组播 PTR 查询应得到 PTR+SRV+TXT+A 应答
	q := &message{Questions: []question{{Name: serviceType, Type: typePTR, Class: classINET}}}
	raw, err := q.Pack()
	if err != nil {
		t.Fatal(err)
	}
	ft.push(raw)
	time.Sleep(40 * time.Millisecond)

	found := false
	for _, m := range ft.sentMessages(t) {
		if !m.Response {
			continue
		}
		var types []uint16
		for _, rr := range m.Answers {
			types = append(types, rr.Type)
		}
		if len(types) >= 4 {
			found = true
		}
	}
	if !found {
		t.Fatal("PTR 查询应得到包含 4 条以上记录的应答")
	}
}

func TestResponderLegacyUnicast(t *testing.T) {
	ft := &fakeTransport{addr: &net.UDPAddr{IP: net.IPv4(10, 1, 2, 3), Port: 40000}}
	r := newTestResponder(t, ft)
	if err := r.start(testRecords()); err != nil {
		t.Fatal(err)
	}
	defer r.Close()

	q := &message{ID: 0x1234, Questions: []question{{Name: serviceType, Type: typePTR, Class: classINET}}}
	raw, _ := q.Pack()
	ft.push(raw)
	time.Sleep(40 * time.Millisecond)

	ft.mu.Lock()
	last := ft.sent[len(ft.sent)-1]
	ft.mu.Unlock()
	m, err := Parse(last)
	if err != nil {
		t.Fatal(err)
	}
	if m.ID != 0x1234 {
		t.Fatalf("单播应答应回填查询 ID，得到 %#x", m.ID)
	}
}

func TestResponderConflictRenamesHost(t *testing.T) {
	ft := &fakeTransport{addr: &net.UDPAddr{IP: net.IPv4(10, 9, 9, 9), Port: 5353}}
	// 预置一条冲突通告：同名 lanbox.local 但 IP 不同
	conflict := &message{Response: true, Answers: []record{{
		Name: "lanbox.local", Type: typeA, Class: classINET, TTL: 120,
		Data: net.IPv4(10, 9, 9, 9).To4(),
	}}}
	raw, _ := conflict.Pack()
	ft.push(raw)
	ft.push(raw)
	ft.push(raw)
	ft.push(raw)
	ft.push(raw)

	r := newTestResponder(t, ft)
	if err := r.start(testRecords()); err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if host := r.Status().Host; host != "lanbox-2" {
		t.Fatalf("冲突后应改名为 lanbox-2，得到 %q", host)
	}
}

func TestResponderCloseSendsGoodbye(t *testing.T) {
	ft := &fakeTransport{addr: &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 5353}}
	r := newTestResponder(t, ft)
	if err := r.start(testRecords()); err != nil {
		t.Fatal(err)
	}
	r.Close()
	sawZeroTTL := false
	for _, m := range ft.sentMessages(t) {
		for _, rr := range m.Answers {
			if rr.TTL == 0 {
				sawZeroTTL = true
			}
		}
	}
	if !sawZeroTTL {
		t.Fatal("关闭时应发 TTL=0 的 goodbye")
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/mdns -run TestResponder -v`
Expected: FAIL —— `undefined: Records` / `transport`

- [ ] **Step 3: 实现 `internal/mdns/responder.go`**

```go
package mdns

import (
	"context"
	"errors"
	"log"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"
)

const serviceType = "_lanbox._tcp.local"

var multicastIPv4 = &net.UDPAddr{IP: net.IPv4(224, 0, 0, 251), Port: 5353}

// Records 是一条 lanbox 服务的通告内容。
type Records struct {
	Instance string
	Host     string
	Port     int
	TXT      map[string]string
	Addrs    []net.IP
}

// Status 是通告器状态快照。
type Status struct {
	Running  bool
	Instance string
	Host     string
	Addrs    []net.IP
	Err      string
}

type names struct {
	instance string // 不含 ._lanbox._tcp.local
	host     string // 不含 .local
}

func (n names) instanceFQDN() string { return n.instance + "." + serviceType }
func (n names) hostFQDN() string     { return n.host + ".local" }

type responderConfig struct {
	ttl         uint32
	probeGap    time.Duration
	announceGap time.Duration
	rescanEvery time.Duration
	recvTimeout time.Duration
}

func defaultResponderConfig() responderConfig {
	return responderConfig{
		ttl:         120,
		probeGap:    250 * time.Millisecond,
		announceGap: time.Second,
		rescanEvery: 30 * time.Second,
		recvTimeout: 500 * time.Millisecond,
	}
}

// transport 是报文通道抽象，测试用假实现。
type transport interface {
	recv(b []byte) (int, net.Addr, error)
	send(b []byte, addr net.Addr) (int, error)
	setDeadline(t time.Time) error
	localAddrs() []net.IP
	rescan() error
	close() error
}

// Responder 是一个 mDNS 通告器。
type Responder struct {
	newTransport func(addrs []net.IP) (transport, error)
	cfg          responderConfig
	logger       *log.Logger

	mu     sync.Mutex
	t      transport
	cancel context.CancelFunc
	names  names
	rec    Records
	status Status
	wg     sync.WaitGroup
}

// Start 创建并启动通告器；端口 5353 不可用时返回 error（调用方降级）。
func Start(rec Records) (*Responder, error) {
	r := &Responder{newTransport: newTransport, cfg: defaultResponderConfig(), logger: log.Default()}
	if err := r.start(rec); err != nil {
		return nil, err
	}
	return r, nil
}

func (r *Responder) start(rec Records) error {
	if rec.Host == "" {
		return errors.New("mDNS 主机名不能为空")
	}
	if rec.Instance == "" {
		rec.Instance = rec.Host
	}
	t, err := r.newTransport(rec.Addrs)
	if err != nil {
		return err
	}
	n, err := r.probe(t, rec)
	if err != nil {
		_ = t.close()
		return err
	}

	ctx, cancel := context.WithCancel(context.Background())
	r.mu.Lock()
	r.t = t
	r.cancel = cancel
	r.names = n
	r.rec = rec
	r.status = Status{Running: true, Instance: n.instance, Host: n.host, Addrs: t.localAddrs()}
	r.mu.Unlock()

	r.wg.Add(3)
	go r.readLoop(ctx, t)
	go r.beatLoop(ctx, t)
	go func() { // 第二次播报（间隔 announceGap）
		defer r.wg.Done()
		select {
		case <-ctx.Done():
		case <-time.After(r.cfg.announceGap):
			r.announce(t)
		}
	}()
	r.announce(t)
	return nil
}

// Update 用新的记录重启通告（停旧 → 重新探测 → 播报）。
func (r *Responder) Update(rec Records) error {
	r.stop()
	return r.start(rec)
}

// Status 返回当前状态。
func (r *Responder) Status() Status {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.status
}

// Close 发送 goodbye 并关闭。
func (r *Responder) Close() error {
	r.stop()
	return nil
}

func (r *Responder) stop() {
	r.mu.Lock()
	t, cancel := r.t, r.cancel
	r.t, r.cancel = nil, nil
	r.status.Running = false
	r.mu.Unlock()
	if t == nil {
		return
	}
	if cancel != nil {
		cancel()
	}
	if raw, err := r.bye(t).Pack(); err == nil {
		_, _ = t.send(raw, multicastIPv4)
	}
	r.wg.Wait()
	_ = t.close()
}

func (r *Responder) probe(t transport, rec Records) (names, error) {
	host := rec.Host
	instance := rec.Instance
	for attempt := 0; attempt < 5; attempt++ {
		n := names{instance: instance, host: host}
		conflict := r.probeOnce(t, n, rec)
		if !conflict {
			return n, nil
		}
		host = rec.Host + "-" + strconv.Itoa(attempt+2)
		if rec.Instance == rec.Host {
			instance = host
		}
	}
	return names{}, errors.New("mDNS 名称冲突：找不到可用的主机名")
}

func (r *Responder) probeOnce(t transport, n names, rec Records) bool {
	auth := buildRecords(n, rec, t.localAddrs(), 0)
	var qs []question
	for _, rr := range auth {
		qs = append(qs, question{Name: rr.Name, Type: typeANY, Class: classINET})
	}
	raw, err := (&message{Questions: qs, Authorities: auth}).Pack()
	if err != nil {
		return false
	}
	for i := 0; i < 3; i++ {
		if _, err := t.send(raw, multicastIPv4); err != nil {
			return false
		}
		deadline := time.Now().Add(r.cfg.probeGap)
		for time.Now().Before(deadline) {
			_ = t.setDeadline(deadline)
			buf := make([]byte, maxPacket)
			k, _, err := t.recv(buf)
			if err != nil {
				break
			}
			if r.conflicts(buf[:k], n, rec, t.localAddrs()) {
				return true
			}
		}
	}
	return false
}

func (r *Responder) conflicts(raw []byte, n names, rec Records, addrs []net.IP) bool {
	m, err := Parse(raw)
	if err != nil || !m.Response {
		return false
	}
	want := map[string]bool{}
	for _, rr := range buildRecords(n, rec, addrs, 0) {
		want[recordKey(rr)] = true
	}
	for _, rr := range m.Answers {
		if !strings.EqualFold(rr.Name, n.hostFQDN()) && !strings.EqualFold(rr.Name, n.instanceFQDN()) {
			continue
		}
		if rr.Type != typeA && rr.Type != typeAAAA && rr.Type != typeSRV && rr.Type != typeTXT {
			continue
		}
		if !want[recordKey(rr)] {
			return true
		}
	}
	return false
}

func (r *Responder) localAddrs() []net.IP {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.t == nil {
		return nil
	}
	return r.t.localAddrs()
}

func (r *Responder) readLoop(ctx context.Context, t transport) {
	defer r.wg.Done()
	buf := make([]byte, maxPacket)
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		_ = t.setDeadline(time.Now().Add(r.cfg.recvTimeout))
		k, addr, err := t.recv(buf)
		if err != nil {
			continue
		}
		r.handle(t, buf[:k], addr)
	}
}

func (r *Responder) handle(t transport, raw []byte, addr net.Addr) {
	m, err := Parse(raw)
	if err != nil || m.Response || len(m.Questions) == 0 {
		return
	}
	ans := r.answer(m)
	if ans == nil {
		return
	}
	if isLegacy(addr) || hasUnicastQuestion(m.Questions) {
		ans.ID = m.ID
	}
	out, err := ans.Pack()
	if err != nil {
		return
	}
	dest := net.Addr(multicastIPv4)
	if isLegacy(addr) || hasUnicastQuestion(m.Questions) {
		dest = addr
	}
	_, _ = t.send(out, dest)
}

func isLegacy(addr net.Addr) bool {
	ua, ok := addr.(*net.UDPAddr)
	return !ok || ua.Port != 5353
}

func hasUnicastQuestion(qs []question) bool {
	for _, q := range qs {
		if q.unicast() {
			return true
		}
	}
	return false
}

func (r *Responder) answer(m *message) *message {
	r.mu.Lock()
	n, rec := r.names, r.rec
	t := r.t
	r.mu.Unlock()
	var addrs []net.IP
	if t != nil {
		addrs = t.localAddrs()
	}
	all := buildRecords(n, rec, addrs, r.cfg.ttl)
	known := map[string]bool{}
	for _, rr := range m.Answers {
		if rr.TTL >= r.cfg.ttl/2 {
			known[recordKey(rr)] = true
		}
	}
	var out []record
	for _, q := range m.Questions {
		for _, rr := range all {
			if !strings.EqualFold(rr.Name, q.Name) {
				continue
			}
			if q.Type != typeANY && rr.Type != q.Type {
				continue
			}
			if known[recordKey(rr)] {
				continue
			}
			out = append(out, rr)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return &message{Response: true, Authoritative: true, Answers: out}
}

func (r *Responder) announce(t transport) {
	r.mu.Lock()
	n, rec := r.names, r.rec
	r.mu.Unlock()
	msg := &message{Response: true, Authoritative: true, Answers: buildRecords(n, rec, t.localAddrs(), r.cfg.ttl)}
	raw, err := msg.Pack()
	if err != nil {
		return
	}
	_, _ = t.send(raw, multicastIPv4)
}

func (r *Responder) bye(t transport) *message {
	r.mu.Lock()
	n, rec := r.names, r.rec
	r.mu.Unlock()
	return &message{Response: true, Authoritative: true, Answers: buildRecords(n, rec, t.localAddrs(), 0)}
}

func (r *Responder) beatLoop(ctx context.Context, t transport) {
	defer r.wg.Done()
	announceEvery := time.Duration(r.cfg.ttl/2) * time.Second
	announceTick := time.NewTicker(announceEvery)
	defer announceTick.Stop()
	rescanTick := time.NewTicker(r.cfg.rescanEvery)
	defer rescanTick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-announceTick.C:
			r.announce(t)
		case <-rescanTick.C:
			if err := t.rescan(); err == nil {
				r.announce(t)
			}
		}
	}
}

// buildRecords 是纯函数，便于测试。
func buildRecords(n names, rec Records, addrs []net.IP, ttl uint32) []record {
	out := []record{
		{Name: serviceType, Type: typePTR, Class: classINET, TTL: ttl, Data: mustPTR(n.instanceFQDN())},
		{Name: n.instanceFQDN(), Type: typeSRV, Class: classINET | cacheFlushFlag, TTL: ttl, Data: mustSRV(uint16(rec.Port), n.hostFQDN())},
		{Name: n.instanceFQDN(), Type: typeTXT, Class: classINET | cacheFlushFlag, TTL: ttl, Data: rdataTXT(rec.TXT)},
		{Name: "_services._dns-sd._udp.local", Type: typePTR, Class: classINET, TTL: ttl, Data: mustPTR(serviceType)},
	}
	for _, ip := range addrs {
		if v4 := ip.To4(); v4 != nil {
			out = append(out, record{Name: n.hostFQDN(), Type: typeA, Class: classINET | cacheFlushFlag, TTL: ttl, Data: append([]byte(nil), v4...)})
			continue
		}
		if v6 := ip.To16(); v6 != nil {
			out = append(out, record{Name: n.hostFQDN(), Type: typeAAAA, Class: classINET | cacheFlushFlag, TTL: ttl, Data: append([]byte(nil), v6...)})
		}
	}
	return out
}
```

（`recordKey` 已在 Task 4 的 `wire.go` 中定义，本文件直接使用，不要重复定义。）

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/mdns -count=1 -race`
Expected: PASS（`-race` 用于提前发现通告器并发问题）

- [ ] **Step 5: 提交**

```bash
git add internal/mdns/responder.go internal/mdns/responder_test.go
git commit -m "feat(mdns): 通告器（探测、冲突改名、播报、应答、goodbye）"
```

---

## Task 6: mDNS 浏览（CLI 用）

**Files:**
- Create: `internal/mdns/browse.go`
- Test: `internal/mdns/browse_test.go`

- [ ] **Step 1: 写失败测试**

```go
package mdns

import (
	"context"
	"net"
	"sort"
	"testing"
	"time"
)

func TestBrowseStateConsume(t *testing.T) {
	st := newBrowseState()
	ptr := &message{Response: true, Answers: []record{{
		Name: serviceType, Type: typePTR, Class: classINET, TTL: 120,
		Data: mustPTR("office." + serviceType),
	}}}
	if pending := st.consume(ptr); len(pending) != 1 || pending[0] != "office" {
		t.Fatalf("应产生 office 待查询，得到 %+v", pending)
	}
	srv := &message{Response: true, Answers: []record{
		{Name: "office." + serviceType, Type: typeSRV, Class: classINET, TTL: 120, Data: mustSRV(9443, "nas.local")},
		{Name: "office." + serviceType, Type: typeTXT, Class: classINET, TTL: 120, Data: rdataTXT(map[string]string{"v": "1", "fp": "abc123"})},
		{Name: "nas.local", Type: typeA, Class: classINET, TTL: 120, Data: net.IPv4(192, 168, 1, 9).To4()},
	}}
	st.consume(srv)

	services := st.services()
	if len(services) != 1 {
		t.Fatalf("应聚合出 1 个服务，得到 %+v", services)
	}
	s := services[0]
	if s.Instance != "office" || s.Host != "nas" || s.Port != 9443 || s.TXT["fp"] != "abc123" {
		t.Fatalf("服务字段不对: %+v", s)
	}
	if len(s.Addrs) != 1 || s.Addrs[0].String() != "192.168.1.9" {
		t.Fatalf("地址聚合不对: %+v", s.Addrs)
	}
}

func TestBrowseWithFakeTransport(t *testing.T) {
	ft := &fakeTransport{addr: &net.UDPAddr{IP: net.IPv4(192, 168, 1, 9), Port: 5353}}
	resp := &message{Response: true, Answers: []record{
		{Name: serviceType, Type: typePTR, Class: classINET, TTL: 120, Data: mustPTR("office." + serviceType)},
		{Name: "office." + serviceType, Type: typeSRV, Class: classINET, TTL: 120, Data: mustSRV(8443, "lanbox.local")},
		{Name: "lanbox.local", Type: typeAAAA, Class: classINET, TTL: 120, Data: net.ParseIP("fe80::1").To16()},
		{Name: "lanbox.local", Type: typeA, Class: classINET, TTL: 120, Data: net.IPv4(192, 168, 1, 9).To4()},
	}}
	raw, _ := resp.Pack()
	ft.push(raw)

	got, err := browseWith(context.Background(), ft, 60*time.Millisecond)
	if err != nil {
		t.Fatalf("浏览失败: %v", err)
	}
	if len(got) != 1 || got[0].Instance != "office" || got[0].Port != 8443 {
		t.Fatalf("结果不对: %+v", got)
	}
	addrs := make([]string, 0, len(got[0].Addrs))
	for _, ip := range got[0].Addrs {
		addrs = append(addrs, ip.String())
	}
	sort.Strings(addrs)
	if len(addrs) != 2 {
		t.Fatalf("应保留 A 与 AAAA 两个地址: %v", addrs)
	}
}

func TestBrowseTimeoutsWithoutTraffic(t *testing.T) {
	ft := &fakeTransport{addr: &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 5353}}
	got, err := browseWith(context.Background(), ft, 20*time.Millisecond)
	if err != nil || len(got) != 0 {
		t.Fatalf("无响应时应返回空结果，得到 %+v %v", got, err)
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/mdns -run TestBrowse -v`
Expected: FAIL —— `undefined: newBrowseState`

- [ ] **Step 3: 实现 `internal/mdns/browse.go`**

```go
package mdns

import (
	"context"
	"net"
	"sort"
	"strings"
	"time"
)

// Service 是一个被发现的 lanbox 实例。
type Service struct {
	Instance string            `json:"instance"`
	Host     string            `json:"host"`
	Addrs    []net.IP          `json:"addrs"`
	Port     int               `json:"port"`
	TXT      map[string]string `json:"txt"`
}

// Browse 在局域网内查找 lanbox 实例。
func Browse(ctx context.Context, timeout time.Duration) ([]Service, error) {
	t, err := newTransport(0, nil)
	if err != nil {
		return nil, err
	}
	defer t.close()
	return browseWith(ctx, t, timeout)
}

func browseWith(ctx context.Context, t transport, timeout time.Duration) ([]Service, error) {
	deadline := time.Now().Add(timeout)
	st := newBrowseState()
	ptrQuery, err := (&message{Questions: []question{{Name: serviceType, Type: typePTR, Class: classINET}}}).Pack()
	if err != nil {
		return nil, err
	}
	nextQuery := time.Now()
	buf := make([]byte, maxPacket)
	for {
		if ctx.Err() != nil || time.Now().After(deadline) {
			break
		}
		if !time.Now().Before(nextQuery) {
			_, _ = t.send(ptrQuery, multicastIPv4)
			nextQuery = time.Now().Add(time.Second)
		}
		wait := time.Until(deadline)
		if d := time.Until(nextQuery); d < wait {
			wait = d
		}
		if wait <= 0 {
			continue
		}
		_ = t.setDeadline(time.Now().Add(wait))
		n, _, err := t.recv(buf)
		if err != nil {
			continue
		}
		m, err := Parse(buf[:n])
		if err != nil || !m.Response {
			continue
		}
		for _, inst := range st.consume(m) {
			raw, err := (&message{Questions: []question{{
				Name: inst + "." + serviceType, Type: typeANY, Class: classINET,
			}}}).Pack()
			if err == nil {
				_, _ = t.send(raw, multicastIPv4)
			}
		}
	}
	return st.services(), nil
}

type browseState struct {
	instances map[string]*Service
	asked     map[string]bool
	hostAddrs map[string][]net.IP
}

func newBrowseState() *browseState {
	return &browseState{instances: map[string]*Service{}, asked: map[string]bool{}, hostAddrs: map[string][]net.IP{}}
}

// consume 吸收一条响应，返回需要补查的实例名。
func (s *browseState) consume(m *message) []string {
	var pending []string
	for _, rr := range m.Answers {
		name := strings.ToLower(rr.Name)
		switch rr.Type {
		case typePTR:
			if name != serviceType {
				continue
			}
			target, err := parsePTR(rr.Data)
			if err != nil {
				continue
			}
			inst := trimServiceSuffix(target)
			if inst == "" {
				continue
			}
			if s.instances[inst] == nil {
				s.instances[inst] = &Service{Instance: inst, Addrs: []net.IP{}, TXT: map[string]string{}}
			}
			if !s.asked[inst] {
				s.asked[inst] = true
				pending = append(pending, inst)
			}
		case typeSRV:
			inst := trimServiceSuffix(rr.Name)
			if inst == "" {
				continue
			}
			svc := s.instances[inst]
			if svc == nil {
				svc = &Service{Instance: inst, Addrs: []net.IP{}, TXT: map[string]string{}}
				s.instances[inst] = svc
			}
			port, target, err := parseSRV(rr.Data)
			if err != nil {
				continue
			}
			svc.Port = int(port)
			svc.Host = strings.TrimSuffix(strings.ToLower(target), ".local")
		case typeTXT:
			inst := trimServiceSuffix(rr.Name)
			if inst == "" {
				continue
			}
			if svc := s.instances[inst]; svc != nil {
				svc.TXT = parseTXT(rr.Data)
			}
		case typeA, typeAAAA:
			host := strings.TrimSuffix(name, ".local")
			if len(rr.Data) == 4 {
				s.hostAddrs[host] = appendUniqueIP(s.hostAddrs[host], net.IP(append([]byte(nil), rr.Data...)))
			} else if len(rr.Data) == 16 {
				s.hostAddrs[host] = appendUniqueIP(s.hostAddrs[host], net.IP(append([]byte(nil), rr.Data...)))
			}
		}
	}
	return pending
}

func (s *browseState) services() []Service {
	out := make([]Service, 0, len(s.instances))
	for _, svc := range s.instances {
		svc.Addrs = append([]net.IP(nil), s.hostAddrs[strings.ToLower(svc.Host)]...)
		sort.Slice(svc.Addrs, func(i, j int) bool { return svc.Addrs[i].String() < svc.Addrs[j].String() })
		if svc.TXT == nil {
			svc.TXT = map[string]string{}
		}
		out = append(out, *svc)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Instance < out[j].Instance })
	return out
}

func trimServiceSuffix(name string) string {
	name = strings.TrimSuffix(strings.ToLower(name), ".")
	if !strings.HasSuffix(name, "."+serviceType) {
		return ""
	}
	return strings.TrimSuffix(name, "."+serviceType)
}

func appendUniqueIP(list []net.IP, ip net.IP) []net.IP {
	for _, have := range list {
		if have.Equal(ip) {
			return list
		}
	}
	return append(list, ip)
}
```

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/mdns -count=1`
Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add internal/mdns/browse.go internal/mdns/browse_test.go
git commit -m "feat(mdns): 浏览 lanbox 实例并聚合 SRV/TXT/A/AAAA"
```

---

## Task 7: 套接字实现（Linux + 非 Linux 回落）

**Files:**
- Create: `internal/mdns/sock_linux.go`
- Create: `internal/mdns/sock_other.go`
- Test: 无单测（人工验收），但必须通过 `go vet` 与 `go build`

- [ ] **Step 1: 实现 `internal/mdns/sock_linux.go`**

```go
//go:build linux

package mdns

import (
	"context"
	"errors"
	"net"
	"sync"
	"syscall"
	"time"
)

type linuxTransport struct {
	mu     sync.Mutex
	conn   *net.UDPConn
	ifaces []net.Interface
	addrs  []net.IP
	joined map[int]bool
}

// newTransport 绑定 port（0 表示随机端口），并加入所有合格网卡的组播组。
// 优先用 REUSEADDR+REUSEPORT；若与只设 REUSEADDR 的进程（如 avahi）冲突，则回落为只设 REUSEADDR。
func newTransport(port int, addrs []net.IP) (transport, error) {
	addr := "0.0.0.0:0"
	if port > 0 {
		addr = "0.0.0.0:" + itoa(port)
	}
	pc, err := listenUDP(addr, true)
	if err != nil {
		pc, err = listenUDP(addr, false)
	}
	if err != nil {
		return nil, err
	}
	t := &linuxTransport{conn: pc, joined: map[int]bool{}}
	if err := t.rescan(); err != nil {
		_ = t.conn.Close()
		return nil, err
	}
	if port > 0 {
		_ = t.conn.SetMulticastTTL(255)
		_ = t.conn.SetMulticastLoopback(true)
	}
	return t, nil
}

func listenUDP(addr string, reusePort bool) (*net.UDPConn, error) {
	lc := net.ListenConfig{Control: func(network, address string, c syscall.RawConn) error {
		var serr error
		if err := c.Control(func(fd uintptr) {
			if serr = syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_REUSEADDR, 1); serr != nil {
				return
			}
			if reusePort {
				serr = syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_REUSEPORT, 1)
			}
		}); err != nil {
			return err
		}
		return serr
	}}
	pc, err := lc.ListenPacket(context.Background(), "udp4", addr)
	if err != nil {
		return nil, err
	}
	return pc.(*net.UDPConn), nil
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [6]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

func (t *linuxTransport) rescan() error {
	ifaces, err := net.Interfaces()
	if err != nil {
		return err
	}
	var usable []net.Interface
	var addrs []net.IP
	for _, ifi := range ifaces {
		if ifi.Flags&net.FlagUp == 0 || ifi.Flags&net.FlagMulticast == 0 || ifi.Flags&net.FlagLoopback != 0 {
			continue
		}
		usable = append(usable, ifi)
		ifa, err := ifi.Addrs()
		if err != nil {
			continue
		}
		for _, a := range ifa {
			if ipn, ok := a.(*net.IPNet); ok && ipn.IP.To16() != nil {
				addrs = append(addrs, ipn.IP)
			}
		}
	}
	t.mu.Lock()
	t.ifaces = usable
	t.addrs = addrs
	for _, ifi := range usable {
		if t.joined[ifi.Index] {
			continue
		}
		if t.join(ifi.Index, ifi) == nil {
			t.joined[ifi.Index] = true
		}
	}
	t.mu.Unlock()
	return nil
}

func (t *linuxTransport) join(idx int, ifi net.Interface) error {
	raw, err := t.conn.SyscallConn()
	if err != nil {
		return err
	}
	var serr error
	if err := raw.Control(func(fd uintptr) {
		mreq := &syscall.IPMreqn{Ifindex: int32(idx)}
		copy(mreq.Multiaddr[:], []byte{224, 0, 0, 251})
		serr = syscall.SetsockoptIPMreqn(int(fd), syscall.IPPROTO_IP, syscall.IP_ADD_MEMBERSHIP, mreq)
	}); err != nil {
		return err
	}
	return serr
}

func (t *linuxTransport) recv(b []byte) (int, net.Addr, error) {
	return t.conn.ReadFromUDP(b)
}

func (t *linuxTransport) send(b []byte, addr net.Addr) (int, error) {
	ua, ok := addr.(*net.UDPAddr)
	if !ok {
		return 0, errors.New("mDNS 只支持 UDP 地址")
	}
	if !ua.IP.IsMulticast() {
		return t.conn.WriteToUDP(b, ua)
	}
	t.mu.Lock()
	ifaces := append([]net.Interface(nil), t.ifaces...)
	t.mu.Unlock()
	if len(ifaces) == 0 {
		return t.conn.WriteToUDP(b, ua)
	}
	n := 0
	for i := range ifaces {
		if err := t.conn.SetMulticastInterface(&ifaces[i]); err != nil {
			continue
		}
		if k, err := t.conn.WriteToUDP(b, ua); err == nil {
			n = k
		}
	}
	return n, nil
}

func (t *linuxTransport) setDeadline(d time.Time) error { return t.conn.SetReadDeadline(d) }

func (t *linuxTransport) localAddrs() []net.IP {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]net.IP(nil), t.addrs...)
}

func (t *linuxTransport) close() error {
	t.mu.Lock()
	conn := t.conn
	t.conn = nil
	t.mu.Unlock()
	if conn == nil {
		return nil
	}
	return conn.Close()
}
```

- [ ] **Step 2: 实现 `internal/mdns/sock_other.go`**

```go
//go:build !linux

package mdns

import (
	"net"
	"time"
)

type udpTransport struct {
	conn *net.UDPConn
}

// newTransport 在非 Linux 上用标准库的组播绑定（仅默认网卡，供开发机使用）。
func newTransport(port int, addrs []net.IP) (transport, error) {
	target := &net.UDPAddr{IP: net.IPv4(224, 0, 0, 251), Port: port}
	if port == 0 {
		conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero, Port: 0})
		if err != nil {
			return nil, err
		}
		return &udpTransport{conn: conn}, nil
	}
	conn, err := net.ListenMulticastUDP("udp4", nil, target)
	if err != nil {
		return nil, err
	}
	return &udpTransport{conn: conn}, nil
}

func (t *udpTransport) recv(b []byte) (int, net.Addr, error) { return t.conn.ReadFromUDP(b) }
func (t *udpTransport) send(b []byte, addr net.Addr) (int, error) {
	ua, ok := addr.(*net.UDPAddr)
	if !ok {
		return 0, nil
	}
	return t.conn.WriteToUDP(b, ua)
}
func (t *udpTransport) setDeadline(d time.Time) error { return t.conn.SetReadDeadline(d) }
func (t *udpTransport) rescan() error                 { return nil }

func (t *udpTransport) localAddrs() []net.IP {
	ifa, err := net.InterfaceAddrs()
	if err != nil {
		return nil
	}
	var out []net.IP
	for _, a := range ifa {
		if ipn, ok := a.(*net.IPNet); ok {
			out = append(out, ipn.IP)
		}
	}
	return out
}

func (t *udpTransport) close() error { return t.conn.Close() }
```

- [ ] **Step 3: 编译与静态检查**

Run: `go build ./... && go vet ./... && gofmt -l internal/mdns`
Expected: 无输出（gofmt 列表为空），构建通过

- [ ] **Step 4: 提交**

```bash
git add internal/mdns/sock_linux.go internal/mdns/sock_other.go
git commit -m "feat(mdns): 5353 套接字（REUSEADDR/REUSEPORT、逐网卡加组与发送）"
```

---

## Task 8: `store`/`auth` 热更

**Files:**
- Modify: `internal/store/store.go`、`internal/store/files.go`、`internal/store/items.go`
- Modify: `internal/auth/auth.go`
- Test: `internal/store/store_test.go`、`internal/auth/auth_test.go`

- [ ] **Step 1: 写失败测试**

追加到 `internal/store/store_test.go`：

```go
func TestSetLimitsTakesEffectImmediately(t *testing.T) {
	s := newStore(t, Options{MaxTextBytes: 65536, MaxFiles: 500, MaxItemBytes: 1 << 30})
	if _, err := s.Create(CreateInput{Text: "0123456789"}); err != nil {
		t.Fatalf("默认限额下应成功: %v", err)
	}

	s.SetLimits(Options{MaxTextBytes: 5, MaxFileBytes: 1 << 20, MinFreeBytes: 0, MaxFiles: 2, MaxItemBytes: 1 << 20})
	if _, err := s.Create(CreateInput{Text: "0123456789"}); !errors.Is(err, ErrTextTooLong) {
		t.Fatalf("新上限应立即生效，得到 %v", err)
	}
	if s.MaxFiles() != 2 {
		t.Fatalf("MaxFiles 应返回新值，得到 %d", s.MaxFiles())
	}
}
```

追加到 `internal/auth/auth_test.go`：

```go
func TestSetSessionTTL(t *testing.T) {
	dir := t.TempDir()
	m, err := Open(filepath.Join(dir, "users.json"), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := m.BootstrapAdmin(); err != nil {
		t.Fatal(err)
	}

	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	m.now = func() time.Time { return base }
	tok, expires, err := m.NewSession("admin")
	if err != nil {
		t.Fatal(err)
	}
	if got := expires.Sub(base); got != time.Hour {
		t.Fatalf("默认 TTL 应为 1h，得到 %v", got)
	}

	m.SetSessionTTL(10 * time.Minute)
	later := base.Add(2 * time.Hour)
	m.now = func() time.Time { return later }
	if _, ok := m.Lookup(tok); ok {
		t.Fatal("两小时后旧会话应已过期")
	}
	tok2, expires2, err := m.NewSession("admin")
	if err != nil {
		t.Fatal(err)
	}
	if got := expires2.Sub(later); got != 10*time.Minute {
		t.Fatalf("新 TTL 应为 10m，得到 %v", got)
	}
	if _, ok := m.Lookup(tok2); !ok {
		t.Fatal("新会话应有效")
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/store ./internal/auth -run 'TestSetLimits|TestSetSessionTTL' -v`
Expected: FAIL —— `s.SetLimits` / `m.SetSessionTTL` 未定义

- [ ] **Step 3: 实现 store 限额快照**

`internal/store/store.go`：把 `Store` 的 `opts Options` 换成 `lim atomic.Pointer[limits]`（保留 `Options` 公开类型与 `Open` 签名），新增：

```go
type limits struct {
	maxFileBytes int64
	minFreeBytes int64
	maxTextBytes int
	maxFiles     int
	maxItemBytes int64
}

func limitsFrom(o Options) *limits {
	return &limits{
		maxFileBytes: o.MaxFileBytes,
		minFreeBytes: o.MinFreeBytes,
		maxTextBytes: o.MaxTextBytes,
		maxFiles:     o.MaxFiles,
		maxItemBytes: o.MaxItemBytes,
	}
}

// SetLimits 立即替换上传限额（热更），不影响已存在内容。
// FreeSpace 仍由 Open 时确定，热更不改变它。
func (s *Store) SetLimits(o Options) { s.lim.Store(limitsFrom(o)) }

func (s *Store) lims() *limits { return s.lim.Load() }
```

`Store` 结构体改为：

```go
type Store struct {
	itemsRoot string
	freeSpace func(path string) (int64, error)
	lim       atomic.Pointer[limits]
	mu        sync.RWMutex
	index     map[string]*Item
}
```

`Open` 里设置 `s.freeSpace` 与 `s.lim.Store(limitsFrom(opts))`。

把 `files.go`/`items.go` 里所有 `s.opts.X` 替换为 `s.lims().x`：

| 旧 | 新 |
|---|---|
| `s.opts.MaxFiles` | `s.lims().maxFiles` |
| `s.opts.MaxItemBytes` | `s.lims().maxItemBytes` |
| `s.opts.MinFreeBytes` | `s.lims().minFreeBytes` |
| `s.opts.MaxFileBytes` | `s.lims().maxFileBytes` |
| `s.opts.MaxTextBytes` | `s.lims().maxTextBytes` |
| `s.opts.FreeSpace(...)` | `s.freeSpace(...)` |

（`maxFiles()` / `maxItemBytes()` 方法保留原有回落默认值语义。）

- [ ] **Step 4: 实现 auth TTL 热更**

`internal/auth/auth.go` 追加：

```go
// SetSessionTTL 立即替换新会话与续期使用的有效期（热更）。
func (m *Manager) SetSessionTTL(d time.Duration) {
	m.mu.Lock()
	m.ttl = d
	m.mu.Unlock()
}
```

确认 `NewSession`、`Lookup` 对 `m.ttl` 的读取都在 `m.mu` 锁内（当前两处均在锁区间内，符合）。

- [ ] **Step 5: 运行测试确认通过**

Run: `go test ./internal/store ./internal/auth -count=1 -race`
Expected: PASS

- [ ] **Step 6: 提交**

```bash
git add internal/store internal/auth
git commit -m "feat(store,auth): 限额与会话 TTL 支持热更"
```

---

## Task 9: HTTP API（当前配置快照、`healthz`、配置读写、重启）

**Files:**
- Modify: `internal/httpapi/server.go`、`helpers.go`、`items_handlers.go`、`middleware.go`
- Create: `internal/httpapi/config_handlers.go`
- Test: `internal/httpapi/api_test.go`

- [ ] **Step 1: 改测试夹具并写失败测试**

`internal/httpapi/api_test.go` 的 `env` 增加字段 `restarts *int`，并把 `newEnv` 改成"总是带 `Live`"：

```go
type env struct {
	srv           *httptest.Server
	client        *http.Client
	clientNoJar   *http.Client
	store         *store.Store
	auth          *auth.Manager
	cfg           config.Config
	adminPassword string
	restarts      *int
}
```

`newEnv` 里，在创建 `store` 之前先准备配置文件与 `Live`，热更钩子要像 `main` 一样把限额写进 store：

```go
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	body := fmt.Sprintf(`{"data_dir":%q,"max_file_size_mb":8,"max_item_size_mb":16,"min_free_disk_mb":0,"max_files_per_item":%d}`,
		dir, testMaxFilesPerItem)
	if err := os.WriteFile(cfgPath, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	var st *store.Store
	restarts := 0
	live, err := config.OpenLive(cfgPath, config.Hooks{
		Apply: func(prev, next config.Config) error {
			if st != nil {
				st.SetLimits(store.Options{
					MaxFileBytes: next.MaxFileBytes(),
					MinFreeBytes: next.MinFreeBytes(),
					MaxTextBytes: next.MaxTextBytes,
					MaxFiles:     next.MaxFilesPerItem,
					MaxItemBytes: next.MaxItemBytes(),
				})
			}
			return nil
		},
		Restart: func() { restarts++ },
	})
	if err != nil {
		t.Fatalf("打开 Live 失败: %v", err)
	}
	cfg := live.Current()
```

随后 `st, err = store.Open(...)`（不要再用 `:=` 遮掉闭包里的 `st`），`httpapi.Options` 增加 `Live: live, Version: "test", StartedAt: time.Now()`，`env` 返回时带 `restarts: &restarts`。

追加测试：

```go
func TestHealthzIsPublic(t *testing.T) {
	e := newEnv(t)
	resp, body := e.doWith(t, e.clientNoJar, e.newJSONReq(t, http.MethodGet, "/api/healthz", nil))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("healthz 应公开可访问，得到 %d %s", resp.StatusCode, body)
	}
	var got struct {
		OK      bool   `json:"ok"`
		Name    string `json:"name"`
		Version string `json:"version"`
	}
	if err := json.Unmarshal(body, &got); err != nil || !got.OK || got.Name != "lanbox" || got.Version != "test" {
		t.Fatalf("healthz 响应不对: %s", body)
	}
	if resp.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("healthz 应 no-store，得到 %q", resp.Header.Get("Cache-Control"))
	}
}

func TestConfigRequiresAdmin(t *testing.T) {
	e := newEnv(t)
	e.loginAdmin(t)
	resp, body := e.do(t, e.newJSONReq(t, http.MethodPost, "/api/users", map[string]any{
		"username": "bob", "password": "password123", "is_admin": false,
	}))
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("创建普通用户失败: %d %s", resp.StatusCode, body)
	}
	e.do(t, e.newJSONReq(t, http.MethodPost, "/api/logout", nil))
	e.login(t, "bob", "password123")

	resp, body = e.do(t, e.newJSONReq(t, http.MethodGet, "/api/config", nil))
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("普通成员读配置应 403，得到 %d %s", resp.StatusCode, body)
	}
	resp, body = e.do(t, e.newJSONReq(t, http.MethodPut, "/api/config", map[string]any{
		"values": map[string]any{"max_text_bytes": 10},
	}))
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("普通成员写配置应 403，得到 %d %s", resp.StatusCode, body)
	}
	resp, body = e.do(t, e.newJSONReq(t, http.MethodPost, "/api/config/restart", nil))
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("普通成员重启应 403，得到 %d %s", resp.StatusCode, body)
	}
}

func TestPutConfigHotApplyAndRestart(t *testing.T) {
	e := newEnv(t)
	e.loginAdmin(t)

	resp, body := e.do(t, e.newJSONReq(t, http.MethodPut, "/api/config", map[string]any{
		"values": map[string]any{"max_text_bytes": 5, "listen": ":0"},
	}))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("保存应成功，得到 %d %s", resp.StatusCode, body)
	}
	var res struct {
		Changed         []string `json:"changed"`
		Applied         []string `json:"applied"`
		RestartRequired []string `json:"restart_required"`
	}
	if err := json.Unmarshal(body, &res); err != nil {
		t.Fatal(err)
	}
	if len(res.Applied) != 1 || res.Applied[0] != "max_text_bytes" {
		t.Fatalf("applied 不对: %+v", res.Applied)
	}
	if len(res.RestartRequired) != 1 || res.RestartRequired[0] != "listen" {
		t.Fatalf("restart_required 不对: %+v", res.RestartRequired)
	}

	// 热更已生效：store 立刻按新上限拒绝
	if _, err := e.store.Create(store.CreateInput{Text: "0123456789"}); !errors.Is(err, store.ErrTextTooLong) {
		t.Fatalf("max_text_bytes 未热更，得到 %v", err)
	}

	resp, body = e.do(t, e.newJSONReq(t, http.MethodGet, "/api/config", nil))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("读配置失败: %d %s", resp.StatusCode, body)
	}
	var got struct {
		Origins map[string]string `json:"origins"`
		Paths   map[string]string `json:"paths"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	if got.Origins["max_text_bytes"] != "web" || got.Paths["web_config"] == "" {
		t.Fatalf("来源/路径不对: %+v", got)
	}

	resp, body = e.do(t, e.newJSONReq(t, http.MethodPost, "/api/config/restart", nil))
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("重启应 202，得到 %d %s", resp.StatusCode, body)
	}
	if *e.restarts != 1 {
		t.Fatalf("Restart 钩子应调用一次，得到 %d", *e.restarts)
	}
	resp, body = e.do(t, e.newJSONReq(t, http.MethodPost, "/api/config/restart", nil))
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("冷却期内应 429，得到 %d %s", resp.StatusCode, body)
	}
}

func TestPutConfigRejectsInvalid(t *testing.T) {
	e := newEnv(t)
	e.loginAdmin(t)

	resp, body := e.do(t, e.newJSONReq(t, http.MethodPut, "/api/config", map[string]any{
		"values": map[string]any{"max_files_per_item": 0},
	}))
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("非法值应 400，得到 %d %s", resp.StatusCode, body)
	}

	resp, body = e.do(t, e.newJSONReq(t, http.MethodPut, "/api/config", map[string]any{
		"values": map[string]any{"no_such_field": 1},
	}))
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("未知字段应 400，得到 %d %s", resp.StatusCode, body)
	}
}
```

（`api_test.go` 需要补导入 `errors`、`fmt`、`os`、`time`、`lanbox/internal/store`；若已有则跳过。`listen:" :0"` 只用于验证"可绑定即通过"，不会真的重启。）

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/httpapi -run 'TestHealthz|TestConfig|TestPutConfig' -v`
Expected: FAIL —— `/api/healthz` 404、`/api/config` 404

- [ ] **Step 3: 实现**

`internal/httpapi/server.go`：`Options` 追加并改造 `Server`：

```go
type Options struct {
	Cfg        config.Config
	Store      *store.Store
	Auth       *auth.Manager
	Web        fs.FS
	Log        *log.Logger
	Live       *config.Live
	MDNSStatus func() MDNSStatus
	StartedAt  time.Time
	Version    string
}

type Server struct {
	cfgMu      sync.RWMutex
	cfg        config.Config
	store      *store.Store
	auth       *auth.Manager
	hub        *Hub
	web        fs.FS
	log        *log.Logger
	mux        *http.ServeMux
	live       *config.Live
	mdnsStatus func() MDNSStatus
	startedAt  time.Time
	version    string
}
```

`New` 里复制新字段（`StartedAt` 为零时用 `time.Now()`、`Version` 为空时用 `"dev"`）。新增：

```go
// SetConfig 替换当前配置快照（热更后由 main 调用）。
func (s *Server) SetConfig(c config.Config) {
	s.cfgMu.Lock()
	s.cfg = c
	s.cfgMu.Unlock()
}

func (s *Server) current() config.Config {
	s.cfgMu.RLock()
	defer s.cfgMu.RUnlock()
	return s.cfg
}
```

把 `helpers.go`、`items_handlers.go`、`middleware.go` 里所有 `s.cfg.` 改为 `s.current().`（共 8 处，见 grep），注意 `items_handlers.go` 里同一函数内多次读取时用局部变量避免重复加锁：

```go
	maxText := s.current().MaxTextBytes
```

`server.go` 需要补导入 `sync`、`time` 与 `lanbox/internal/config`（`config` 已导入）。`routes()` 追加：

```go
	s.mux.HandleFunc("GET /api/healthz", s.handleHealthz)
	s.mux.HandleFunc("GET /api/config", s.withAdmin(s.handleGetConfig))
	s.mux.HandleFunc("PUT /api/config", s.withAdmin(s.handlePutConfig))
	s.mux.HandleFunc("POST /api/config/restart", s.withAdmin(s.handleRestart))
```

在 `middleware.go` 的 `accessLog` 最前面加：

```go
		if r.URL.Path == "/api/healthz" {
			next.ServeHTTP(w, r)
			return
		}
```

新建 `internal/httpapi/config_handlers.go`：

```go
package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"lanbox/internal/config"
)

// MDNSStatus 是 mDNS 通告状态（由 main 适配，避免 httpapi 依赖 internal/mdns）。
type MDNSStatus struct {
	Running  bool     `json:"running"`
	Instance string   `json:"instance"`
	Host     string   `json:"host"`
	Port     int      `json:"port"`
	Addrs    []string `json:"addrs"`
	Error    string   `json:"error"`
}

type configValues struct {
	Listen          *string      `json:"listen"`
	DataDir         *string      `json:"data_dir"`
	TLS             *config.TLS  `json:"tls"`
	MaxFileSizeMB   *int64       `json:"max_file_size_mb"`
	MaxItemSizeMB   *int64       `json:"max_item_size_mb"`
	MinFreeDiskMB   *int64       `json:"min_free_disk_mb"`
	MaxFilesPerItem *int         `json:"max_files_per_item"`
	MaxTextBytes    *int         `json:"max_text_bytes"`
	SessionTTLHours *int         `json:"session_ttl_hours"`
	MDNS            *config.MDNS `json:"mdns"`
}

func (v configValues) applyTo(c config.Config) config.Config {
	if v.Listen != nil {
		c.Listen = *v.Listen
	}
	if v.DataDir != nil {
		c.DataDir = *v.DataDir
	}
	if v.TLS != nil {
		c.TLS = *v.TLS
	}
	if v.MaxFileSizeMB != nil {
		c.MaxFileSizeMB = *v.MaxFileSizeMB
	}
	if v.MaxItemSizeMB != nil {
		c.MaxItemSizeMB = *v.MaxItemSizeMB
	}
	if v.MinFreeDiskMB != nil {
		c.MinFreeDiskMB = *v.MinFreeDiskMB
	}
	if v.MaxFilesPerItem != nil {
		c.MaxFilesPerItem = *v.MaxFilesPerItem
	}
	if v.MaxTextBytes != nil {
		c.MaxTextBytes = *v.MaxTextBytes
	}
	if v.SessionTTLHours != nil {
		c.SessionTTLHours = *v.SessionTTLHours
	}
	if v.MDNS != nil {
		c.MDNS = *v.MDNS
	}
	return c
}

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":       true,
		"name":     "lanbox",
		"version":  s.version,
		"uptime_s": int(time.Since(s.startedAt).Seconds()),
	})
}

func (s *Server) handleGetConfig(w http.ResponseWriter, r *http.Request) {
	cur := s.current()
	baseline := cur
	configFile, webConfig := "", ""
	if s.live != nil {
		configFile, webConfig = s.live.Paths()
		baseline = s.live.BaselineConfig()
	}
	resp := map[string]any{
		"values":         cur,
		"baseline":       baseline,
		"origins":        cur.Origins,
		"editable":       config.EditableKeys(),
		"restart_fields": config.RestartFields(),
		"paths":          map[string]string{"config_file": configFile, "web_config": webConfig},
	}
	if s.mdnsStatus != nil {
		resp["mdns"] = s.mdnsStatus()
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handlePutConfig(w http.ResponseWriter, r *http.Request) {
	if s.live == nil {
		writeError(w, http.StatusServiceUnavailable, "config_unavailable", "配置服务未就绪")
		return
	}
	var in struct {
		Values  *configValues `json:"values"`
		Restart bool          `json:"restart"`
	}
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil || in.Values == nil {
		writeError(w, http.StatusBadRequest, "bad_request", "请求体不是合法配置")
		return
	}
	desired := in.Values.applyTo(s.current())
	res, err := s.live.Update(desired)
	if err != nil {
		switch {
		case errors.Is(err, config.ErrInvalidTLS):
			writeError(w, http.StatusBadRequest, "invalid_tls", err.Error())
		case errors.Is(err, config.ErrListenUnavailable):
			writeError(w, http.StatusBadRequest, "listen_unavailable", err.Error())
		default:
			writeError(w, http.StatusBadRequest, "invalid_config", err.Error())
		}
		return
	}
	s.SetConfig(s.live.Current())
	restarting := false
	if in.Restart && len(res.RestartRequired) > 0 {
		restarting = s.live.RequestRestart()
	}
	s.log.Printf("info: %s 更新配置 %v（热更 %v，需重启 %v）", userFrom(r).Username, res.Changed, res.Applied, res.RestartRequired)
	writeJSON(w, http.StatusOK, map[string]any{
		"changed":          res.Changed,
		"applied":          res.Applied,
		"restart_required": res.RestartRequired,
		"restarting":       restarting,
	})
}

func (s *Server) handleRestart(w http.ResponseWriter, r *http.Request) {
	if s.live == nil {
		writeError(w, http.StatusServiceUnavailable, "config_unavailable", "配置服务未就绪")
		return
	}
	if !s.live.RequestRestart() {
		writeError(w, http.StatusTooManyRequests, "restart_cooldown", "重启过于频繁，请稍后再试")
		return
	}
	s.log.Printf("info: %s 请求重启服务", userFrom(r).Username)
	writeJSON(w, http.StatusAccepted, map[string]bool{"restarting": true})
}

```

注意：
- `healthz` 只回 `ok/name/version/uptime_s`，不暴露任何路径或配置。
- `handlePutConfig` 里 `s.SetConfig(s.live.Current())` 必须在 `Live.Update` 成功后调用，保证 `current()` 读到新值。
- `POST /api/config/restart` 的冷却判断在 `Live.RequestRestart` 内，`httpapi` 不需要自己记时间。

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/httpapi -count=1`
Expected: PASS（原有 API 测试必须全绿；`s.current()` 改造不能改变错误文案）

- [ ] **Step 5: 提交**

```bash
git add internal/httpapi
git commit -m "feat(httpapi): healthz 与配置读写/重启接口（admin-only）"
```

---

## Task 10: `main` 接线（Live、显式监听、自愈、mDNS、自重启）

**Files:**
- Create: `cmd/lanbox/mdnsglue.go`
- Create: `cmd/lanbox/restart_unix.go`、`cmd/lanbox/restart_other.go`
- Modify: `cmd/lanbox/main.go`

- [ ] **Step 1: 实现 `cmd/lanbox/mdnsglue.go`**

```go
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/pem"
	"log"
	"os"
	"sync"

	"lanbox/internal/config"
	"lanbox/internal/httpapi"
	"lanbox/internal/mdns"
)

// mdnsRunner 负责通告器的启动、热更与状态适配。
type mdnsRunner struct {
	log *log.Logger

	mu      sync.Mutex
	r       *mdns.Responder
	certPath string
	lastErr string
	instance string
	host     string
	addrs    []string
}

func (m *mdnsRunner) apply(cfg config.Config) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !cfg.MDNS.Enabled {
		if m.r != nil {
			_ = m.r.Close()
			m.r = nil
		}
		m.lastErr = ""
		m.instance, m.host, m.addrs = "", "", nil
		return
	}
	rec := mdns.Records{
		Instance: cfg.MDNS.Instance,
		Host:     cfg.MDNS.Hostname,
		Port:     cfg.ListenPort(),
		TXT: map[string]string{
			"v":    "1",
			"tls":  "1",
			"auth": "1",
		},
	}
	if rec.Instance == "" {
		rec.Instance = rec.Host
	}
	rec.TXT["name"] = rec.Instance
	if fp := certFingerprint(m.certPath); fp != "" {
		rec.TXT["fp"] = fp
	}
	if m.r == nil {
		r, err := mdns.Start(rec)
		if err != nil {
			m.lastErr = err.Error()
			m.log.Printf("warn: mDNS 未启用：%v", err)
			return
		}
		m.r = r
	} else if err := m.r.Update(rec); err != nil {
		m.lastErr = err.Error()
		m.log.Printf("warn: mDNS 更新失败：%v", err)
		return
	}
	m.lastErr = ""
	st := m.r.Status()
	m.instance, m.host = st.Instance, st.Host
	m.addrs = nil
	for _, ip := range st.Addrs {
		m.addrs = append(m.addrs, ip.String())
	}
	m.log.Printf("info: mDNS 已通告 %s.local（实例 %s）", st.Host, st.Instance)
}

func (m *mdnsRunner) status(cfg config.Config) httpapi.MDNSStatus {
	m.mu.Lock()
	defer m.mu.Unlock()
	return httpapi.MDNSStatus{
		Running:  m.r != nil,
		Instance: m.instance,
		Host:     m.host,
		Port:     cfg.ListenPort(),
		Addrs:    append([]string(nil), m.addrs...),
		Error:    m.lastErr,
	}
}

func (m *mdnsRunner) close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.r != nil {
		_ = m.r.Close()
		m.r = nil
	}
}

// certFingerprint 返回叶证书 DER 的 sha256 hex；读不到时返回空串。
func certFingerprint(certPath string) string {
	if certPath == "" {
		return ""
	}
	return fingerprintFromPEM(certPath)
}
```

同文件追加（避免再依赖额外包）：

```go
func fingerprintFromPEM(certPath string) string {
	data, err := os.ReadFile(certPath)
	if err != nil {
		return ""
	}
	block, _ := pem.Decode(data)
	if block == nil || block.Type != "CERTIFICATE" {
		return ""
	}
	sum := sha256.Sum256(block.Bytes)
	return hex.EncodeToString(sum[:])
}
```

导入补 `os`、`encoding/pem`。
- [ ] **Step 2: 实现自重启文件**

`cmd/lanbox/restart_unix.go`：

```go
//go:build unix

package main

import (
	"os"
	"syscall"
)

// restartProcess 用 exec 原地重启（PID 不变），交给 systemd 的 Restart=always 兜底。
func restartProcess() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	return syscall.Exec(exe, os.Args, os.Environ())
}
```

`cmd/lanbox/restart_other.go`：

```go
//go:build !unix

package main

import "errors"

func restartProcess() error { return errors.New("当前平台不支持自重启") }
```

- [ ] **Step 3: 改写 `cmd/lanbox/main.go` 为以下内容**

```go
// lanbox：文件与消息共享服务。
package main

import (
	"context"
	"crypto/tls"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"lanbox"
	"lanbox/internal/auth"
	"lanbox/internal/config"
	"lanbox/internal/httpapi"
	"lanbox/internal/store"
	"lanbox/internal/tlsutil"
)

// version 由构建时注入：-ldflags "-X main.version=v1.0.0"
var version = "dev"

func main() {
	if err := run(); err != nil {
		log.Fatalf("lanbox 启动失败: %v", err)
	}
}

func run() error {
	cfgPath := flag.String("config", "config.json", "配置文件路径（不存在则使用默认值）")
	resetAdmin := flag.Bool("reset-admin-password", false, "重置 admin 密码并退出")
	showVersion := flag.Bool("version", false, "打印版本号并退出")
	flag.Parse()

	if *showVersion {
		fmt.Printf("lanbox %s\n", version)
		return nil
	}

	logger := log.New(os.Stdout, "", log.LstdFlags)
	restartCh := make(chan struct{}, 1)
	mdnsRun := &mdnsRunner{log: logger}
	var (
		st *store.Store
		am *auth.Manager
	)

	// hooks 在 OpenLive 时绑定；闭包变量 st/am 在后续赋值后才被调用。
	live, err := config.OpenLive(*cfgPath, config.Hooks{
		Apply: func(prev, next config.Config) error {
			if st != nil {
				st.SetLimits(storeOpts(next))
			}
			if am != nil {
				am.SetSessionTTL(next.SessionTTL())
			}
			mdnsRun.apply(next)
			return nil
		},
		Restart: func() {
			select {
			case restartCh <- struct{}{}:
			default:
			}
		},
	})
	if err != nil {
		return err
	}
	cfg := live.Current()

	if err := os.MkdirAll(cfg.DataDir, 0o750); err != nil {
		return fmt.Errorf("创建数据目录 %s 失败: %w", cfg.DataDir, err)
	}

	am, err = auth.Open(filepath.Join(cfg.DataDir, "users.json"), cfg.SessionTTL())
	if err != nil {
		return err
	}

	if *resetAdmin {
		pw := auth.GeneratePassword()
		if err := am.SetPassword("admin", pw); err != nil {
			return err
		}
		printPasswordBox("admin 密码已重置", pw)
		return nil
	}

	pw, created, err := am.BootstrapAdmin()
	if err != nil {
		return err
	}
	if created {
		printPasswordBox("已创建管理员账号 admin", pw)
	}

	st, err = store.Open(filepath.Join(cfg.DataDir, "items"), storeOpts(cfg))
	if err != nil {
		return err
	}

	certPath, keyPath, generated, err := tlsutil.Ensure(cfg.DataDir, cfg.TLS.CertFile, cfg.TLS.KeyFile, tlsutil.Hosts(cfg.Listen))
	if err != nil {
		return err
	}
	if generated {
		logger.Printf("info: 已生成自签证书 %s（浏览器首次访问需手动信任）", certPath)
	}
	mdnsRun.certPath = certPath

	srv := httpapi.New(httpapi.Options{
		Cfg:        cfg,
		Store:      st,
		Auth:       am,
		Web:        lanbox.WebFS(),
		Log:        logger,
		Live:       live,
		MDNSStatus: func() httpapi.MDNSStatus { return mdnsRun.status(live.Current()) },
		StartedAt:  time.Now(),
		Version:    version,
	})

	mdnsRun.apply(cfg)
	defer mdnsRun.close()

	// 显式绑定，便于"改错 listen"时启动自愈
	ln, err := net.Listen("tcp", cfg.Listen)
	if err != nil && cfg.Origins["listen"] == "web" {
		logger.Printf("warn: 网页设置的 listen=%s 绑定失败（%v），回退到文件配置", cfg.Listen, err)
		if _, derr := live.DropWebOverride("listen"); derr != nil {
			logger.Printf("warn: 清除 listen 覆盖失败: %v", derr)
		} else {
			cfg = live.Current()
		}
		ln, err = net.Listen("tcp", cfg.Listen)
	}
	if err != nil {
		return fmt.Errorf("监听 %s 失败: %w", cfg.Listen, err)
	}

	httpSrv := &http.Server{
		Handler:           srv,
		ReadHeaderTimeout: 20 * time.Second,
		IdleTimeout:       120 * time.Second,
		TLSConfig:         &tls.Config{MinVersion: tls.VersionTLS12},
	}

	printAccessURLs(cfg, logger)

	errCh := make(chan error, 1)
	go func() {
		if err := httpSrv.ServeTLS(ln, certPath, keyPath); err != nil && err != http.ErrServerClosed {
			errCh <- err
			return
		}
		errCh <- nil
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	shutdown := func() error {
		srv.Close() // 先断 SSE，避免 Shutdown 等待长连接
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return httpSrv.Shutdown(ctx)
	}

	select {
	case err := <-errCh:
		mdnsRun.close()
		return err
	case sig := <-sigCh:
		logger.Printf("info: 收到信号 %v，开始优雅退出", sig)
		if err := shutdown(); err != nil {
			return fmt.Errorf("关闭服务超时: %w", err)
		}
		logger.Println("info: 已退出")
		return nil
	case <-restartCh:
		logger.Println("info: 收到网页重启请求，正在重新加载进程")
		time.Sleep(300 * time.Millisecond) // 让 202 响应先写回浏览器
		mdnsRun.close()
		_ = shutdown()
		if err := restartProcess(); err != nil {
			logger.Printf("error: 自重启失败: %v", err)
			return err
		}
		return nil
	}
}

// storeOpts 把配置映射为 store 限额。
func storeOpts(cfg config.Config) store.Options {
	return store.Options{
		MaxFileBytes: cfg.MaxFileBytes(),
		MinFreeBytes: cfg.MinFreeBytes(),
		MaxTextBytes: cfg.MaxTextBytes,
		MaxFiles:     cfg.MaxFilesPerItem,
		MaxItemBytes: cfg.MaxItemBytes(),
	}
}

func printPasswordBox(title, password string) {
	line := strings.Repeat("=", 46)
	fmt.Printf("\n%s\n  %s\n  用户名: admin\n  密  码: %s\n  请立即保存，此密码只显示这一次。\n  忘记可用 -reset-admin-password 重置。\n%s\n\n",
		line, title, password, line)
}

func printAccessURLs(cfg config.Config, logger *log.Logger) {
	portText := fmt.Sprintf("%d", cfg.ListenPort())
	if _, p, err := net.SplitHostPort(cfg.Listen); err == nil && p != "" {
		portText = p
	}
	ips, err := tlsutil.LocalIPs()
	if err != nil || len(ips) == 0 {
		logger.Printf("info: 监听 %s（HTTPS）", cfg.Listen)
	} else {
		logger.Printf("info: 监听 %s（HTTPS），可用地址：", cfg.Listen)
		printed := map[string]bool{}
		for _, ip := range ips {
			parsed := net.ParseIP(ip)
			if parsed == nil || parsed.IsLinkLocalUnicast() {
				continue
			}
			host := ip
			if strings.Contains(ip, ":") {
				host = "[" + ip + "]"
			}
			url := "https://" + host + ":" + portText
			if printed[url] {
				continue
			}
			printed[url] = true
			logger.Printf("  %s", url)
		}
	}
	if cfg.MDNS.Enabled {
		logger.Printf("  https://%s.local:%s （mDNS）", cfg.MDNS.Hostname, portText)
	}
}
```

注意：
- `st`/`am` 在 `OpenLive` 时尚未赋值，热更钩子用闭包变量读取，`Apply` 只在后续请求到来后才被调用。
- `restartCh` 是唯一的重启入口：`Live.RequestRestart` → `Hooks.Restart` → 写入 channel → 主循环 select 分支。
- `ServeTLS(ln, ...)` 使用已绑定的 listener，确保自愈逻辑生效。

- [ ] **Step 4: 编译 + 单元测试**

Run: `go build ./... && go vet ./... && go test ./... -count=1`
Expected: 全绿

- [ ] **Step 5: 提交**

```bash
git add cmd/lanbox
git commit -m "feat(lanbox): 显式监听与启动自愈、mDNS 接线、网页自重启"
```

---

## Task 11: 网页设置页

**Files:**
- Modify: `web/index.html`、`web/admin.js`、`web/api.js`、`web/style.css`
- Create: `web/settings.js`
- Test: 手动（无前端测试框架）；`make check` 必须全绿

- [ ] **Step 1: `web/api.js` 追加接口**

```js
	config: () => request('GET', '/api/config'),
	saveConfig: (values, restart = false) => request('PUT', '/api/config', { values, restart }),
	restart: () => request('POST', '/api/config/restart'),
	healthz: async () => {
		const ctrl = new AbortController();
		const timer = setTimeout(() => ctrl.abort(), 1000);
		try {
			const resp = await fetch('/api/healthz', { signal: ctrl.signal, cache: 'no-store' });
			return resp.ok ? await resp.json() : null;
		} finally {
			clearTimeout(timer);
		}
	},
```

- [ ] **Step 2: `web/index.html` 改造管理抽屉**

把现有 `<aside id="admin-drawer">` 整块替换为：

```html
<aside id="admin-drawer" class="drawer" hidden>
  <div class="drawer-head">
    <strong>管理</strong>
    <span class="spacer"></span>
    <button id="btn-close-admin" type="button">关闭</button>
  </div>
  <div class="tabs">
    <button id="tab-users" class="chip active" type="button">用户</button>
    <button id="tab-settings" class="chip" type="button">设置</button>
  </div>
  <section id="pane-users">
    <form id="user-form" class="card">
      <label>用户名<input id="user-name" autocomplete="off" required></label>
      <label>密码<input id="user-pass" type="password" autocomplete="new-password" minlength="8" required></label>
      <label class="inline"><input id="user-admin" type="checkbox"> 管理员</label>
      <button type="submit" class="primary">新增用户</button>
      <p id="admin-error" class="error" hidden></p>
    </form>
    <ul id="user-list" class="users"></ul>
  </section>
  <section id="pane-settings" hidden>
    <div id="settings-groups"></div>
    <p id="settings-error" class="error" hidden></p>
    <p id="settings-hint" class="hint"></p>
    <div class="settings-actions">
      <button id="btn-save-settings" class="primary" type="button">保存</button>
      <button id="btn-restart" type="button" hidden>立即重启</button>
    </div>
  </section>
</aside>

<div id="restart-mask" class="mask" hidden>
  <div class="card mask-card">
    <strong>服务重启中…</strong>
    <p class="hint">约 1–3 秒不可用，所有人需重新登录。若刚改了监听地址，请在新地址访问。</p>
    <p id="restart-error" class="error" hidden></p>
  </div>
</div>
```

- [ ] **Step 3: `web/admin.js` 改为标签页壳（保留用户逻辑）**

```js
import { api } from './api.js';
import { initSettings, showSettings } from './settings.js';

const $ = (id) => document.getElementById(id);

function showAdminError(msg) {
  const el = $('admin-error');
  el.textContent = msg || '';
  el.hidden = !msg;
}

async function renderUsers() {
  const list = $('user-list');
  let users = [];
  try {
    users = await api.users();
  } catch (err) {
    showAdminError(err.message);
    return;
  }
  list.replaceChildren(...users.map(userRow));
}

function userRow(u) {
  const li = document.createElement('li');
  const name = document.createElement('span');
  name.textContent = u.username;
  const tag = document.createElement('span');
  tag.className = 'tag';
  tag.textContent = u.is_admin ? '管理员' : '成员';
  const sp = document.createElement('span');
  sp.className = 'spacer';

  const reset = document.createElement('button');
  reset.type = 'button';
  reset.textContent = '重置密码';
  reset.addEventListener('click', async () => {
    const pw = prompt(`为 ${u.username} 设置新密码（至少 8 位）：`);
    if (!pw) return;
    try {
      await api.setUserPassword(u.username, pw);
      showAdminError('');
      alert(`已重置 ${u.username} 的密码`);
    } catch (err) {
      showAdminError(err.message);
    }
  });

  const del = document.createElement('button');
  del.type = 'button';
  del.className = 'danger';
  del.textContent = '删除';
  del.addEventListener('click', async () => {
    if (!confirm(`确定删除用户 ${u.username}？`)) return;
    try {
      await api.deleteUser(u.username);
      showAdminError('');
      await renderUsers();
    } catch (err) {
      showAdminError(err.message);
    }
  });

  li.append(name, tag, sp, reset, del);
  return li;
}

let tab = 'users';

function switchTab(name) {
  tab = name;
  $('tab-users').classList.toggle('active', name === 'users');
  $('tab-settings').classList.toggle('active', name === 'settings');
  $('pane-users').hidden = name !== 'users';
  $('pane-settings').hidden = name !== 'settings';
  if (name === 'settings') {
    showSettings().catch((err) => {
      const el = $('settings-error');
      el.textContent = err.message;
      el.hidden = false;
    });
  }
}

export function initAdmin() {
  $('btn-admin').addEventListener('click', async () => {
    showAdminError('');
    $('admin-drawer').hidden = false;
    switchTab('users');
    await renderUsers();
  });
  $('btn-close-admin').addEventListener('click', () => {
    $('admin-drawer').hidden = true;
  });
  $('tab-users').addEventListener('click', () => switchTab('users'));
  $('tab-settings').addEventListener('click', () => switchTab('settings'));
  $('user-form').addEventListener('submit', async (ev) => {
    ev.preventDefault();
    showAdminError('');
    try {
      await api.createUser($('user-name').value.trim(), $('user-pass').value, $('user-admin').checked);
      $('user-name').value = '';
      $('user-pass').value = '';
      $('user-admin').checked = false;
      await renderUsers();
    } catch (err) {
      showAdminError(err.message);
    }
  });
  initSettings();
}
```

- [ ] **Step 4: 新增 `web/settings.js`**

```js
import { api } from './api.js';

const $ = (id) => document.getElementById(id);

const GROUPS = [
  {
    title: '上传与存储',
    fields: [
      { key: 'max_file_size_mb', label: '单文件上限 (MB)', min: 1, step: 1 },
      { key: 'max_item_size_mb', label: '单条总大小上限 (MB)', min: 1, step: 1 },
      { key: 'min_free_disk_mb', label: '保留空闲空间 (MB)', min: 0, step: 1 },
      { key: 'max_files_per_item', label: '单条附件数上限', min: 1, step: 1 },
      { key: 'max_text_bytes', label: '文字上限 (字节)', min: 1, step: 1 },
    ],
  },
  {
    title: '会话',
    fields: [{ key: 'session_ttl_hours', label: '会话有效期 (小时)', min: 1, step: 1 }],
  },
  {
    title: '网络与证书',
    fields: [
      { key: 'listen', label: '监听地址', type: 'text', hint: '如 :8443 或 192.168.1.9:8443' },
      { key: 'tls.cert_file', label: '证书文件', type: 'text', hint: '留空则用自签证书' },
      { key: 'tls.key_file', label: '私钥文件', type: 'text', hint: '与证书同时留空或同时填写' },
      { key: 'data_dir', label: '数据目录', type: 'text' },
    ],
  },
  {
    title: '局域网发现',
    fields: [
      { key: 'mdns.enabled', label: '启用 mDNS 通告', type: 'bool' },
      { key: 'mdns.hostname', label: '主机名', type: 'text', hint: '访问地址 https://<主机名>.local:<端口>' },
      { key: 'mdns.instance', label: '实例名', type: 'text', hint: '留空则同主机名' },
    ],
  },
];

const RESTART_HINT = '以下项需重启生效：';
let current = null;

const originText = { default: '默认', file: '配置文件', env: '环境变量', web: '网页设置' };

function getValue(obj, path) {
  return path.split('.').reduce((o, k) => (o == null ? undefined : o[k]), obj);
}

function setValue(obj, path, value) {
  const parts = path.split('.');
  let cur = obj;
  for (const k of parts.slice(0, -1)) {
    if (cur[k] == null || typeof cur[k] !== 'object') cur[k] = {};
    cur = cur[k];
  }
  cur[parts[parts.length - 1]] = value;
}

function rowFor(field) {
  const row = document.createElement('div');
  row.className = 'settings-row';

  const label = document.createElement('label');
  label.textContent = field.label;

  let input;
  if (field.type === 'bool') {
    input = document.createElement('input');
    input.type = 'checkbox';
    input.checked = Boolean(getValue(current.values, field.key));
  } else {
    input = document.createElement('input');
    input.type = field.type === 'text' ? 'text' : 'number';
    if (field.min !== undefined) input.min = String(field.min);
    if (field.step !== undefined) input.step = String(field.step);
    input.value = getValue(current.values, field.key) ?? '';
  }
  input.dataset.key = field.key;
  input.dataset.kind = field.type === 'bool' ? 'bool' : field.type === 'text' ? 'text' : 'number';

  const origin = current.origins[field.key] || 'default';
  if (origin === 'web' && field.type !== 'bool') {
    const baseValue = getValue(current.baseline, field.key);
    if (baseValue !== undefined && baseValue !== '') input.placeholder = `恢复默认：${baseValue}`;
  }

  const badges = document.createElement('span');
  badges.className = 'badges';
  const originBadge = document.createElement('span');
  originBadge.className = 'badge origin';
  originBadge.textContent = originText[origin] || origin;
  badges.append(originBadge);
  if ((current.restart_fields || []).includes(field.key)) {
    const rb = document.createElement('span');
    rb.className = 'badge warn';
    rb.textContent = '需重启';
    badges.append(rb);
  }

  const wrap = document.createElement('span');
  wrap.className = 'settings-input';
  wrap.append(input, badges);
  if (field.hint) {
    const hint = document.createElement('small');
    hint.className = 'hint';
    hint.textContent = field.hint;
    wrap.append(hint);
  }
  row.append(label, wrap);
  return row;
}

function render(values) {
  const root = $('settings-groups');
  root.replaceChildren();
  for (const group of GROUPS) {
    const box = document.createElement('div');
    box.className = 'card settings-group';
    const h = document.createElement('strong');
    h.textContent = group.title;
    box.append(h);
    for (const field of group.fields) box.append(rowFor(field));
    if (group.title === '局域网发现') {
      const st = values.mdns || {};
      const info = document.createElement('p');
      info.className = 'hint';
      info.textContent = st.running
        ? `已通告：https://${st.host}.local:${st.port}（实例 ${st.instance}${
            st.addrs && st.addrs.length ? '，' + st.addrs.join(', ') : ''
          }）`
        : `未通告${st.error ? '：' + st.error : ''}`;
      box.append(info);
    }
    root.append(box);
  }
  const paths = values.paths || {};
  $('settings-hint').textContent =
    `配置文件：${paths.config_file || '-'}；网页覆盖：${paths.web_config || '-'}。` +
    '删除网页覆盖文件即回到配置文件设置。';
}

export async function showSettings() {
  $('settings-error').hidden = true;
  $('settings-hint').textContent = '加载中…';
  current = await api.config();
  render(current);
  $('btn-restart').hidden = true;
}

function collect() {
  const desired = JSON.parse(JSON.stringify(current.values));
  for (const input of $('settings-groups').querySelectorAll('input[data-key]')) {
    const kind = input.dataset.kind;
    let value;
    if (kind === 'bool') value = input.checked;
    else if (kind === 'number') value = Number(input.value);
    else value = input.value.trim();
    setValue(desired, input.dataset.key, value);
  }
  return desired;
}

async function save() {
  const err = $('settings-error');
  err.hidden = true;
  const desired = collect();
  try {
    const res = await api.saveConfig(desired, false);
    await showSettings();
    const bullets = [];
    if (res.applied.length) bullets.push(`已生效：${res.applied.join('、')}`);
    if (res.restart_required.length) bullets.push(`${RESTART_HINT}${res.restart_required.join('、')}`);
    $('settings-hint').textContent = bullets.join('；') || '没有变化';
    $('btn-restart').hidden = res.restart_required.length === 0;
  } catch (e) {
    err.textContent = e.message;
    err.hidden = false;
  }
}

async function doRestart() {
  if (!confirm('确定重启服务？约 1–3 秒不可用，所有人需重新登录。若刚改了监听地址，请在新地址访问。')) return;
  const mask = $('restart-mask');
  const err = $('restart-error');
  err.hidden = true;
  mask.hidden = false;
  try {
    await api.restart();
  } catch (e) {
    err.textContent = e.message;
    err.hidden = false;
    mask.hidden = true;
    return;
  }
  const deadline = Date.now() + 30000;
  const tick = async () => {
    try {
      const h = await api.healthz();
      if (h && h.ok) {
        location.reload();
        return;
      }
    } catch {
      /* 服务还没回来 */
    }
    if (Date.now() > deadline) {
      err.textContent = '服务 30 秒内没有回来，请检查进程与监听地址后手动访问。';
      err.hidden = false;
      return;
    }
    setTimeout(tick, 800);
  };
  setTimeout(tick, 500);
}

export function initSettings() {
  $('btn-save-settings').addEventListener('click', save);
  $('btn-restart').addEventListener('click', doRestart);
}
```

- [ ] **Step 5: `web/style.css` 追加样式**

```css
.tabs { display: flex; gap: 8px; padding: 0 16px 8px; }
.settings-group { margin: 0 0 12px; }
.settings-group > strong { display: block; margin-bottom: 8px; }
.settings-row {
  display: grid;
  grid-template-columns: 11rem 1fr;
  gap: 8px;
  align-items: start;
  margin-bottom: 8px;
}
.settings-input { display: flex; flex-wrap: wrap; gap: 6px; align-items: center; }
.settings-input input[type="text"], .settings-input input[type="number"] { min-width: 12rem; }
.settings-input .hint { flex-basis: 100%; }
.badges { display: inline-flex; gap: 4px; }
.badge.origin { background: rgba(127, 127, 127, 0.16); }
.badge.warn { background: rgba(220, 140, 0, 0.22); }
.settings-actions { display: flex; gap: 8px; padding: 8px 16px 16px; }
.mask {
  position: fixed;
  inset: 0;
  background: rgba(0, 0, 0, 0.55);
  display: flex;
  align-items: center;
  justify-content: center;
  z-index: 50;
}
.mask-card { max-width: 26rem; text-align: center; }
@media (max-width: 640px) {
  .settings-row { grid-template-columns: 1fr; }
}
```

- [ ] **Step 6: 校验与手动验收**

Run: `make check`
Expected: 全绿（Go 侧不受影响）

浏览器验收（记入 README 的验收清单）：
1. 管理员 → 管理 → 设置：四组字段都显示，来源徽标与实际一致
2. 改「文字上限」→ 保存 → 提示"已生效"，发一条超过旧上限的文字成功
3. 改「监听地址」→ 保存 → 提示"需重启生效"并出现「立即重启」→ 点按并确认 → 遮罩 → 自动回到登录页
4. 浏览器控制台无 CSP 报错、无 `alert` 之外的阻塞

- [ ] **Step 7: 提交**

```bash
git add web
git commit -m "feat(web): 管理员设置页（来源/需重启标注、保存与重启遮罩）"
```

---

## Task 12: CLI `discover`、自动发现与证书指纹固定

**Files:**
- Create: `cmd/lanbox-cli/cmd_discover.go`
- Modify: `cmd/lanbox-cli/main.go`、`cmd/lanbox-cli/render.go`
- Test: `cmd/lanbox-cli/discover_test.go`

- [ ] **Step 1: 写失败测试**

```go
package main

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"lanbox/internal/mdns"
)

func TestRenderServices(t *testing.T) {
	var buf bytes.Buffer
	services := []mdns.Service{
		{Instance: "office", Host: "nas", Port: 8443, TXT: map[string]string{"v": "1", "fp": "0123456789abcdef"}},
		{Instance: "lab", Host: "pi", Port: 9443, TXT: map[string]string{"v": "1"}},
	}
	if err := renderServices(&buf, services); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{"office", "https://", "8443", "01234567", "lab", "pi.local", "-"} {
		if !strings.Contains(out, want) {
			t.Fatalf("输出缺少 %q:\n%s", want, out)
		}
	}
}

func TestDecideServer(t *testing.T) {
	orig := browseServices
	defer func() { browseServices = orig }()

	browseServices = func(time.Duration) ([]mdns.Service, error) {
		return nil, nil
	}
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
```

（测试里需要 `net` 导入。）

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./cmd/lanbox-cli -run 'TestRenderServices|TestDecideServer' -v`
Expected: FAIL —— `undefined: renderServices` / `decideServer` / `browseServices`

- [ ] **Step 3: 实现 `cmd/lanbox-cli/cmd_discover.go`**

```go
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"strings"
	"time"

	"lanbox/internal/mdns"
)

// browseServices 可被测试替换。
var browseServices = func(timeout time.Duration) ([]mdns.Service, error) {
	return mdns.Browse(context.Background(), timeout)
}

// urlOf 把发现结果转成一个可访问的 URL。
func urlOf(s mdns.Service) string {
	host := pickHost(s.Addrs)
	if host == "" || s.Port <= 0 {
		return "-"
	}
	return fmt.Sprintf("https://%s:%d", host, s.Port)
}

// pickHost 优先选非链路本地的 IPv4，其次 IPv6。
func pickHost(addrs []net.IP) string {
	var v6 string
	for _, ip := range addrs {
		if ip.To4() != nil && !ip.IsLinkLocalUnicast() {
			return ip.String()
		}
		if ip.To4() == nil && !ip.IsLinkLocalUnicast() && v6 == "" {
			v6 = "[" + ip.String() + "]"
		}
	}
	return v6
}

func cmdDiscover(g *globals, args []string) error {
	fs := flag.NewFlagSet("discover", flag.ContinueOnError)
	fs.SetOutput(g.stderr)
	timeout := fs.Duration("timeout", 2*time.Second, "发现超时")
	if err := fs.Parse(args); err != nil {
		return errUsage
	}
	services, err := browseServices(*timeout)
	if err != nil {
		return fmt.Errorf("局域网发现失败: %w", err)
	}
	if g.json {
		return json.NewEncoder(g.stdout).Encode(services)
	}
	return renderServices(g.stdout, services)
}

// decideServer 在局域网内选定唯一的 lanbox 实例。
func decideServer(timeout time.Duration) (baseURL, fp string, err error) {
	services, err := browseServices(timeout)
	if err != nil {
		return "", "", fmt.Errorf("%w: 局域网发现失败: %v", errUsage, err)
	}
	switch len(services) {
	case 0:
		return "", "", fmt.Errorf("%w: 请用 -s 指定服务地址，例如 -s https://192.168.1.9:8443（局域网内未发现 lanbox）", errUsage)
	case 1:
		s := services[0]
		url := urlOf(s)
		if url == "-" {
			return "", "", fmt.Errorf("%w: 发现 %s 但地址不完整，请用 -s 指定", errUsage, s.Instance)
		}
		return url, s.TXT["fp"], nil
	default:
		var b strings.Builder
		for _, s := range services {
			fmt.Fprintf(&b, "\n  %s @ %s", s.Instance, urlOf(s))
		}
		return "", "", fmt.Errorf("%w: 发现多个 lanbox 实例，请用 -s 指定：%s", errUsage, b.String())
	}
}
```

- [ ] **Step 4: `cmd/lanbox-cli/render.go` 追加**

```go
// shortFP 返回指纹前 8 位。
func shortFP(fp string) string {
	if len(fp) <= 8 {
		return fp
	}
	return fp[:8]
}

// renderServices 输出发现结果表格。
func renderServices(w io.Writer, services []mdns.Service) error {
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "实例\t地址\t版本\t主机名\t证书指纹")
	for _, s := range services {
		fp := "-"
		if v := s.TXT["fp"]; v != "" {
			fp = shortFP(v)
		}
		version := s.TXT["v"]
		if version == "" {
			version = "-"
		}
		host := s.Host
		if host == "" {
			host = "-"
		} else {
			host += ".local"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", s.Instance, urlOf(s), version, host, fp)
	}
	return tw.Flush()
}
```

导入补 `lanbox/internal/mdns`。

- [ ] **Step 5: `cmd/lanbox-cli/main.go` 接线**

1. `globals` 增加字段：

```go
	pinFP    string
```

2. `run()` 的 switch 增加（放在 `whoami` 之后均可，`discover` 不需要会话）：

```go
	case "discover":
		err = cmdDiscover(g, cmdArgs)
```

3. `usage` 的命令列表加一行：

```
  discover [-timeout 2s] [-json]  列出局域网内的 lanbox 实例
```

并在"会话与地址"说明处补：`不给 -s 时依次尝试 LANBOX_URL、本地会话、局域网 mDNS 自动发现。`

4. `resolveServer` 末尾改为：

```go
	if sess != nil && sess.URL != "" {
		return sess.URL, nil
	}
	base, fp, err := decideServer(2 * time.Second)
	if err != nil {
		return "", err
	}
	g.pinFP = fp
	return base, nil
```

5. `httpClient` 在 `tlsCfg := &tls.Config{MinVersion: tls.VersionTLS12}` 之后、`if g.insecure` 之前插入：

```go
	if g.pinFP != "" && !g.insecure && g.caFile == "" {
		want := strings.ToLower(strings.TrimSpace(g.pinFP))
		tlsCfg.InsecureSkipVerify = true
		tlsCfg.VerifyPeerCertificate = func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
			if len(rawCerts) == 0 {
				return errors.New("服务器未提供证书")
			}
			sum := sha256.Sum256(rawCerts[0])
			got := hex.EncodeToString(sum[:])
			if subtle.ConstantTimeCompare([]byte(got), []byte(want)) != 1 {
				return fmt.Errorf("证书指纹不匹配（通告 %s，实际 %s）；若证书已更换，请重新 discover 或使用 -ca", shortFP(want), shortFP(got))
			}
			return nil
		}
		fmt.Fprintf(g.stderr, "已按 mDNS 通告固定证书指纹 sha256:%s\n", shortFP(want))
	}
```

导入补 `crypto/sha256`、`crypto/subtle`、`encoding/hex`、`time`。

- [ ] **Step 6: 运行测试确认通过**

Run: `go test ./cmd/lanbox-cli -count=1 && go build ./...`
Expected: PASS

- [ ] **Step 7: 提交**

```bash
git add cmd/lanbox-cli
git commit -m "feat(cli): discover 子命令、无 -s 自动发现与证书指纹固定"
```

---

## Task 13: 文档与端到端验收

**Files:**
- Modify: `README.md`
- Test: `make check` + 人工验收清单（spec §16）

- [ ] **Step 1: README 配置一节改写**

在配置表格中追加：

```markdown
| `mdns.enabled` | `true` | 是否在局域网内以 mDNS 通告 |
| `mdns.hostname` | `lanbox` | 通告为 `https://<hostname>.local:<端口>` |
| `mdns.instance` | 空（同 hostname） | 服务实例名（`_lanbox._tcp` 的 PTR 名称） |
```

环境变量表追加：`LANBOX_MDNS_ENABLED`、`LANBOX_MDNS_HOSTNAME`、`LANBOX_MDNS_INSTANCE`。

在"配置"节末尾追加：

```markdown
### 网页配置与覆盖文件

管理员可在网页「管理 → 设置」里改**全部**配置项。保存后的值写入 `<data_dir>/webconfig.json`（稀疏覆盖，权限 0600），
优先级为：`默认值 → config.json → 环境变量 → webconfig.json`。

- 删除 `webconfig.json` 即回到 `config.json` + 环境变量
- 网页上每项都标注来源（默认/配置文件/环境变量/网页设置）
- `max_*`、`session_ttl_hours`、`mdns.*` 立即生效
- `listen`、`data_dir`、`tls.*` 需重启；网页会提示并提供「立即重启」（`syscall.Exec`，PID 不变，所有人需重新登录）
- 覆盖文件固定在"文件/环境变量解析出的 `data_dir`"下：改过 `data_dir` 后，旧目录里的 `webconfig.json` 仍是真源
- **救援**：把 `listen` 改成不可用的值（或端口被占）时，服务启动会检测到并自动忽略该覆盖、回退到文件配置，日志有 `warn: 网页设置的 listen=... 绑定失败`。也可直接编辑/删除 `<data_dir>/webconfig.json`
```

- [ ] **Step 2: README 增加 mDNS 与 CLI 发现小节**

```markdown
## 局域网发现（mDNS）

服务启动后会在局域网内通告：

- 服务类型：`_lanbox._tcp`（实例名 <mdns.instance>，端口 = 监听端口）
- 主机名：`<mdns.hostname>.local`
- TXT：`v=1`、`name=<实例名>`、`tls=1`、`auth=1`、`fp=<叶证书 SHA-256>`

因此可以直接访问 `https://lanbox.local:8443`（自签证书仍需首次信任）。手机、macOS、Linux 均可解析 `.local`。

排查：

- `avahi-browse -rt _lanbox._tcp`（Linux）或 `dns-sd -B _lanbox._tcp`（macOS）应能看到实例
- 看不到：确认两台机器在同一网段、未禁用组播、防火墙未拦 UDP 5353；服务端日志在 mDNS 不可用时会打印 `warn: mDNS 未启用：...`
- 与 avahi/systemd-resolved 共存：使用 `SO_REUSEADDR`+`SO_REUSEPORT`；若绑定失败则只降级通告，不影响主服务

CLI：

```bash
lanbox-cli discover                 # 列出局域网实例（实例/地址/版本/主机名/证书指纹）
lanbox-cli discover -json           # 机器可读
lanbox-cli ls                       # 不给 -s 时自动发现；唯一实例直接使用，多个则要求 -s
```

自动发现时会按 TXT 里的证书指纹**固定 TLS**（比 `-insecure` 安全）；`-ca` 与 `-insecure` 优先级更高。
```

- [ ] **Step 3: README 的"不做的事"移除 mDNS 相关表述并补验收清单**

在"手动验收清单"追加：

```markdown
12. `avahi-browse -rt _lanbox._tcp` 能看到实例与 TXT；`https://<hostname>.local:<端口>` 可打开登录页
13. 网页设置改 `mdns.hostname` 保存 → 无需重启，旧名发 goodbye、新名出现；关闭 `mdns.enabled` 后记录消失
14. 网页设置改 `max_text_bytes` → 立即生效（发超旧上限的文字成功）
15. 网页设置改 `listen` → 提示需重启 → 「立即重启」后在新端口可访问、页面自动回到登录页
16. `lanbox-cli discover` 列表正确；无 `-s` 且唯一实例时 `lanbox-cli ls` 可直连（不传 `-insecure`）；两个实例时报错列候选
```

- [ ] **Step 4: 全量自检**

Run: `make check`
Expected: gofmt/vet/test 全绿

Run: `go build -tags '' ./... && GOOS=linux GOARCH=arm64 go build ./...`
Expected: 交叉编译通过（`sock_linux.go` 生效）

- [ ] **Step 5: 按 spec §16 人工验收**

逐条执行 spec 的 16 项验收（本机 + 一台同网段设备），把结果记录在 PR/提交说明里。至少确认：

- 第 3/4 项（CLI 发现与指纹固定）
- 第 6/7 项（热更立即生效）
- 第 8/9 项（重启与启动自愈）
- 第 10 项（删除 webconfig 回滚）

- [ ] **Step 6: 提交**

```bash
git add README.md
git commit -m "docs: mDNS 发现、网页配置与救援步骤"
```
