package bridge

import "github.com/db-fly/db-fly/internal/app"

// New 构造 Bridge：把业务内核包装成一组 JSON 可序列化的门面方法。
//
// 本文件保持纯 Go（不引入 wails），因此 GUI 与 B/S 两个入口都可以在任意平台复用：
//   - GUI 入口通过 service_windows.go 的 NewService 再包装成 wails 服务；
//   - B/S 入口通过 internal/httpapi 以 HTTP + 反射分发方式调用这些方法。
func New(a *app.App, version, dataDir, secretsName string) *Bridge {
	b := newBridge(a)
	b.setRuntimeInfo(version, dataDir, secretsName)
	return b
}
