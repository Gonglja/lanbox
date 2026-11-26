package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Pipeline 把 Clip 按固定规则变成 Decision。所有方法并发安全。
type Pipeline struct {
	mu        sync.Mutex
	settings  Settings
	available bool // 已登录且服务端可用
	seen      map[string]time.Time
	now       func() time.Time
	stat      statFn
	walk      walkFn
}

// NewPipeline 用初始配置构造决策流水线。
func NewPipeline(s Settings) *Pipeline {
	return &Pipeline{
		settings: s,
		seen:     map[string]time.Time{},
		now:      time.Now,
		stat:     os.Stat,
		walk:     filepath.WalkDir,
	}
}

// SetSettings 替换配置。
func (p *Pipeline) SetSettings(s Settings) {
	p.mu.Lock()
	p.settings = s
	p.mu.Unlock()
}

// Settings 返回当前配置快照。
func (p *Pipeline) Settings() Settings {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.settings
}

// SetAvailable 标记服务端是否可用（已登录）。不可用时自动上传整体暂停，
// 但仍然不读剪贴板，由调用方在不可用时关闭监听。
func (p *Pipeline) SetAvailable(ok bool) {
	p.mu.Lock()
	p.available = ok
	p.mu.Unlock()
}

// Decide 对一次剪贴板内容做自动上传决策。规则按序短路：
//
//  1. 总开关关 / 已暂停 / 未登录 → 跳过
//  2. 源声明忽略（Clipboard Viewer Ignore）→ 跳过
//  3. 类型优先级：文件 > 图片 > 文本（同时有文件与文本时文本丢弃）
//  4. 对应类型开关关闭 → 跳过
//  5. 去重窗口命中 → 跳过
//  6. 文本为空 / 超上限 → 跳过
//  7. 文件不存在 / 超数量或总量 → 跳过
//  8. 图片超上限 → 跳过
func (p *Pipeline) Decide(c Clip) Decision {
	p.mu.Lock()
	s := p.settings
	available := p.available
	p.mu.Unlock()

	switch {
	case !s.Enabled:
		return skipped("剪贴板自动上传已关闭")
	case s.Paused:
		return skipped("已暂停")
	case !available:
		return skipped("未连接服务器")
	}
	if c.Ignore {
		return skipped("剪贴板内容要求忽略（密码管理器等）")
	}
	at := c.At
	if at.IsZero() {
		at = p.now()
	}

	switch {
	case len(c.Paths) > 0:
		if !s.Files {
			return skipped("文件自动上传已关闭")
		}
		files, hash, skippedN, note := p.expandFiles(c.Paths, s)
		if note != "" {
			return skipped(note)
		}
		if len(files) == 0 {
			return skipped("没有可上传的文件")
		}
		d := Decision{Upload: true, Kind: KindFiles, Files: files, Hash: hash, Skipped: skippedN}
		if p.dedupeHit(hash, at, s.DedupeSeconds) {
			return skipped("与最近一次上传内容相同")
		}
		return d

	case len(c.PNG) > 0:
		if !s.Images {
			return skipped("图片自动上传已关闭")
		}
		if s.MaxImageMB > 0 && len(c.PNG) > s.MaxImageMB<<20 {
			return skipped(fmt.Sprintf("图片超过 %dMB 上限", s.MaxImageMB))
		}
		sum := sha256.Sum256(c.PNG)
		hash := "i:" + hex.EncodeToString(sum[:])
		if p.dedupeHit(hash, at, s.DedupeSeconds) {
			return skipped("与最近一次上传内容相同")
		}
		return Decision{Upload: true, Kind: KindImage, Files: []File{{
			Rel: "clipboard-" + at.Format("20060102-150405") + ".png", Size: int64(len(c.PNG)), Data: c.PNG,
		}}, Hash: hash}

	default:
		if strings.TrimSpace(c.Text) == "" {
			return skipped("剪贴板为空")
		}
		if !s.Text {
			return skipped("文本自动上传已关闭")
		}
		if s.MaxTextBytes > 0 && len(c.Text) > s.MaxTextBytes {
			return skipped(fmt.Sprintf("文本超过 %d 字节上限", s.MaxTextBytes))
		}
		sum := sha256.Sum256([]byte(c.Text))
		hash := "t:" + hex.EncodeToString(sum[:])
		if p.dedupeHit(hash, at, s.DedupeSeconds) {
			return skipped("与最近一次上传内容相同")
		}
		return Decision{Upload: true, Kind: KindText, Text: c.Text, Hash: hash}
	}
}

// Build 把手动提交的文本与路径整理成上传内容（不查开关、不去重，但仍守限额）。
func (p *Pipeline) Build(text string, paths []string) Decision {
	p.mu.Lock()
	s := p.settings
	p.mu.Unlock()

	if strings.TrimSpace(text) == "" && len(paths) == 0 {
		return skipped("内容为空")
	}
	var files []File
	skippedN := 0
	if len(paths) > 0 {
		var note string
		files, _, skippedN, note = p.expandFiles(paths, s)
		if note != "" {
			return skipped(note)
		}
	}
	if s.MaxTextBytes > 0 && len(text) > s.MaxTextBytes {
		return skipped(fmt.Sprintf("文本超过 %d 字节上限", s.MaxTextBytes))
	}
	kind := KindText
	switch {
	case len(files) > 0:
		kind = KindFiles
	case len(text) > 0:
		kind = KindText
	}
	return Decision{Upload: true, Kind: kind, Text: text, Files: files, Skipped: skippedN}
}

func skipped(reason string) Decision { return Decision{Skip: reason} }

// dedupeHit 记录内容哈希并判断是否命中窗口。调用方已持锁或传入快照，
// 这里只在 Pipeline 的锁内修改 map。
func (p *Pipeline) dedupeHit(hash string, at time.Time, windowSec int) bool {
	if windowSec <= 0 {
		return false
	}
	window := time.Duration(windowSec) * time.Second
	p.mu.Lock()
	defer p.mu.Unlock()
	// 顺手清理过期条目，避免长时间运行时 map 无限增长。
	for k, t := range p.seen {
		if at.Sub(t) >= window {
			delete(p.seen, k)
		}
	}
	if t, ok := p.seen[hash]; ok && at.Sub(t) < window {
		return true
	}
	if len(p.seen) > 64 {
		// 极端情况（时钟回拨等）：整体清空，宁可多传一次也不无限增长。
		clear(p.seen)
	}
	p.seen[hash] = at
	return false
}

// expandFiles 把路径列表展开成附件列表（文件夹递归），并计算 内容哈希。
// 返回的 hash 由 每个文件的相对路径 + 大小 + 修改时间 组成，保证"同样的文件
// 重新复制一次"仍能去重，而内容变化后不会被误判为重复。
func (p *Pipeline) expandFiles(paths []string, s Settings) (files []File, hash string, skippedN int, note string) {
	seenPath := map[string]bool{}
	var parts []string
	var total int64
	for _, raw := range paths {
		abs, err := filepath.Abs(raw)
		if err != nil {
			skippedN++
			continue
		}
		if seenPath[abs] {
			continue
		}
		seenPath[abs] = true
		info, err := p.stat(abs)
		if err != nil {
			skippedN++
			continue
		}
		if info.IsDir() {
			base := filepath.Base(abs)
			walkErr := p.walk(abs, func(fp string, d fs.DirEntry, err error) error {
				if err != nil {
					skippedN++
					return nil // 跳过无权限的条目，继续遍历
				}
				if d.IsDir() {
					return nil
				}
				if d.Type()&fs.ModeSymlink != 0 {
					skippedN++
					return nil
				}
				fi, err := d.Info()
				if err != nil {
					skippedN++
					return nil
				}
				rel, err := filepath.Rel(abs, fp)
				if err != nil {
					skippedN++
					return nil
				}
				relSlash := path.Join(base, filepath.ToSlash(rel))
				parts = append(parts, fmt.Sprintf("%s|%d|%d", relSlash, fi.Size(), fi.ModTime().UnixNano()))
				files = append(files, File{Path: fp, Rel: relSlash, Size: fi.Size()})
				total += fi.Size()
				return nil
			})
			if walkErr != nil {
				skippedN++
			}
			continue
		}
		if info.Mode()&fs.ModeSymlink != 0 {
			skippedN++
			continue
		}
		rel := filepath.Base(abs)
		parts = append(parts, fmt.Sprintf("%s|%d|%d", rel, info.Size(), info.ModTime().UnixNano()))
		files = append(files, File{Path: abs, Rel: rel, Size: info.Size()})
		total += info.Size()
	}
	if len(files) == 0 {
		if skippedN > 0 {
			return nil, "", skippedN, fmt.Sprintf("%d 个文件无法读取", skippedN)
		}
		return nil, "", 0, "没有可上传的文件"
	}
	if s.MaxFiles > 0 && len(files) > s.MaxFiles {
		return nil, "", skippedN, fmt.Sprintf("附件数量 %d 超过 %d 个上限", len(files), s.MaxFiles)
	}
	if s.MaxTotalMB > 0 && total > int64(s.MaxTotalMB)<<20 {
		return nil, "", skippedN, fmt.Sprintf("附件总大小 %.1fMB 超过 %dMB 上限", float64(total)/(1<<20), s.MaxTotalMB)
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Rel < files[j].Rel })
	sort.Strings(parts)
	sum := sha256.Sum256([]byte(strings.Join(parts, "\n")))
	return files, "f:" + hex.EncodeToString(sum[:]), skippedN, ""
}
