package httpapi

import (
	"net/http"
	"os"
	"strconv"
)

// handleDownload 流式下发附件，支持 Range 断点续传。
func (s *Server) handleDownload(w http.ResponseWriter, r *http.Request) {
	id, fid := r.PathValue("id"), r.PathValue("fid")
	path, f, err := s.store.FilePath(id, fid)
	if err != nil {
		s.writeStoreErr(w, err)
		return
	}
	fh, err := os.Open(path)
	if err != nil {
		s.log.Printf("warn: 打开附件 %s 失败: %v", path, err)
		writeError(w, http.StatusNotFound, "file_missing", "文件已不存在")
		return
	}
	defer fh.Close()
	st, err := fh.Stat()
	if err != nil {
		s.writeStoreErr(w, err)
		return
	}

	h := w.Header()
	h.Set("Content-Type", "application/octet-stream")
	h.Set("Content-Disposition", attachmentHeader(f.Name))
	h.Set("Content-Length", strconv.FormatInt(st.Size(), 10))
	h.Set("Cache-Control", "no-store")

	http.ServeContent(w, r, "", st.ModTime(), fh)

	if err := s.store.IncDownloads(id, fid); err != nil {
		s.log.Printf("warn: 下载计数失败 %s/%s: %v", id, fid, err)
	}
}
