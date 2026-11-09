//go:build windows

package credential

import (
	"errors"
	"fmt"
	"syscall"
	"unsafe"
)

// Windows 使用 DPAPI，密文绑定当前 Windows 用户，无需主密码。
// 采用纯 syscall 调用 crypt32.dll，不依赖 CGO，便于 GUI/CLI 统一构建。

type dataBlob struct {
	size uint32
	data *byte
}

var (
	crypt32           = syscall.NewLazyDLL("crypt32.dll")
	kernel32          = syscall.NewLazyDLL("kernel32.dll")
	procProtectData   = crypt32.NewProc("CryptProtectData")
	procUnprotectData = crypt32.NewProc("CryptUnprotectData")
	procLocalFree     = kernel32.NewProc("LocalFree")
)

const cryptProtectUIForbidden = 0x1

type dpapiCipher struct{}

func (dpapiCipher) Name() string { return "dpapi" }

func platformCipher(_ string) (Cipher, error) { return dpapiCipher{}, nil }

func (dpapiCipher) Encrypt(plain []byte) ([]byte, error) {
	in := dataBlob{size: uint32(len(plain))}
	if len(plain) > 0 {
		in.data = &plain[0]
	}
	var out dataBlob
	ret, _, callErr := procProtectData.Call(
		uintptr(unsafe.Pointer(&in)),
		0, 0, 0, 0,
		uintptr(cryptProtectUIForbidden),
		uintptr(unsafe.Pointer(&out)),
	)
	if ret == 0 {
		return nil, fmt.Errorf("CryptProtectData 失败: %w", callErr)
	}
	if out.size == 0 || out.data == nil {
		return nil, errors.New("CryptProtectData 返回空数据")
	}
	defer func() {
		_, _, _ = procLocalFree.Call(uintptr(unsafe.Pointer(out.data)))
	}()
	buf := make([]byte, out.size)
	copy(buf, unsafe.Slice(out.data, int(out.size)))
	return buf, nil
}

func (dpapiCipher) Decrypt(data []byte) ([]byte, error) {
	in := dataBlob{size: uint32(len(data))}
	if len(data) > 0 {
		in.data = &data[0]
	}
	var out dataBlob
	ret, _, callErr := procUnprotectData.Call(
		uintptr(unsafe.Pointer(&in)),
		0, 0, 0, 0,
		uintptr(cryptProtectUIForbidden),
		uintptr(unsafe.Pointer(&out)),
	)
	if ret == 0 {
		return nil, fmt.Errorf("CryptUnprotectData 失败: %w", callErr)
	}
	if out.size == 0 || out.data == nil {
		return nil, errors.New("CryptUnprotectData 返回空数据")
	}
	defer func() {
		_, _, _ = procLocalFree.Call(uintptr(unsafe.Pointer(out.data)))
	}()
	buf := make([]byte, out.size)
	copy(buf, unsafe.Slice(out.data, int(out.size)))
	return buf, nil
}
