package httpapi

import (
	"archive/zip"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
)

// handleDownloadZip 把一条内容的全部附件流式打包成 zip。
// 用 Store 方式（不压缩）：ARM 小主机省 CPU，附件多为已压缩媒体。
// 全程 io.Copy 直通，不在内存里缓存文件内容，也不设置 Content-Length（走 chunked）。
func (s *Server) handleDownloadZip(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	it, err := s.store.Get(id)
	if err != nil {
		s.writeStoreErr(w, err)
		return
	}
	if len(it.Files) == 0 {
		writeError(w, http.StatusBadRequest, "no_files", "这条内容没有附件")
		return
	}

	h := w.Header()
	h.Set("Content-Type", "application/zip")
	h.Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", "lanbox-"+id+".zip"))
	h.Set("Cache-Control", "no-store")

	zw := zip.NewWriter(w)
	// 逐个附件搬进 zip；任一条目失败就中止，避免吐出半截可解压但内容缺失的包。
	for _, f := range it.Files {
		disk, meta, err := s.store.FilePath(id, f.ID)
		if err != nil {
			s.log.Printf("warn: 打包时定位附件 %s/%s 失败: %v", id, f.ID, err)
			return
		}
		fh, err := os.Open(disk)
		if err != nil {
			s.log.Printf("warn: 打包时打开附件 %s 失败: %v", disk, err)
			return
		}
		name := meta.RelPath()
		if !safeZipName(name) {
			name = meta.Name
		}
		dst, err := zw.CreateHeader(&zip.FileHeader{
			Name:     name,
			Method:   zip.Store,
			Modified: it.CreatedAt,
		})
		if err != nil {
			fh.Close()
			s.log.Printf("warn: 写入 zip 条目 %s 失败: %v", name, err)
			return
		}
		if _, err := io.Copy(dst, fh); err != nil {
			fh.Close()
			s.log.Printf("warn: 复制附件 %s 到 zip 失败: %v", name, err)
			return
		}
		fh.Close()
	}
	if err := zw.Close(); err != nil {
		s.log.Printf("warn: 关闭 zip 失败: %v", err)
	}
	s.log.Printf("info: %s 打包下载内容 %s（%d 个附件）", userFrom(r).Username, id, len(it.Files))
}

// safeZipName 拒绝空名、绝对路径与含 ".." 段的条目名。
// RelPath() 的输出已被 SanitizeRelPath 规范化过，这里只做最后一道防御，防止解压时逃出目标目录。
func safeZipName(name string) bool {
	if name == "" || strings.HasPrefix(name, "/") || strings.HasPrefix(name, `\`) {
		return false
	}
	if strings.ContainsRune(name, 0) {
		return false
	}
	for _, seg := range strings.Split(strings.ReplaceAll(name, `\`, "/"), "/") {
		if seg == ".." {
			return false
		}
	}
	return true
}
