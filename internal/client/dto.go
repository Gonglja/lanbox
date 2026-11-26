// Package client 是 lanbox 服务的命令行客户端实现：
// 只认线上 JSON 契约，不依赖 internal/store 或 internal/httpapi。
package client

import "time"

// File 是一个附件的线上表示。
type File struct {
	ID        string `json:"id"`
	Path      string `json:"path"`
	Name      string `json:"name"`
	Size      int64  `json:"size"`
	SHA256    string `json:"sha256"`
	Downloads int64  `json:"downloads"`
}

// RelPath 返回附件的相对路径，兼容旧服务端（只有 name）。
func (f File) RelPath() string {
	if f.Path != "" {
		return f.Path
	}
	return f.Name
}

// Item 是一条内容。
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

// TotalSize 返回全部附件字节数。
func (it Item) TotalSize() int64 {
	var n int64
	for _, f := range it.Files {
		n += f.Size
	}
	return n
}

// User 是当前登录身份。
type User struct {
	Username string `json:"username"`
	IsAdmin  bool   `json:"is_admin"`
}

// ListResult 是列表分页结果。
type ListResult struct {
	Items      []Item `json:"items"`
	NextCursor string `json:"next_cursor"`
}

// Session 是登录成功后本地保存的会话。
type Session struct {
	URL      string    `json:"url"`
	Username string    `json:"username"`
	SID      string    `json:"sid"`
	SavedAt  time.Time `json:"saved_at"`
	// PinFP 是 mDNS 自动发现时固定的服务端证书指纹（sha256 hex，可空）。
	PinFP string `json:"pin_fp,omitempty"`
}

// Error 是服务端返回的结构化错误。
type Error struct {
	Status  int
	Code    string
	Message string
}

func (e *Error) Error() string { return e.Message }
