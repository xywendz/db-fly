// Package logging 提供统一的日志初始化：在「运行目录/logs」下创建日志目录，
// 将标准库日志（log 包）同时输出到「按天滚动的文件 + 标准错误」，并返回一个
// 写入该目录的审计日志器（用于后端数据库操作日志）。
package logging

import (
	"io"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/db-fly/db-fly/internal/platform/audit"
)

// Setup 在 base 下创建 logs 目录（base 为空时使用当前工作目录），并完成两件事：
//  1. 将标准库 log 输出重定向到 logs/db-fly-YYYY-MM-DD.log（追加）+ 标准错误；
//  2. 返回一个写入 logs/audit.log 的审计日志器，用于记录数据库操作。
//
// 返回的闭包用于进程退出时关闭日志文件；失败时对标准日志无副作用。
func Setup(base string) (audit.Logger, func(), error) {
	if base == "" {
		wd, err := os.Getwd()
		if err != nil {
			return nil, nil, err
		}
		base = wd
	}
	logDir := filepath.Join(base, "logs")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		return nil, nil, err
	}

	logPath := filepath.Join(logDir, "db-fly-"+time.Now().Format("2006-01-02")+".log")
	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, nil, err
	}
	log.SetOutput(io.MultiWriter(f, os.Stderr))
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)

	auditLog, aerr := audit.New(logDir)
	if aerr != nil {
		_ = f.Close()
		return nil, nil, aerr
	}
	closer := func() { _ = f.Close() }
	return auditLog, closer, nil
}
