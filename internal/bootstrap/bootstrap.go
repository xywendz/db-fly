// Package bootstrap 提供 GUI 本地模式与 B/S 服务模式共用的业务内核装配。
// 两种入口共享同一套 config / credential / audit / app.New 初始化链，避免行为漂移。
package bootstrap

import (
	"fmt"

	"github.com/db-fly/db-fly/internal/app"
	"github.com/db-fly/db-fly/internal/platform/audit"
	"github.com/db-fly/db-fly/internal/platform/config"
	"github.com/db-fly/db-fly/internal/platform/credential"
)

// Runtime 是一次进程启动装配出的运行时；调用方负责在退出时调用 Close。
type Runtime struct {
	App         *app.App
	DataDir     string
	SecretsName string
}

// New 创建运行时。dataDir 为空时使用 config.DefaultDir()（~/.db-fly）。
func New(dataDir string) (*Runtime, error) {
	if dataDir == "" {
		dir, err := config.DefaultDir()
		if err != nil {
			return nil, fmt.Errorf("无法确定数据目录: %w", err)
		}
		dataDir = dir
	}
	cfg, err := config.New(dataDir)
	if err != nil {
		return nil, fmt.Errorf("加载配置失败: %w", err)
	}
	secrets, err := credential.New(dataDir)
	if err != nil {
		return nil, fmt.Errorf("初始化凭证存储失败: %w", err)
	}
	var logger audit.Logger = audit.Nop{}
	if l, lerr := audit.New(dataDir); lerr == nil {
		logger = l
	}
	return &Runtime{
		App:         app.New(cfg, secrets, logger),
		DataDir:     dataDir,
		SecretsName: secrets.Name(),
	}, nil
}

// Close 释放全部数据库会话。
func (r *Runtime) Close() error {
	if r == nil || r.App == nil {
		return nil
	}
	return r.App.Close()
}
