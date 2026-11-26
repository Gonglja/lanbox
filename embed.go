// Package lanbox 只承担一件事：把 web/ 目录编进二进制。
package lanbox

import (
	"embed"
	"io/fs"
)

//go:embed all:web
var webFiles embed.FS

// WebFS 返回以 web/ 为根的只读文件系统，供 httpapi 提供静态资源。
func WebFS() fs.FS {
	sub, err := fs.Sub(webFiles, "web")
	if err != nil {
		panic(err) // 只可能是编程错误：embed 路径写错
	}
	return sub
}
