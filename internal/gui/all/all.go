//go:build windows && cgo

// Package all 汇总按数据库类型拆分的 GUI 子包，确保它们在程序启动时通过 init()
// 向 gui 主壳注册各自的 workspace 实现。主程序或测试只需导入本包即可启用全部类别。
package all

import (
	_ "github.com/db-fly/db-fly/internal/gui/document"
	_ "github.com/db-fly/db-fly/internal/gui/keyvalue"
	_ "github.com/db-fly/db-fly/internal/gui/relational"
)
