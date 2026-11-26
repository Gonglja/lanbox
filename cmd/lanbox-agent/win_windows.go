//go:build windows

package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows/registry"

	"lanbox/internal/session"
)

const (
	runKeyPath = `Software\Microsoft\Windows\CurrentVersion\Run`
	runValue   = "lanbox-agent"

	logMaxBytes = 2 << 20 // 单个日志文件 2MB
)

var (
	procCreateMutexW = syscall.NewLazyDLL("kernel32.dll").NewProc("CreateMutexW")
	mutexHandle      uintptr
)

// acquireSingleInstance 用命名互斥量保证只有一个实例在跑（避免重复上传剪贴板）。
func acquireSingleInstance() bool {
	name, err := syscall.UTF16PtrFromString(`Global\lanbox-agent`)
	if err != nil {
		return true
	}
	h, _, errno := procCreateMutexW.Call(0, 0, uintptr(unsafe.Pointer(name)))
	if h == 0 {
		// 创建失败时不阻止启动，交给后续逻辑。
		return true
	}
	if errno == syscall.ERROR_ALREADY_EXISTS {
		return false
	}
	mutexHandle = h
	return true
}

// setAutostart 写入/删除 HKCU 的 Run 项。
func setAutostart(enable bool) error {
	key, err := registry.OpenKey(registry.CURRENT_USER, runKeyPath, registry.SET_VALUE)
	if err != nil {
		return fmt.Errorf("打开注册表失败: %w", err)
	}
	defer key.Close()
	if !enable {
		if err := key.DeleteValue(runValue); err != nil && err != registry.ErrNotExist {
			return fmt.Errorf("删除开机自启失败: %w", err)
		}
		return nil
	}
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("获取程序路径失败: %w", err)
	}
	if err := key.SetStringValue(runValue, `"`+exe+`"`); err != nil {
		return fmt.Errorf("写入开机自启失败: %w", err)
	}
	return nil
}

// setupLog 初始化 %AppData%\lanbox\agent.log（2MB × 2 轮转）。
func setupLog() (*log.Logger, func()) {
	dir, err := session.Dir()
	if err != nil {
		return log.New(os.Stderr, "", log.LstdFlags), func() {}
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return log.New(os.Stderr, "", log.LstdFlags), func() {}
	}
	w := &rotateWriter{path: filepath.Join(dir, "agent.log"), max: logMaxBytes}
	logger := log.New(w, "", log.LstdFlags|log.Lmsgprefix)
	return logger, func() { w.Close() }
}

// rotateWriter 是带大小轮转的日志写入器。
type rotateWriter struct {
	mu   sync.Mutex
	path string
	max  int64
	f    *os.File
	size int64
}

func (w *rotateWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.f == nil {
		f, err := os.OpenFile(w.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
		if err != nil {
			return 0, err
		}
		w.f = f
		if st, err := f.Stat(); err == nil {
			w.size = st.Size()
		}
	}
	if w.size+int64(len(p)) > w.max {
		w.f.Close()
		w.f = nil
		_ = os.Remove(w.path + ".1")
		_ = os.Rename(w.path, w.path+".1")
		f, err := os.OpenFile(w.path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
		if err != nil {
			return 0, err
		}
		w.f = f
		w.size = 0
	}
	n, err := w.f.Write(p)
	w.size += int64(n)
	return n, err
}

func (w *rotateWriter) Close() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.f != nil {
		w.f.Close()
		w.f = nil
	}
}
