//go:build windows

package winclip

import (
	"errors"
	"unsafe"
)

// cryptProtectUIForbidden 禁止 DPAPI 弹任何 UI（后台进程必须静默）。
const cryptProtectUIForbidden = 0x1

type dataBlob struct {
	Size uint32
	_    uint32
	Data *byte
}

// Protect 用当前用户作用域的 DPAPI 加密数据。
func Protect(plain []byte) ([]byte, error) {
	if len(plain) == 0 {
		return nil, errors.New("没有可加密的内容")
	}
	in := dataBlob{Size: uint32(len(plain)), Data: &plain[0]}
	var out dataBlob
	ok, _, err := procCryptProtectData.Call(
		uintptr(unsafe.Pointer(&in)), 0, 0, 0, 0,
		cryptProtectUIForbidden, uintptr(unsafe.Pointer(&out)),
	)
	if ok == 0 {
		return nil, errSys("DPAPI 加密", err)
	}
	defer procLocalFree.Call(uintptr(unsafe.Pointer(out.Data)))
	if out.Data == nil || out.Size == 0 {
		return nil, errors.New("DPAPI 加密返回空结果")
	}
	buf := make([]byte, int(out.Size))
	copy(buf, unsafe.Slice((*byte)(unsafe.Pointer(out.Data)), int(out.Size)))
	return buf, nil
}

// Unprotect 解密 DPAPI 密文（必须是同一用户、同一台机器加密的）。
func Unprotect(secret []byte) ([]byte, error) {
	if len(secret) == 0 {
		return nil, errors.New("没有可解密的内容")
	}
	in := dataBlob{Size: uint32(len(secret)), Data: &secret[0]}
	var out dataBlob
	ok, _, err := procCryptUnprotectData.Call(
		uintptr(unsafe.Pointer(&in)), 0, 0, 0, 0,
		cryptProtectUIForbidden, uintptr(unsafe.Pointer(&out)),
	)
	if ok == 0 {
		return nil, errSys("DPAPI 解密", err)
	}
	defer procLocalFree.Call(uintptr(unsafe.Pointer(out.Data)))
	if out.Data == nil || out.Size == 0 {
		return nil, errors.New("DPAPI 解密返回空结果")
	}
	buf := make([]byte, int(out.Size))
	copy(buf, unsafe.Slice((*byte)(unsafe.Pointer(out.Data)), int(out.Size)))
	return buf, nil
}
