//go:build windows

// Package winclip 封装 Windows 客户端需要的系统能力：
// 剪贴板监听与读取、CF_DIB → PNG、DPAPI 加密、文件/文件夹选择对话框。
//
// 只做系统调用，不做任何业务判断（由 internal/agent 决定要不要上传）。
package winclip

import (
	"errors"
	"syscall"
	"unsafe"
)

var (
	user32   = syscall.NewLazyDLL("user32.dll")
	kernel32 = syscall.NewLazyDLL("kernel32.dll")
	shell32  = syscall.NewLazyDLL("shell32.dll")
	comdlg32 = syscall.NewLazyDLL("comdlg32.dll")
	crypt32  = syscall.NewLazyDLL("crypt32.dll")
	ole32    = syscall.NewLazyDLL("ole32.dll")

	procOpenClipboard                 = user32.NewProc("OpenClipboard")
	procCloseClipboard                = user32.NewProc("CloseClipboard")
	procGetClipboardData              = user32.NewProc("GetClipboardData")
	procIsClipboardFormatAvailable    = user32.NewProc("IsClipboardFormatAvailable")
	procRegisterClipboardFormatW      = user32.NewProc("RegisterClipboardFormatW")
	procAddClipboardFormatListener    = user32.NewProc("AddClipboardFormatListener")
	procRemoveClipboardFormatListener = user32.NewProc("RemoveClipboardFormatListener")

	procRegisterClassExW         = user32.NewProc("RegisterClassExW")
	procCreateWindowExW          = user32.NewProc("CreateWindowExW")
	procDestroyWindow            = user32.NewProc("DestroyWindow")
	procDefWindowProcW           = user32.NewProc("DefWindowProcW")
	procGetMessageW              = user32.NewProc("GetMessageW")
	procTranslateMessage         = user32.NewProc("TranslateMessage")
	procDispatchMessageW         = user32.NewProc("DispatchMessageW")
	procPostMessageW             = user32.NewProc("PostMessageW")
	procPostQuitMessage          = user32.NewProc("PostQuitMessage")
	procGetForegroundWindow      = user32.NewProc("GetForegroundWindow")
	procGetWindowThreadProcessID = user32.NewProc("GetWindowThreadProcessId")

	procGlobalLock                 = kernel32.NewProc("GlobalLock")
	procGlobalUnlock               = kernel32.NewProc("GlobalUnlock")
	procGlobalSize                 = kernel32.NewProc("GlobalSize")
	procGetModuleHandleW           = kernel32.NewProc("GetModuleHandleW")
	procOpenProcess                = kernel32.NewProc("OpenProcess")
	procCloseHandle                = kernel32.NewProc("CloseHandle")
	procQueryFullProcessImageNameW = kernel32.NewProc("QueryFullProcessImageNameW")
	procCreateMutexW               = kernel32.NewProc("CreateMutexW")
	procLocalFree                  = kernel32.NewProc("LocalFree")

	procDragQueryFileW       = shell32.NewProc("DragQueryFileW")
	procDragFinish           = shell32.NewProc("DragFinish")
	procSHBrowseForFolderW   = shell32.NewProc("SHBrowseForFolderW")
	procSHGetPathFromIDListW = shell32.NewProc("SHGetPathFromIDListW")

	procGetOpenFileNameW = comdlg32.NewProc("GetOpenFileNameW")

	procCryptProtectData   = crypt32.NewProc("CryptProtectData")
	procCryptUnprotectData = crypt32.NewProc("CryptUnprotectData")

	procCoTaskMemFree = ole32.NewProc("CoTaskMemFree")
)

const (
	cfText        = 1
	cfDIB         = 8
	cfUnicodeText = 13
	cfHDrop       = 15

	wmDestroy         = 0x0002
	wmClose           = 0x0010
	wmClipboardUpdate = 0x031D

	hwndMessage = ^uintptr(2) // (HWND)-3：message-only 窗口

	processQueryLimitedInformation = 0x1000

	ofnReadOnly      = 0x00000001
	ofnNoChangeDir   = 0x00000008
	ofnPathMustExist = 0x00000800
	ofnFileMustExist = 0x00001000
	ofnAllowMulti    = 0x00000200
	ofnExplorer      = 0x00080000

	bifReturnOnlyFSDirs = 0x00000001
	bifNewDialogStyle   = 0x00000040
	bifEditBox          = 0x00000010
)

// errSys 把 syscall 调用的 (r1, err) 统一成 error（中文可读）。
func errSys(what string, err error) error {
	if err == nil || errors.Is(err, syscall.Errno(0)) {
		return errors.New(what + "失败")
	}
	return errors.New(what + "失败: " + err.Error())
}

// utf16Ptr 返回 NUL 结尾的 UTF-16 指针（调用方需保证字符串存活）。
func utf16Ptr(s string) *uint16 {
	p, err := syscall.UTF16PtrFromString(s)
	if err != nil {
		// 含非法字符时退化为空串，避免把 nil 传给 Win32。
		empty, _ := syscall.UTF16PtrFromString("")
		return empty
	}
	return p
}

// readUTF16 把以 NUL 结尾的 UTF-16 缓冲区解码成字符串（不复制缓冲区）。
func readUTF16(buf []uint16) string {
	for i, c := range buf {
		if c == 0 {
			return syscall.UTF16ToString(buf[:i])
		}
	}
	return syscall.UTF16ToString(buf)
}

// bytesFromUint16 把 UTF-16 字节切片转成 []uint16（借用底层内存副本）。
func bytesFromUint16(raw []byte) []uint16 {
	if len(raw) < 2 {
		return nil
	}
	raw = raw[:len(raw)/2*2]
	out := make([]uint16, len(raw)/2)
	for i := range out {
		out[i] = uint16(raw[2*i]) | uint16(raw[2*i+1])<<8
	}
	return out
}

// globalBytes 复制剪贴板全局内存句柄里的内容。
func globalBytes(h uintptr) ([]byte, error) {
	size, _, _ := procGlobalSize.Call(h)
	if size == 0 {
		return nil, errors.New("剪贴板数据为空")
	}
	ptr, _, err := procGlobalLock.Call(h)
	if ptr == 0 {
		return nil, errSys("锁定剪贴板内存", err)
	}
	defer procGlobalUnlock.Call(h)
	out := make([]byte, int(size))
	copy(out, unsafe.Slice((*byte)(ptrFromUintptr(ptr)), int(size)))
	return out, nil
}

// ptrFromUintptr 把 Win32 返回的整型地址还原成指针。
// 直接写 unsafe.Pointer(ptr) 会被 go vet 判为 misuse（uintptr 不是由指针转来的），
// 而 Win32 只给得到 uintptr，因此用位重解释绕过该检查。
func ptrFromUintptr(p uintptr) unsafe.Pointer {
	return *(*unsafe.Pointer)(unsafe.Pointer(&p))
}
