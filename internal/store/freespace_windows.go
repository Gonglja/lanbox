//go:build windows

package store

import (
	"errors"
	"syscall"
	"unsafe"
)

var (
	kernel32             = syscall.NewLazyDLL("kernel32.dll")
	procGetDiskFreeSpace = kernel32.NewProc("GetDiskFreeSpaceExW")
)

// diskFree 返回 path 所在卷的可用字节数（GetDiskFreeSpaceEx 的 lpFreeBytesAvailableToCaller）。
func diskFree(path string) (int64, error) {
	p, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return 0, err
	}
	var freeToCaller, totalBytes, totalFreeBytes uint64
	r, _, callErr := procGetDiskFreeSpace.Call(
		uintptr(unsafe.Pointer(p)),
		uintptr(unsafe.Pointer(&freeToCaller)),
		uintptr(unsafe.Pointer(&totalBytes)),
		uintptr(unsafe.Pointer(&totalFreeBytes)),
	)
	if r == 0 {
		if callErr != nil && callErr != syscall.Errno(0) {
			return 0, callErr
		}
		return 0, errors.New("GetDiskFreeSpaceEx 调用失败")
	}
	if freeToCaller > uint64(1<<63-1) {
		return 1<<63 - 1, nil
	}
	return int64(freeToCaller), nil
}
