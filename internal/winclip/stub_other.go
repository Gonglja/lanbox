//go:build !windows

// Package winclip 在非 Windows 平台只提供编译桩：
// 客户端本体仅在 Windows 上构建，这里保证 `go vet ./...` / `go build ./...`
// 在其它平台仍可运行。
package winclip

import (
	"errors"

	"lanbox/internal/agent"
)

var errUnsupported = errors.New("剪贴板能力仅在 Windows 上可用")

// Watcher 是监听器桩。
type Watcher struct{}

// NewWatcher 返回桩。
func NewWatcher(logger func(format string, args ...any)) *Watcher { return &Watcher{} }

// Start 恒返回不支持。
func (w *Watcher) Start(ch chan<- agent.Clip) error { return errUnsupported }

// Close 空实现。
func (w *Watcher) Close() {}

// Read 恒返回不支持。
func Read() (agent.Clip, error) { return agent.Clip{}, errUnsupported }

// DIBToPNG 恒返回不支持。
func DIBToPNG(raw []byte) ([]byte, error) { return nil, errUnsupported }

// Protect 恒返回不支持。
func Protect(plain []byte) ([]byte, error) { return nil, errUnsupported }

// Unprotect 恒返回不支持。
func Unprotect(secret []byte) ([]byte, error) { return nil, errUnsupported }

// PickFiles 恒返回不支持。
func PickFiles(title string) ([]string, error) { return nil, errUnsupported }

// PickFolder 恒返回不支持。
func PickFolder(title string) (string, error) { return "", errUnsupported }
