//go:build !windows || !cgo

package gui

import "errors"

// Run 在非 Windows 或未启用 CGO 的环境给出明确提示，保证 CLI 始终可独立编译。
func Run() error {
	return errors.New("GUI 需要 Windows 且启用 CGO（并安装 mingw-w64 gcc）；Linux 请使用 dbctl")
}
