//go:build windows

// db-fly 桌面程序入口（wails3 + Vue）。
// wails3 v3 的 Windows 后端是纯 Go（CGO_ENABLED=0 即可），不需要 mingw-w64 gcc。
// 非 Windows 平台由 stub.go 提供占位入口，保证 go build ./... / go test ./... 可用。
package main

import (
	"embed"
	"io/fs"
	"log"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/db-fly/db-fly/internal/bootstrap"
	"github.com/db-fly/db-fly/internal/bridge"
	// 注册全部数据库业务模块（各自 init() 自注册到 provider 注册中心）。
	_ "github.com/db-fly/db-fly/internal/provider/all"
)

// 前端构建产物由 build/gui.ps1 从 frontend/dist 复制到 assets/。
//
//go:embed all:assets
var assets embed.FS

// logo.png 的副本，用于 Linux/macOS 的窗口与关于对话框图标。
// Windows 下窗口图标由 rsrc_windows_*.syso 中的 exe 资源提供（见 build/gui.ps1）。
//
//go:embed icon.png
var appIcon []byte

// assetFS 剥离嵌入目录的 assets/ 前缀，使 index.html 位于 FS 根，
// 让 BundledAssetFileServer 可以直接以 "/" 提供单页应用。
func assetFS() fs.FS {
	sub, err := fs.Sub(assets, "assets")
	if err != nil {
		log.Fatalf("资源嵌入异常: %v", err)
	}
	return sub
}

// emitter 把 wails 事件总线适配成 bridge.EventEmitter。
type emitter struct{ wailsApp *application.App }

func (e emitter) Emit(name string, data ...any) { e.wailsApp.Event.Emit(name, data...) }

func main() {
	// 与 B/S 入口共用同一套装配链（config / credential / audit / app.New）。
	rt, err := bootstrap.New("")
	if err != nil {
		log.Fatalf("初始化业务内核失败: %v", err)
	}
	core := rt.App
	svc := bridge.NewService(core, bridge.Version, rt.DataDir, rt.SecretsName)

	wailsApp := application.New(application.Options{
		Name:        "db-fly",
		Description: "模块化、多数据库、跨平台的数据库客户端",
		Icon:        appIcon,
		Services: []application.Service{
			svc,
		},
		Assets: application.AssetOptions{
			Handler: application.BundledAssetFileServer(assetFS()),
		},
		Mac: application.MacOptions{
			ApplicationShouldTerminateAfterLastWindowClosed: true,
		},
	})

	// 创建应用后注入事件发射器：Bridge 通过包级变量把进度/通知转发到 wails 事件总线。
	bridge.SetDefaultEmitter(emitter{wailsApp: wailsApp})

	wailsApp.Window.NewWithOptions(application.WebviewWindowOptions{
		Name:      "main",
		Title:     "db-fly",
		Width:     1280,
		Height:    800,
		MinWidth:  960,
		MinHeight: 640,
	})

	wailsApp.OnShutdown(func() { _ = rt.Close() })

	if err := wailsApp.Run(); err != nil {
		log.Fatalf("应用运行失败: %v", err)
	}
}
