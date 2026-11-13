//go:build windows

package bridge

import (
	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/db-fly/db-fly/internal/app"
)

// NewService 构造 Bridge 并包装为 wails 服务，仅用于 GUI 本地模式。
//
// application.NewService 的调用必须保留在本包内：wails3 generate bindings 的静态分析器
// 通过它推断服务类型（*bridge.Bridge）并生成前端绑定，方法 ID 由类型全名 + 方法名计算，
// 与本文件的位置无关，因此现有 bindings 保持不变即可继续工作。
// 该文件带 windows 构建标签，B/S 入口在 Linux 编译时不会链接 wails（其 Linux 后端需要 CGO）。
func NewService(a *app.App, version, dataDir, secretsName string) application.Service {
	// 局部变量形式让绑定生成器的静态分析器可直接推断 T = bridge.Bridge。
	b := New(a, version, dataDir, secretsName)
	return application.NewService(b)
}
