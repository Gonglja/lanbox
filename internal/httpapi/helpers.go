package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"

	"lanbox/internal/store"
)

// errorBody 是统一的错误响应体。
type errorBody struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// writeJSON 写出 JSON 响应。
func writeJSON(w http.ResponseWriter, status int, v any) {
	if v == nil {
		w.WriteHeader(status)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// writeError 写出统一错误响应。
func writeError(w http.ResponseWriter, status int, code, msg string) {
	var eb errorBody
	eb.Error.Code = code
	eb.Error.Message = msg
	writeJSON(w, status, eb)
}

// writeStoreErr 把 store 的领域错误映射为状态码与中文消息。
func (s *Server) writeStoreErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "内容不存在")
	case errors.Is(err, store.ErrFileNotFound):
		writeError(w, http.StatusNotFound, "file_not_found", "文件不存在")
	case errors.Is(err, store.ErrTooLarge):
		writeError(w, http.StatusRequestEntityTooLarge, "file_too_large",
			fmt.Sprintf("文件超过 %dMB 上限", s.current().MaxFileSizeMB))
	case errors.Is(err, store.ErrNoSpace):
		writeError(w, http.StatusInsufficientStorage, "no_space",
			fmt.Sprintf("磁盘空间不足，需保留至少 %dMB 空闲", s.current().MinFreeDiskMB))
	case errors.Is(err, store.ErrTooManyFiles):
		writeError(w, http.StatusBadRequest, "too_many_files",
			fmt.Sprintf("附件数量不能超过 %d 个", s.store.MaxFiles()))
	case errors.Is(err, store.ErrBadPath):
		writeError(w, http.StatusBadRequest, "bad_path", "附件路径不合法")
	case errors.Is(err, store.ErrItemTooLarge):
		writeError(w, http.StatusRequestEntityTooLarge, "item_too_large",
			fmt.Sprintf("单条内容超过 %dMB 上限", s.store.MaxItemBytes()>>20))
	case errors.Is(err, store.ErrTextTooLong):
		writeError(w, http.StatusBadRequest, "text_too_long",
			fmt.Sprintf("文本超过 %d 字节上限", s.current().MaxTextBytes))
	default:
		s.log.Printf("error: %v", err)
		writeError(w, http.StatusInternalServerError, "internal", "服务器内部错误")
	}
}

// clientIP 返回直连客户端 IP（不信任 X-Forwarded-For，避免伪造来源标注）。
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// decodeJSON 读取并解析有大小上限的 JSON 请求体。
func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	limited := http.MaxBytesReader(w, r.Body, 1<<20)
	if err := json.NewDecoder(limited).Decode(dst); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "请求内容格式不正确")
		return false
	}
	return true
}

// uploadPath 从 multipart 字段名解析附件相对路径。
// 新前端/CLI 用 "files:<相对路径>"；旧前端用 "files" + filename。
// 返回 isFile=false 表示这不是文件字段。
func uploadPath(field, filename string) (path string, isFile bool) {
	if rel, ok := strings.CutPrefix(field, "files:"); ok {
		return rel, true // rel 为空也交给 store 判错
	}
	if filename != "" {
		return filename, true
	}
	return "", false
}

// attachmentHeader 构造同时兼容老浏览器与 UTF-8 文件名的 Content-Disposition。
func attachmentHeader(name string) string {
	ascii := strings.Map(func(r rune) rune {
		if r < 0x20 || r > 0x7e || r == '"' || r == '\\' {
			return '_'
		}
		return r
	}, name)
	return fmt.Sprintf("attachment; filename=%q; filename*=UTF-8''%s", ascii, rfc5987(name))
}

// rfc5987 按 RFC 5987 对文件名做百分号编码。
func rfc5987(s string) string {
	var b strings.Builder
	for _, c := range []byte(s) {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9',
			c == '-', c == '.', c == '_', c == '~':
			b.WriteByte(c)
		default:
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}
