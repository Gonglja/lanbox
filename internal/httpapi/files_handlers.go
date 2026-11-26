package httpapi

import (
	"net/http"
	"os"
	"strconv"
)

// handleDownload 流式下发附件，支持 Range 断点续传。
//
// 默认按附件下载（application/octet-stream + attachment）。
// 带 ?inline=1 且扩展名在白名单内时改为内联展示（正确的 MIME + inline），
// 供网页里的图片/视频预览使用；白名单外的类型仍回退为附件下载。
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
	previewCT, asInline := "", false
	if inlineRequested(r) {
		previewCT, asInline = inlineType(f.Name)
	}
	switch {
	case asInline:
		h.Set("Content-Type", previewCT)
		h.Set("Content-Disposition", dispositionHeader("inline", f.Name))
		if previewCT == "image/svg+xml" {
			// SVG 可以内嵌脚本：直接在新标签打开时用沙箱 CSP 兜底（<img> 里本就不执行脚本）。
			h.Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; img-src data:; sandbox")
		}
		// 预览请求频繁（滚动、SSE 重绘、视频 Range），允许浏览器短暂复用；
		// 附件 ID 每次上传都是新的，不会读到过期内容。
		h.Set("Cache-Control", "private, max-age=300")
	default:
		h.Set("Content-Type", "application/octet-stream")
		h.Set("Content-Disposition", attachmentHeader(f.Name))
		h.Set("Cache-Control", "no-store")
	}
	h.Set("Content-Length", strconv.FormatInt(st.Size(), 10))

	http.ServeContent(w, r, "", st.ModTime(), fh)

	// 预览（含视频 Range 分段）不计入「下载次数」。
	if asInline {
		return
	}
	if err := s.store.IncDownloads(id, fid); err != nil {
		s.log.Printf("warn: 下载计数失败 %s/%s: %v", id, fid, err)
	}
}
