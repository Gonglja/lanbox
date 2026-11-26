// Package agent 是 Windows 客户端的平台无关核心：
// 决定"剪贴板里的什么内容值得上传"（Pipeline），并负责上传队列、
// 退避重试与本地历史（Manager）。
//
// 约束：本包不得 import 任何 Win32、Wails 或 internal/client 包，
// 平台能力一律通过接口注入，以便在任意平台单测。
package agent

import (
	"io/fs"
	"os"
	"time"
)

// Kind 是内容类型。
type Kind string

const (
	KindText  Kind = "text"
	KindFiles Kind = "files"
	KindImage Kind = "image"
)

// Clip 是内容源（Windows 剪贴板）读出的一次内容。
// 同一时刻可能同时带文本与文件（资源管理器复制文件时两者都有），
// 由 Pipeline 按 "文件 > 图片 > 文本" 的优先级取舍。
type Clip struct {
	At     time.Time
	Ignore bool     // 源声明忽略（剪贴板带 Clipboard Viewer Ignore 格式）
	Text   string   // CF_UNICODETEXT
	Paths  []string // CF_HDROP 里的绝对路径（文件或文件夹）
	PNG    []byte   // CF_DIB 已转成的 PNG
	Source string   // 来源进程名，仅用于展示与日志
}

// Settings 是剪贴板相关配置。
type Settings struct {
	Enabled       bool // 总开关
	Paused        bool // 暂停（持久化）
	Text          bool // 自动上传文本
	Files         bool // 自动上传文件/文件夹
	Images        bool // 自动上传图片
	DedupeSeconds int  // 去重窗口，0 = 不去重
	MaxTextBytes  int  // 文本上限
	MaxImageMB    int  // 图片上限（MB）
	MaxTotalMB    int  // 单条附件总量上限（MB），<=0 表示不限
	MaxFiles      int  // 单条附件数量上限，<=0 表示不限
	TextPrefix    string
}

// File 是待上传的一个附件。
type File struct {
	Path string // 本机绝对路径，供上传层打开
	Rel  string // 上传后的相对路径，如 "素材/a.png"
	Size int64
	// Data 非空时直接上传内存内容（图片来自剪贴板，没有磁盘路径）。
	Data []byte
}

// Decision 是一次决策的结果。Upload 为 false 时 Skip 说明原因（中文，可直接展示）。
type Decision struct {
	Upload  bool
	Skip    string
	Kind    Kind
	Text    string
	Files   []File
	Hash    string
	Skipped int // 展开目录时被跳过的文件数（权限不足/符号链接等）
}

// statFn/walkFn 便于测试注入。
type statFn func(string) (os.FileInfo, error)

type walkFn func(root string, fn fs.WalkDirFunc) error
