//go:build windows && cgo

package main

import (
	"fmt"
	"os"

	"github.com/db-fly/db-fly/internal/domain"
	"github.com/db-fly/db-fly/internal/gui"
	// 注册按数据库类型拆分的 GUI 子包（relational / document / keyvalue）。
	_ "github.com/db-fly/db-fly/internal/gui/all"
)

func main() {
	// GUI 子系统没有控制台：捕获 panic 并写入启动日志 + 弹窗，避免静默退出。
	defer func() {
		if r := recover(); r != nil {
			showFatal("db-fly 启动失败", fmt.Sprintf("内部错误（panic）: %v", r))
			os.Exit(1)
		}
	}()
	if err := gui.Run(); err != nil {
		msg := domain.UserMessage(err)
		fmt.Fprintln(os.Stderr, "错误:", msg)
		showFatal("db-fly 启动失败", msg)
		os.Exit(1)
	}
}
