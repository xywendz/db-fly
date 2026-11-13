package bridge

import (
	"github.com/db-fly/db-fly/internal/app"
	"github.com/wailsapp/wails/v3/pkg/application"
)

// NewService 构造 Bridge 并包装为 wails 服务。
//
// 该文件保持纯 Go，原因有二：
//  1. 让 wails 绑定生成器（wails3 generate bindings）能扫描到
//     application.NewService 调用，从而正确生成 Bridge 的前端绑定；
//  2. 让 internal/bridge 在任意平台都能通过 go build / go vet 类型校验。
//
// wails3 v3 的 Windows 运行时同样是纯 Go（CGO_ENABLED=0，无需 gcc），窗口代码
// 位于 cmd/db-fly-gui/main.go（//go:build windows）。
func NewService(a *app.App, version, dataDir, secretsName string) application.Service {
	b := newBridge(a)
	b.setRuntimeInfo(version, dataDir, secretsName)
	return application.NewService(b)
}
