// Package config 负责读取 lanbox 的配置：默认值 → config.json → 环境变量 → 校验。
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"time"
)

// TLS 是证书路径配置。两者都为空表示自动生成自签证书。
type TLS struct {
	CertFile string `json:"cert_file"`
	KeyFile  string `json:"key_file"`
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
}

// Defaults 返回可直接使用的默认配置。
func Defaults() Config {
	return Config{
		Listen:          ":8443",
		DataDir:         "./data",
		MaxFileSizeMB:   1024,
		MaxItemSizeMB:   2048,
		MinFreeDiskMB:   1024,
		MaxFilesPerItem: 500,
		MaxTextBytes:    65536,
		SessionTTLHours: 168,
	}
}

// Load 读取 path（不存在则仅用默认值），再应用环境变量并校验。
func Load(path string) (Config, error) {
	c := Defaults()
	raw, err := os.ReadFile(path)
	switch {
	case err == nil:
		if err := json.Unmarshal(raw, &c); err != nil {
			return c, fmt.Errorf("解析配置文件 %s 失败: %w", path, err)
		}
	case errors.Is(err, os.ErrNotExist):
		// 允许没有配置文件
	default:
		return c, fmt.Errorf("读取配置文件 %s 失败: %w", path, err)
	}
	if err := applyEnv(&c); err != nil {
		return c, err
	}
	if err := c.Validate(); err != nil {
		return c, err
	}
	return c, nil
}

func applyEnv(c *Config) error {
	if v, ok := os.LookupEnv("LANBOX_LISTEN"); ok {
		c.Listen = v
	}
	if v, ok := os.LookupEnv("LANBOX_DATA_DIR"); ok {
		c.DataDir = v
	}
	if v, ok := os.LookupEnv("LANBOX_TLS_CERT_FILE"); ok {
		c.TLS.CertFile = v
	}
	if v, ok := os.LookupEnv("LANBOX_TLS_KEY_FILE"); ok {
		c.TLS.KeyFile = v
	}
	ints := []struct {
		env string
		dst *int64
	}{
		{"LANBOX_MAX_FILE_SIZE_MB", &c.MaxFileSizeMB},
		{"LANBOX_MAX_ITEM_SIZE_MB", &c.MaxItemSizeMB},
		{"LANBOX_MIN_FREE_DISK_MB", &c.MinFreeDiskMB},
	}
	for _, it := range ints {
		v, ok := os.LookupEnv(it.env)
		if !ok {
			continue
		}
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			return fmt.Errorf("环境变量 %s 不是整数: %q", it.env, v)
		}
		*it.dst = n
	}
	if v, ok := os.LookupEnv("LANBOX_MAX_FILES_PER_ITEM"); ok {
		n, err := strconv.Atoi(v)
		if err != nil {
			return fmt.Errorf("环境变量 LANBOX_MAX_FILES_PER_ITEM 不是整数: %q", v)
		}
		c.MaxFilesPerItem = n
	}
	if v, ok := os.LookupEnv("LANBOX_MAX_TEXT_BYTES"); ok {
		n, err := strconv.Atoi(v)
		if err != nil {
			return fmt.Errorf("环境变量 LANBOX_MAX_TEXT_BYTES 不是整数: %q", v)
		}
		c.MaxTextBytes = n
	}
	if v, ok := os.LookupEnv("LANBOX_SESSION_TTL_HOURS"); ok {
		n, err := strconv.Atoi(v)
		if err != nil {
			return fmt.Errorf("环境变量 LANBOX_SESSION_TTL_HOURS 不是整数: %q", v)
		}
		c.SessionTTLHours = n
	}
	return nil
}

// Validate 校验配置自洽性，返回面向使用者的中文错误。
func (c Config) Validate() error {
	if c.Listen == "" {
		return errors.New("listen 不能为空，例如 \":8443\"")
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
	return nil
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
