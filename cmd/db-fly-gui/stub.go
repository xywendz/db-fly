//go:build !windows

// 非 Windows 环境下的占位入口：让 go build ./... 与单测在任意平台都可通过。
// Windows 下由 main.go 编译真正的桌面程序（wails3 v3 纯 Go，不需要 CGO）。
package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr, "db-fly 桌面程序仅支持 Windows（wails3 + WebView2）。")
	fmt.Fprintln(os.Stderr, "Windows 下请使用 ./build/gui.ps1 或 wails3 build 构建，或改用命令行工具 dbctl。")
	os.Exit(1)
}
