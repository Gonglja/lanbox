package httpapi

import (
	"net/http"
	"path"
	"strings"
)

// inlineTypes 是允许在浏览器内联展示（预览）的扩展名 → MIME 映射。
// 白名单之外的类型一律回退为附件下载，避免把未知内容当网页执行。
var inlineTypes = map[string]string{
	// 图片
	".png":  "image/png",
	".jpg":  "image/jpeg",
	".jpeg": "image/jpeg",
	".gif":  "image/gif",
	".webp": "image/webp",
	".avif": "image/avif",
	".bmp":  "image/bmp",
	".ico":  "image/x-icon",
	".svg":  "image/svg+xml",
	// 视频
	".mp4":  "video/mp4",
	".m4v":  "video/mp4",
	".webm": "video/webm",
	".ogv":  "video/ogg",
	".mov":  "video/quicktime",
	".mkv":  "video/x-matroska",
	// 音频
	".mp3":  "audio/mpeg",
	".m4a":  "audio/mp4",
	".aac":  "audio/aac",
	".oga":  "audio/ogg",
	".ogg":  "audio/ogg",
	".opus": "audio/ogg",
	".wav":  "audio/wav",
	".flac": "audio/flac",
	".weba": "audio/webm",
}

// inlineType 返回可用于内联预览的 MIME；ok=false 表示只能作为附件下载。
func inlineType(name string) (string, bool) {
	ct, ok := inlineTypes[strings.ToLower(path.Ext(name))]
	return ct, ok
}

// inlineRequested 判断请求是否要求内联展示（?inline=1）。
// 缺省仍是附件下载，保证既有客户端（lanbox-cli、浏览器另存为）行为不变。
func inlineRequested(r *http.Request) bool {
	q := r.URL.Query()
	if !q.Has("inline") {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(q.Get("inline"))) {
	case "0", "false", "no", "off":
		return false
	}
	return true
}
