// Package webui 把 B/S 模式的前端构建产物嵌入 Go 进程，实现单二进制部署。
package webui

import (
	"embed"
	"io/fs"
)

// dist 由 build/web.ps1 或 Dockerfile 从 frontend/dist-web 复制而来。
// 仓库内保留的 index.html 占位页会在未构建前端时给出明确提示。
//
//go:embed all:dist
var files embed.FS

// FS 返回以 dist/ 为根的静态文件系统，可直接交给 http.FileServer。
func FS() fs.FS {
	sub, err := fs.Sub(files, "dist")
	if err != nil {
		panic("webui: 嵌入资源异常: " + err.Error())
	}
	return sub
}
