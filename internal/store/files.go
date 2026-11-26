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
	"strconv"
	"strings"
)

// diskName 返回附件在 files/ 下的磁盘文件名：<文件ID>__<basename>。
// 写（AddFile）、读（FilePath）、删（DeleteFile）共用，避免三处各自拼装后走偏。
// 极端情况下 Path 与 Name 都为空（外部写入的 meta），退化为 <fid>__<fid>。
func diskName(f File) string {
	name := f.Name
	if name == "" {
		name = path.Base(f.RelPath())
	}
	return f.ID + "__" + name
}

// MaxFiles 返回生效的单条内容附件数量上限，供 httpapi 生成提示文案。
func (s *Store) MaxFiles() int { return s.maxFiles() }

// maxFiles 返回生效的附件数量上限。
func (s *Store) maxFiles() int {
	if s.opts.MaxFiles > 0 {
		return s.opts.MaxFiles
	}
	return DefaultMaxFiles
}

// newFileID 生成附件 ID。
func newFileID() string {
	var b [4]byte
	for i := range b {
		b[i] = byte(rand.UintN(256))
	}
	return "f" + hex.EncodeToString(b[:])
}

// uniqueRelPath 在已占用路径集合里为 p 找一个不冲突的路径，候选用完时返回空串。
// limit 是候选编号的上界，由调用方按附件数量上限传入，避免退化成无界循环。
// 比较大小写不敏感：Windows/macOS 解压时 A.txt 与 a.txt 会互相覆盖。
func uniqueRelPath(taken map[string]bool, p string, limit int) string {
	if !taken[strings.ToLower(p)] {
		return p
	}
	dir, base := path.Split(p)
	ext := path.Ext(base)
	stem := strings.TrimSuffix(base, ext)
	for i := 2; i <= limit; i++ {
		cand := dir + stem + "~" + strconv.Itoa(i) + ext
		if !taken[strings.ToLower(cand)] {
			return cand
		}
	}
	return ""
}

// AddFile 将 r 流式写入该内容的 files 目录，同时计算 sha256。
// relPath 是内容内的相对路径，写入前经 SanitizeRelPath 清洗；
// 磁盘布局仍是 files/<fid>__<basename>，与旧数据保持一致。
// 调用约定：同一 item 的 AddFile 必须串行调用。
func (s *Store) AddFile(itemID, relPath string, declaredSize int64, r io.Reader) (*Item, error) {
	cleanPath, err := SanitizeRelPath(relPath)
	if err != nil {
		return nil, err
	}

	s.mu.RLock()
	cur, ok := s.index[itemID]
	var count int
	var used int64
	taken := map[string]bool{}
	if ok {
		count = len(cur.Files)
		for _, f := range cur.Files {
			used += f.Size
			taken[strings.ToLower(f.RelPath())] = true
		}
	}
	s.mu.RUnlock()
	if !ok {
		return nil, ErrNotFound
	}
	if count >= s.maxFiles() {
		return nil, ErrTooManyFiles
	}

	hasItemLimit := s.opts.MaxItemBytes > 0
	var remaining int64
	if hasItemLimit {
		remaining = s.opts.MaxItemBytes - used
		if remaining <= 0 {
			return nil, ErrItemTooLarge
		}
	}

	finalPath := uniqueRelPath(taken, cleanPath, s.maxFiles()+1)
	if finalPath == "" {
		return nil, ErrTooManyFiles
	}
	base := path.Base(finalPath)

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

	fid := newFileID()
	diskPath := filepath.Join(s.filesDir(itemID), diskName(File{ID: fid, Path: finalPath, Name: base}))

	f, err := os.OpenFile(diskPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o640)
	if err != nil {
		return nil, fmt.Errorf("创建附件文件失败: %w", err)
	}
	hasher := sha256.New()

	// 读取上限取单文件上限与单条剩余额度的较小值，超限时立即中止。
	limit := s.opts.MaxFileBytes
	if hasItemLimit && (limit <= 0 || remaining < limit) {
		limit = remaining
	}
	var src io.Reader = r
	if limit > 0 {
		src = io.LimitReader(r, limit+1)
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
	if s.opts.MaxFileBytes > 0 && n > s.opts.MaxFileBytes {
		os.Remove(diskPath)
		return nil, ErrTooLarge
	}
	if hasItemLimit && n > remaining {
		os.Remove(diskPath)
		return nil, ErrItemTooLarge
	}

	file := File{
		ID:     fid,
		Path:   finalPath,
		Name:   base,
		Size:   n,
		SHA256: hex.EncodeToString(hasher.Sum(nil)),
	}

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
	p := filepath.Join(s.filesDir(itemID), diskName(*found))
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
	diskPath := filepath.Join(s.filesDir(itemID), diskName(cp.Files[idx]))
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
