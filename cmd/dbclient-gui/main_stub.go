//go:build !windows || !cgo

package main

import "fmt"

func main() {
	fmt.Println("DBClient GUI 需要 Windows 且 CGO_ENABLED=1（并安装 mingw-w64 gcc）。")
	fmt.Println("Linux 或纯 Go 环境请使用命令行工具 dbctl。")
}
