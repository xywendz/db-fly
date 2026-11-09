//go:build windows

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"
	"unsafe"
)

const (
	mbOK        = 0x0
	mbIconError = 0x10
)

// messageBox 在 -H windowsgui（无控制台）构建下仍能向用户展示致命错误。
func messageBox(title, message string) {
	text, err := syscall.UTF16PtrFromString(message)
	if err != nil {
		return
	}
	caption, err := syscall.UTF16PtrFromString(title)
	if err != nil {
		return
	}
	proc := syscall.NewLazyDLL("user32.dll").NewProc("MessageBoxW")
	_, _, _ = proc.Call(
		0,
		uintptr(unsafe.Pointer(text)),
		uintptr(unsafe.Pointer(caption)),
		mbOK|mbIconError,
	)
}

// showFatal 把启动错误写入日志并弹窗，便于排查无控制台的 GUI 程序。
func showFatal(title, message string) {
	if path := writeStartupLog(message); path != "" {
		message += "\n\n启动日志: " + path
	}
	messageBox(title, message)
}

func writeStartupLog(message string) string {
	dir := filepath.Join(os.TempDir(), "db-fly")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return ""
	}
	path := filepath.Join(dir, "gui-startup.log")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return ""
	}
	defer f.Close()
	_, _ = fmt.Fprintf(f, "%s %s\n", time.Now().Format(time.RFC3339), message)
	return path
}

// notifyUnsupported 让误以 CGO_ENABLED=0 构建的占位 exe 说明原因，而不是双击无反应。
func notifyUnsupported() {
	messageBox("db-fly 无法启动",
		"当前程序是占位版本：构建时未启用 CGO（CGO_ENABLED=0 或找不到 mingw-w64 gcc）。\n\n"+
			"请安装 mingw-w64 gcc 并运行 ./build/gui.ps1 重新构建 GUI，或使用命令行工具 dbctl。")
}
