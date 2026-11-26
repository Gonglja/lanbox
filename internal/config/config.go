// Package config 负责读取 lanbox 的配置：默认值 → config.json → 环境变量 → 校验。
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// TLS 是证书路径配置。两者都为空表示自动生成自签证书。
type TLS struct {
	CertFile string `json:"cert_file"`
	KeyFile  string `json:"key_file"`
}

// MDNS 是局域网发现配置。
type MDNS struct {
	Enabled  bool   `json:"enabled"`
	Hostname string `json:"hostname"`
	Instance string `json:"instance"`
}

// Config 是 lanbox 的全部配置项。
type Config struct {
	Listen          string `json:"listen"`
	DataDir         string `json:"data_dir"`
	TLS             TLS    `json:"tls"`
	MaxFileSizeMB   int64  `json:"max_file_size_mb"`
	MaxItemSizeMB   int64  `json:"max_item_size_mb"`
	MinFreeDiskMB   int64  `json:"min_free_disk_mb"`
	MaxFilesPerItem int    `json:"max_files_per_item"`
	MaxTextBytes    int    `json:"max_text_bytes"`
	SessionTTLHours int    `json:"session_ttl_hours"`
	MDNS            MDNS   `json:"mdns"`

	// Origins 记录每个字段来自哪一层（default/file/env/web），供网页标注来源。
	Origins map[string]string `json:"-"`
}

// Defaults 返回可直接使用的默认配置。
func Defaults() Config {
	c := Config{
		Listen:          ":443",
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
	c.Origins = nil
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

// Validate 校验配置自洽性，返回面向使用者的中文错误。
func (c Config) Validate() error {
	if c.Listen == "" {
		return errors.New("listen 不能为空，例如 \":443\"")
	}
	if c.DataDir == "" {
		return errors.New("data_dir 不能为空")
	}
	if c.MaxFileSizeMB <= 0 {
		return errors.New("max_file_size_mb 必须大于 0")
	}
	if c.MaxItemSizeMB <= 0 {
		return errors.New("max_item_size_mb 必须大于 0")
	}
	if c.MinFreeDiskMB < 0 {
		return errors.New("min_free_disk_mb 不能为负数")
	}
	if c.MaxFilesPerItem < 1 || c.MaxFilesPerItem > 10000 {
		return errors.New("max_files_per_item 必须在 1..10000 之间")
	}
	if c.MaxTextBytes <= 0 {
		return errors.New("max_text_bytes 必须大于 0")
	}
	if c.SessionTTLHours <= 0 {
		return errors.New("session_ttl_hours 必须大于 0")
	}
	if (c.TLS.CertFile == "") != (c.TLS.KeyFile == "") {
		return errors.New("tls.cert_file 与 tls.key_file 必须同时配置或同时留空")
	}
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
	return nil
}

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

// ListenPort 从 listen 取端口，解析失败回落 443。
func (c Config) ListenPort() int {
	_, port, err := net.SplitHostPort(c.Listen)
	if err != nil {
		return 443
	}
	n, err := strconv.Atoi(port)
	if err != nil || n <= 0 || n > 65535 {
		return 443
	}
	return n
}

// MaxFileBytes 是单文件大小上限（字节）。
func (c Config) MaxFileBytes() int64 { return c.MaxFileSizeMB << 20 }

// MaxItemBytes 是单条内容附件总大小上限（字节）。
func (c Config) MaxItemBytes() int64 { return c.MaxItemSizeMB << 20 }

// MinFreeBytes 是必须保留的空闲磁盘下限（字节）。
func (c Config) MinFreeBytes() int64 { return c.MinFreeDiskMB << 20 }

// SessionTTL 是会话有效期。
func (c Config) SessionTTL() time.Duration {
	return time.Duration(c.SessionTTLHours) * time.Hour
}
