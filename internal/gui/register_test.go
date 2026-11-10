//go:build windows && cgo

// 本文件以外部测试包形式存在，仅用于触发 gui/all 子包 init() 把各数据库类别的
// workspace 注册进 gui 主壳。之所以放在 external test package（gui_test），是因为
// 内部测试包（package gui）若导入 gui/all 会形成 gui -> gui/all -> gui 的循环依赖。
package gui_test

import (
	"testing"

	_ "github.com/db-fly/db-fly/internal/gui/all"
)

func TestWorkspacesRegistered(t *testing.T) {
	// 占位测试：确保子包 init 已执行，各类别 workspace 被注册。
	// 真实校验由 package gui 的内部测试（workbench_test.go）完成。
}
