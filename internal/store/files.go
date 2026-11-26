package store

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"math/rand/v2"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// AddFile 将 r 流式写入该内容的 files 目录，同时计算 sha256。
// 内存占用恒定，与文件大小无关。
// 调用约定：同一 item 的 AddFile 必须串行调用（httpapi 单次上传内顺序处理），
// 且不存在向已有内容追加附件的 API，因此不会出现并发合并冲突。
func (s *Store) AddFile(itemID, name string, declaredSize int64, r io.Reader) (*Item, error) {
	clean := SanitizeName(name)

	s.mu.RLock()
	cur, ok := s.index[itemID]
	var count int
	if ok {
		count = len(cur.Files)
	}
	s.mu.RUnlock()
	if !ok {
		return nil, ErrNotFound
	}
	if count >= MaxFilesPerItem {
		return nil, ErrTooManyFiles
	}

	if s.opts.FreeSpace != nil {
		free, err := s.opts.FreeSpace(s.itemsRoot)
		if err != nil {
			return nil, fmt.Errorf("检查磁盘空间失败: %w", err)
		}
		need := s.opts.MinFreeBytes
		if declaredSize > 0 {
			need += declaredSize
		}
		if free < need {
			return nil, ErrNoSpace
		}
	}

	var rb [4]byte
	for i := range rb {
		rb[i] = byte(rand.UintN(256))
	}
	fid := "f" + hex.EncodeToString(rb[:])
	diskPath := filepath.Join(s.filesDir(itemID), fid+"__"+clean)

	f, err := os.OpenFile(diskPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o640)
	if err != nil {
		return nil, fmt.Errorf("创建附件文件失败: %w", err)
	}
	hasher := sha256.New()
	max := s.opts.MaxFileBytes
	var src io.Reader = r
	if max > 0 {
		src = io.LimitReader(r, max+1)
	}
	n, copyErr := io.Copy(io.MultiWriter(f, hasher), src)
	closeErr := f.Close()
	if copyErr != nil {
		os.Remove(diskPath)
		return nil, fmt.Errorf("写入附件失败: %w", copyErr)
	}
	if closeErr != nil {
		os.Remove(diskPath)
		return nil, fmt.Errorf("保存附件失败: %w", closeErr)
	}
	if max > 0 && n > max {
		os.Remove(diskPath)
		return nil, ErrTooLarge
	}

	file := File{ID: fid, Name: clean, Size: n, SHA256: hex.EncodeToString(hasher.Sum(nil))}

	s.mu.Lock()
	it, ok := s.index[itemID]
	if !ok {
		s.mu.Unlock()
		os.Remove(diskPath)
		return nil, ErrNotFound
	}
	cp := it.clone()
	cp.Files = append(cp.Files, file)
	if err := s.saveMeta(cp); err != nil {
		s.mu.Unlock()
		os.Remove(diskPath)
		return nil, err
	}
	s.index[itemID] = cp
	s.mu.Unlock()
	return cp.clone(), nil
}

// FilePath 返回附件的磁盘路径与元数据。文件 ID 必须在 meta 中登记，因此不存在路径穿越。
func (s *Store) FilePath(itemID, fileID string) (string, *File, error) {
	if strings.ContainsAny(itemID, `/\`) || itemID == "" {
		return "", nil, ErrNotFound
	}
	s.mu.RLock()
	it, ok := s.index[itemID]
	var found *File
	if ok {
		for i := range it.Files {
			if it.Files[i].ID == fileID {
				f := it.Files[i]
				found = &f
				break
			}
		}
	}
	s.mu.RUnlock()
	if !ok {
		return "", nil, ErrNotFound
	}
	if found == nil {
		return "", nil, ErrFileNotFound
	}
	// 磁盘名一直是 <文件ID>__<basename>；旧数据没有 path，name 也非空，能直接命中。
	name := found.Name
	if name == "" {
		name = path.Base(found.RelPath())
	}
	p := filepath.Join(s.filesDir(itemID), found.ID+"__"+name)
	return p, found, nil
}

// IncDownloads 累加附件下载次数。
func (s *Store) IncDownloads(itemID, fileID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	it, ok := s.index[itemID]
	if !ok {
		return ErrNotFound
	}
	cp := it.clone()
	hit := false
	for i := range cp.Files {
		if cp.Files[i].ID == fileID {
			cp.Files[i].Downloads++
			hit = true
			break
		}
	}
	if !hit {
		return ErrFileNotFound
	}
	if err := s.saveMeta(cp); err != nil {
		return err
	}
	s.index[itemID] = cp
	return nil
}

// DeleteFile 删除单个附件。
func (s *Store) DeleteFile(itemID, fileID string) (*Item, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	it, ok := s.index[itemID]
	if !ok {
		return nil, ErrNotFound
	}
	cp := it.clone()
	idx := -1
	for i := range cp.Files {
		if cp.Files[i].ID == fileID {
			idx = i
			break
		}
	}
	if idx < 0 {
		return nil, ErrFileNotFound
	}
	diskPath := filepath.Join(s.filesDir(itemID), cp.Files[idx].ID+"__"+cp.Files[idx].Name)
	cp.Files = append(cp.Files[:idx], cp.Files[idx+1:]...)
	if err := s.saveMeta(cp); err != nil {
		return nil, err
	}
	s.index[itemID] = cp
	if err := os.Remove(diskPath); err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("删除附件文件失败: %w", err)
	}
	return cp.clone(), nil
}

// SanitizeName 清洗用户提交的文件名：仅用于展示与磁盘后缀，绝不参与目录选择。
func SanitizeName(name string) string {
	// 统一分隔符后取最后一段，彻底消除 ../ 之类
	flat := strings.ReplaceAll(name, "\\", "/")
	flat = path.Base(flat)
	var b strings.Builder
	for _, r := range flat {
		if r < 0x20 || r == 0x7f {
			continue
		}
		if strings.ContainsRune(`/\:*?"<>|`, r) {
			continue
		}
		b.WriteRune(r)
	}
	out := strings.TrimSpace(b.String())
	out = strings.TrimLeft(out, ".") // 去掉前导点，避免隐藏文件与 ".."
	out = strings.TrimSpace(out)
	if out == "" || out == "." || out == ".." {
		return "未命名文件"
	}
	const maxRunes = 120
	runes := []rune(out)
	if len(runes) > maxRunes {
		ext := path.Ext(out)
		if len([]rune(ext)) > 20 || ext == out {
			ext = ""
		}
		keep := maxRunes - len([]rune(ext))
		out = string(runes[:keep]) + ext
	}
	return out
}
