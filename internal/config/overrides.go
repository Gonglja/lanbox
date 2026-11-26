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
	Update          *Update `json:"update,omitempty"`
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
		o.MDNS == nil && o.Update == nil
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
	if o.Update != nil {
		// 令牌只来自环境变量，网页覆盖不会碰它。
		token := c.Update.Token
		c.Update = *o.Update
		c.Update.Token = token
		mark("update.check")
		mark("update.url")
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
	// 令牌不参与比较：它不来自文件也不来自网页。
	cur, base := c.Update, baseline.Update
	cur.Token, base.Token = "", ""
	if cur != base {
		v := cur
		o.Update = &v
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
