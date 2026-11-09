//go:build !windows

package main

// notifyUnsupported 在非 Windows 平台由 main_stub.go 的终端输出提示即可。
func notifyUnsupported() {}
