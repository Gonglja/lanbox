//go:build windows

package winclip

import (
	"errors"
	"runtime"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"

	"lanbox/internal/agent"
)

const (
	// 单个剪贴板内容最多读取 8MB，超过直接放弃：业务上限远小于此。
	maxClipboardBytes = 8 << 20
	ignoreFormatName  = "Clipboard Viewer Ignore"
	watcherClassName  = "LanboxClipboardWatcher"
)

// 监听线程与 WndProc 之间共享当前 Watcher（进程内只允许一个）。
var (
	current   atomic.Pointer[Watcher]
	ignoreFmt atomic.Uint32
	procThunk = syscall.NewCallback(wndProc)
)

type msgT struct {
	Hwnd    uintptr
	Message uint32
	_       uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	PtX     int32
	PtY     int32
	Private uint32
}

type wndClassExW struct {
	Size       uint32
	Style      uint32
	WndProc    uintptr
	ClsExtra   int32
	WndExtra   int32
	Instance   uintptr
	Icon       uintptr
	Cursor     uintptr
	Background uintptr
	MenuName   *uint16
	ClassName  *uint16
	IconSm     uintptr
}

// Watcher 在独立线程上创建 message-only 窗口监听剪贴板变化。
type Watcher struct {
	ch     chan<- agent.Clip
	logger func(format string, args ...any)

	mu      sync.Mutex
	hwnd    uintptr
	started bool
	ready   chan error
	done    chan struct{}
}

// NewWatcher 构造监听器（尚未启动）。
func NewWatcher(logger func(format string, args ...any)) *Watcher {
	if logger == nil {
		logger = func(string, ...any) {}
	}
	return &Watcher{logger: logger, ready: make(chan error, 1), done: make(chan struct{})}
}

// Start 启动监听线程，成功返回后剪贴板变化会投递到 ch（非阻塞，投递失败则丢弃）。
func (w *Watcher) Start(ch chan<- agent.Clip) error {
	w.ch = ch
	w.mu.Lock()
	w.started = true
	w.mu.Unlock()
	go w.run()
	select {
	case err := <-w.ready:
		return err
	case <-time.After(5 * time.Second):
		return errors.New("剪贴板监听启动超时")
	}
}

// Close 销毁窗口并等待线程退出。未启动或重复调用都安全。
func (w *Watcher) Close() {
	w.mu.Lock()
	started, hwnd := w.started, w.hwnd
	w.mu.Unlock()
	if !started {
		return
	}
	if hwnd != 0 {
		procPostMessageW.Call(hwnd, wmClose, 0, 0)
	}
	select {
	case <-w.done:
	case <-time.After(3 * time.Second):
	}
}

func (w *Watcher) run() {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	defer close(w.done)

	className := utf16Ptr(watcherClassName)
	instance, _, _ := procGetModuleHandleW.Call(0)
	wc := wndClassExW{
		Size:      uint32(unsafe.Sizeof(wndClassExW{})),
		WndProc:   procThunk,
		Instance:  instance,
		ClassName: className,
	}
	if atom, _, err := procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc))); atom == 0 {
		w.ready <- errSys("注册窗口类", err)
		return
	}
	hwnd, _, err := procCreateWindowExW.Call(
		0, uintptr(unsafe.Pointer(className)), 0, 0,
		0, 0, 0, 0, hwndMessage, 0, instance, 0,
	)
	if hwnd == 0 {
		w.ready <- errSys("创建监听窗口", err)
		return
	}
	w.mu.Lock()
	w.hwnd = hwnd
	w.started = true
	w.mu.Unlock()

	if ok, _, err := procAddClipboardFormatListener.Call(hwnd); ok == 0 {
		w.ready <- errSys("注册剪贴板监听", err)
		return
	}
	current.Store(w)
	w.ready <- nil

	var msg msgT
	for {
		r, _, _ := procGetMessageW.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0)
		if int32(r) <= 0 {
			break
		}
		procTranslateMessage.Call(uintptr(unsafe.Pointer(&msg)))
		procDispatchMessageW.Call(uintptr(unsafe.Pointer(&msg)))
	}
	current.CompareAndSwap(w, nil)
	procRemoveClipboardFormatListener.Call(hwnd)
}

func (w *Watcher) onUpdate() {
	clip, err := Read()
	if err != nil {
		w.logger("读取剪贴板失败: %v", err)
		return
	}
	if clip.Ignore || (clip.Text == "" && len(clip.Paths) == 0 && len(clip.PNG) == 0) {
		return
	}
	select {
	case w.ch <- clip:
	default:
		w.logger("剪贴板事件队列已满，丢弃本次变化")
	}
}

func wndProc(hwnd, msg, wparam, lparam uintptr) uintptr {
	switch msg {
	case wmClipboardUpdate:
		if w := current.Load(); w != nil {
			w.onUpdate()
		}
		return 0
	case wmDestroy:
		procPostQuitMessage.Call(0)
		return 0
	}
	r, _, _ := procDefWindowProcW.Call(hwnd, msg, wparam, lparam)
	return r
}

// Read 读取当前剪贴板内容。优先级：文件（CF_HDROP）> 图片（CF_DIB）> 文本。
func Read() (agent.Clip, error) {
	clip, err := readLocked()
	clip.At = time.Now()
	clip.Source = foregroundApp()
	return clip, err
}

func readLocked() (agent.Clip, error) {
	var lastErr error
	for i := 0; i < 5; i++ {
		ok, _, err := procOpenClipboard.Call(0)
		if ok != 0 {
			lastErr = nil
			break
		}
		lastErr = errSys("打开剪贴板", err)
		// 剪贴板被别的进程占用时短暂等待后重试。
		time.Sleep(20 * time.Millisecond)
	}
	if lastErr != nil {
		return agent.Clip{}, lastErr
	}
	defer procCloseClipboard.Call()

	if isAvailable(ignoreFormat()) {
		return agent.Clip{Ignore: true}, nil
	}

	var clip agent.Clip
	if h, err := clipboardData(cfHDrop); err == nil {
		clip.Paths = dropPaths(h)
		if len(clip.Paths) > 0 {
			return clip, nil
		}
	}
	if h, err := clipboardData(cfDIB); err == nil {
		if raw, err := globalBytes(h); err == nil && len(raw) <= maxClipboardBytes {
			if pngBytes, err := DIBToPNG(raw); err == nil {
				clip.PNG = pngBytes
				return clip, nil
			}
		}
	}
	if h, err := clipboardData(cfUnicodeText); err == nil {
		if raw, err := globalBytes(h); err == nil && len(raw) <= maxClipboardBytes {
			clip.Text = readUTF16(bytesFromUint16(raw))
		}
	}
	return clip, nil
}

func clipboardData(format uint32) (uintptr, error) {
	h, _, err := procGetClipboardData.Call(uintptr(format))
	if h == 0 {
		return 0, errSys("读取剪贴板数据", err)
	}
	return h, nil
}

func isAvailable(format uint32) bool {
	ok, _, _ := procIsClipboardFormatAvailable.Call(uintptr(format))
	return ok != 0
}

// ignoreFormat 返回 "Clipboard Viewer Ignore" 的注册格式号（0 表示注册失败）。
func ignoreFormat() uint32 {
	if v := ignoreFmt.Load(); v != 0 {
		return v
	}
	ptr := utf16Ptr(ignoreFormatName)
	v, _, _ := procRegisterClipboardFormatW.Call(uintptr(unsafe.Pointer(ptr)))
	ignoreFmt.Store(uint32(v))
	return uint32(v)
}

// dropPaths 枚举 CF_HDROP 里的文件/文件夹路径。
func dropPaths(h uintptr) []string {
	count, _, _ := procDragQueryFileW.Call(h, 0xFFFFFFFF, 0, 0)
	if count == 0 || count > 10000 {
		return nil
	}
	out := make([]string, 0, count)
	for i := uintptr(0); i < count; i++ {
		n, _, _ := procDragQueryFileW.Call(h, i, 0, 0)
		if n == 0 || n > 32767 {
			continue
		}
		buf := make([]uint16, n+1)
		got, _, _ := procDragQueryFileW.Call(h, i, uintptr(unsafe.Pointer(&buf[0])), n+1)
		if got == 0 {
			continue
		}
		out = append(out, readUTF16(buf))
	}
	return out
}

// foregroundApp 返回当前前台进程名（失败时返回空）。
func foregroundApp() string {
	hwnd, _, _ := procGetForegroundWindow.Call()
	if hwnd == 0 {
		return ""
	}
	var pid uint32
	procGetWindowThreadProcessID.Call(hwnd, uintptr(unsafe.Pointer(&pid)))
	if pid == 0 {
		return ""
	}
	h, _, _ := procOpenProcess.Call(processQueryLimitedInformation, 0, uintptr(pid))
	if h == 0 {
		return ""
	}
	defer procCloseHandle.Call(h)
	buf := make([]uint16, syscall.MAX_LONG_PATH)
	size := uint32(len(buf))
	ok, _, _ := procQueryFullProcessImageNameW.Call(h, 0, uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&size)))
	if ok == 0 {
		return ""
	}
	full := readUTF16(buf[:size])
	for i := len(full) - 1; i >= 0; i-- {
		if full[i] == '\\' || full[i] == '/' {
			return full[i+1:]
		}
	}
	return full
}
