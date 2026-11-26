// Package store 负责内容的持久化：一条内容一个目录，元数据写在 meta.json。
// 它不认识 HTTP，也不拼接用户可控的路径。
package store

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math/rand/v2"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// 领域错误。httpapi 按这些错误映射状态码。
var (
	ErrNotFound     = errors.New("内容不存在")
	ErrFileNotFound = errors.New("文件不存在")
	ErrTooLarge     = errors.New("文件超过大小上限")
	ErrNoSpace      = errors.New("磁盘空间不足")
	ErrTooManyFiles = errors.New("附件数量超过上限")
	ErrTextTooLong  = errors.New("文本超过长度上限")
	ErrBadPath      = errors.New("附件路径不合法")
	ErrItemTooLarge = errors.New("内容总大小超过上限")
)

// MaxFilesPerItem 是单条内容的附件数量上限。
const MaxFilesPerItem = 20

// File 是一个附件。
type File struct {
	ID        string `json:"id"`
	Path      string `json:"path"` // 相对路径，如 "photos/2024/a.jpg"
	Name      string `json:"name"` // basename，兼容旧前端与磁盘后缀
	Size      int64  `json:"size"`
	SHA256    string `json:"sha256"`
	Downloads int64  `json:"downloads"`
}

// RelPath 返回附件在内容内的相对路径；旧数据没有 path 时回退到文件名。
func (f File) RelPath() string {
	if f.Path != "" {
		return f.Path
	}
	if f.Name != "" {
		return f.Name
	}
	return f.ID
}

// Item 是一条内容（文本和/或附件）。
type Item struct {
	ID        string     `json:"id"`
	CreatedAt time.Time  `json:"created_at"`
	CreatedBy string     `json:"created_by"`
	SourceIP  string     `json:"source_ip"`
	Text      string     `json:"text"`
	Taken     bool       `json:"taken"`
	TakenAt   *time.Time `json:"taken_at"`
	TakenBy   string     `json:"taken_by"`
	Files     []File     `json:"files"`
}

func (it *Item) clone() *Item {
	cp := *it
	cp.Files = make([]File, len(it.Files))
	copy(cp.Files, it.Files)
	return &cp
}

func (it *Item) match(needleLower string) bool {
	if strings.Contains(strings.ToLower(it.Text), needleLower) {
		return true
	}
	for _, f := range it.Files {
		// RelPath 覆盖完整路径，目录名也能被搜到；Name 兜底防 Path 与 Name 不一致。
		if strings.Contains(strings.ToLower(f.RelPath()), needleLower) ||
			strings.Contains(strings.ToLower(f.Name), needleLower) {
			return true
		}
	}
	return false
}

// Options 是存储的可调参数。FreeSpace 为空时使用真实磁盘查询。
type Options struct {
	MaxFileBytes int64
	MinFreeBytes int64
	MaxTextBytes int
	FreeSpace    func(path string) (int64, error)
}

// Store 是内容存储。itemsRoot 是 items 目录（data/items）。
type Store struct {
	itemsRoot string
	opts      Options
	mu        sync.RWMutex
	index     map[string]*Item
}

// Open 创建目录结构并扫描已有内容到内存索引。
func Open(itemsRoot string, opts Options) (*Store, error) {
	if opts.FreeSpace == nil {
		opts.FreeSpace = diskFree
	}
	if err := os.MkdirAll(itemsRoot, 0o750); err != nil {
		return nil, fmt.Errorf("创建数据目录 %s 失败: %w", itemsRoot, err)
	}
	s := &Store{itemsRoot: itemsRoot, opts: opts, index: map[string]*Item{}}
	entries, err := os.ReadDir(itemsRoot)
	if err != nil {
		return nil, fmt.Errorf("读取数据目录失败: %w", err)
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		id := e.Name()
		it, err := readMeta(filepath.Join(itemsRoot, id, "meta.json"))
		if err != nil {
			log.Printf("warn: 跳过损坏的内容 %s: %v", id, err)
			continue
		}
		if it.ID == "" {
			it.ID = id
		}
		if it.Files == nil {
			it.Files = []File{}
		}
		s.index[it.ID] = it
	}
	return s, nil
}

func (s *Store) itemDir(id string) string { return filepath.Join(s.itemsRoot, id) }

func (s *Store) filesDir(id string) string { return filepath.Join(s.itemsRoot, id, "files") }

func readMeta(path string) (*Item, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var it Item
	if err := json.Unmarshal(raw, &it); err != nil {
		return nil, err
	}
	return &it, nil
}

// saveMeta 原子写 meta.json：临时文件 → fsync → rename。调用方必须持有写锁。
func (s *Store) saveMeta(it *Item) error {
	dir := s.itemDir(it.ID)
	raw, err := json.MarshalIndent(it, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化元数据失败: %w", err)
	}
	tmp := filepath.Join(dir, "meta.json.tmp")
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o640)
	if err != nil {
		return fmt.Errorf("写入元数据失败: %w", err)
	}
	if _, err := f.Write(raw); err != nil {
		f.Close()
		os.Remove(tmp)
		return fmt.Errorf("写入元数据失败: %w", err)
	}
	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(tmp)
		return fmt.Errorf("同步元数据失败: %w", err)
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("关闭元数据失败: %w", err)
	}
	if err := os.Rename(tmp, filepath.Join(dir, "meta.json")); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("替换元数据失败: %w", err)
	}
	return nil
}

// NewID 生成按时间有序的 ID：13 位毫秒时间戳 + 8 位随机十六进制。
func NewID() string {
	var b [4]byte
	for i := range b {
		b[i] = byte(rand.UintN(256))
	}
	return fmt.Sprintf("%013d-%s", time.Now().UnixMilli(), hex.EncodeToString(b[:]))
}

// sortedIDsLocked 返回按 ID 倒序（最新在前）的全部 ID。调用方必须持有读锁。
func (s *Store) sortedIDsLocked() []string {
	ids := make([]string, 0, len(s.index))
	for id := range s.index {
		ids = append(ids, id)
	}
	sort.Sort(sort.Reverse(sort.StringSlice(ids)))
	return ids
}
