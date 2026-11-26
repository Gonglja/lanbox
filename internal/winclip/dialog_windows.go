//go:build windows

package winclip

import (
	"runtime"
	"syscall"
	"unsafe"
)

const coinitApartmentThreaded = 0x2

type openFileNameW struct {
	StructSize    uint32
	_             uint32
	Owner         uintptr
	Instance      uintptr
	Filter        *uint16
	CustomFilter  *uint16
	MaxCustFilter uint32
	FilterIndex   uint32
	File          *uint16
	MaxFile       uint32
	_             uint32
	FileTitle     *uint16
	MaxFileTitle  uint32
	_             uint32
	InitialDir    *uint16
	Title         *uint16
	Flags         uint32
	FileOffset    uint16
	FileExtension uint16
	DefExt        *uint16
	CustData      uintptr
	Hook          uintptr
	TemplateName  *uint16
	ReservedPtr   unsafe.Pointer
	Reserved      uint32
	FlagsEx       uint32
}

type browseInfoW struct {
	Owner       uintptr
	Root        uintptr
	DisplayName *uint16
	Title       *uint16
	Flags       uint32
	_           uint32
	Callback    uintptr
	Param       uintptr
	Image       uint32
	_           uint32
}

var (
	procCoInitializeEx = ole32.NewProc("CoInitializeEx")
	procCoUninitialize = ole32.NewProc("CoUninitialize")
)

// makeFilter 生成 OPENFILENAME 需要的双 NUL 结尾过滤器。
func makeFilter(label, pattern string) []uint16 {
	s, err := syscall.UTF16FromString(label + "\x00" + pattern + "\x00")
	if err != nil {
		return []uint16{0, 0}
	}
	return s
}

// PickFiles 弹出多选文件对话框。用户取消时返回 (nil, nil)。
func PickFiles(title string) ([]string, error) {
	// 通用对话框要求 STA，且初始化与清理必须在同一线程上。
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	procCoInitializeEx.Call(0, coinitApartmentThreaded)
	defer procCoUninitialize.Call()

	filter := makeFilter("所有文件", "*.*")
	buf := make([]uint16, 64*1024)
	ofn := openFileNameW{
		StructSize: uint32(unsafe.Sizeof(openFileNameW{})),
		Filter:     &filter[0],
		File:       &buf[0],
		MaxFile:    uint32(len(buf)),
		Title:      utf16Ptr(title),
		Flags:      ofnAllowMulti | ofnExplorer | ofnFileMustExist | ofnPathMustExist | ofnNoChangeDir,
	}
	ok, _, err := procGetOpenFileNameW.Call(uintptr(unsafe.Pointer(&ofn)))
	if ok == 0 {
		// 取消时 GetLastError 为 0；真实错误极少见，统一按取消处理并记录到日志层。
		if err != nil && !isSuccessErrno(err) {
			return nil, errSys("打开文件对话框", err)
		}
		return nil, nil
	}
	first := readUTF16(buf)
	// 多选时 lpstrFile 为 "目录\0文件1\0文件2\0\0"；单选时是完整路径。
	rest := buf[len(first)+1:]
	if len(rest) == 0 || rest[0] == 0 {
		return []string{first}, nil
	}
	out := make([]string, 0, 4)
	for len(rest) > 0 && rest[0] != 0 {
		name := readUTF16(rest)
		out = append(out, first+"\\"+name)
		rest = rest[len(name)+1:]
	}
	if len(out) == 0 {
		return []string{first}, nil
	}
	return out, nil
}

// PickFolder 弹出文件夹选择对话框。用户取消时返回 ("", nil)。
func PickFolder(title string) (string, error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	procCoInitializeEx.Call(0, coinitApartmentThreaded)
	defer procCoUninitialize.Call()

	buf := make([]uint16, 512)
	bi := browseInfoW{
		DisplayName: &buf[0],
		Title:       utf16Ptr(title),
		Flags:       bifReturnOnlyFSDirs | bifEditBox,
	}
	pidl, _, _ := procSHBrowseForFolderW.Call(uintptr(unsafe.Pointer(&bi)))
	if pidl == 0 {
		return "", nil
	}
	defer procCoTaskMemFree.Call(pidl)
	ok, _, err := procSHGetPathFromIDListW.Call(pidl, uintptr(unsafe.Pointer(&buf[0])))
	if ok == 0 {
		return "", errSys("解析文件夹路径", err)
	}
	return readUTF16(buf), nil
}

// isSuccessErrno 判断 Proc.Call 返回的 err 是否只是 ERROR_SUCCESS。
func isSuccessErrno(err error) bool {
	if err == nil {
		return true
	}
	if e, ok := err.(syscall.Errno); ok {
		return e == 0
	}
	return false
}
