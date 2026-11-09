//go:build !windows || !cgo

package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Println("db-fly GUI 需要 Windows 且 CGO_ENABLED=1（并安装 mingw-w64 gcc）。")
	fmt.Println("Linux 或纯 Go 环境请使用命令行工具 dbctl。")
	// -H windowsgui 构建没有控制台，因此在 Windows 上用弹窗说明原因。
	notifyUnsupported()
	os.Exit(1)
}
