// Package app 是 GUI 与 CLI 共用的业务用例层，只依赖 domain 端口与 platform 能力。
package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/db-fly/db-fly/internal/domain"
	"github.com/db-fly/db-fly/internal/platform/audit"
	"github.com/db-fly/db-fly/internal/platform/config"
	"github.com/db-fly/db-fly/internal/platform/credential"
	"github.com/db-fly/db-fly/internal/provider"
)

// App 聚合配置、凭证、审计与会话缓存，是所有用例的入口。
type App struct {
	Config  config.Store
	Secrets credential.Store
	Audit   audit.Logger

	mu       sync.Mutex
	cfgMu    sync.Mutex
	sessions map[string]domain.Session
}

// mutateConfig 串行化「读取 → 修改 → 保存」，避免历史、收藏与连接管理互相覆盖。
func (a *App) mutateConfig(fn func(*config.Config) error) error {
	a.cfgMu.Lock()
	defer a.cfgMu.Unlock()
	cfg, err := a.loadConfig()
	if err != nil {
		return err
	}
	if err := fn(cfg); err != nil {
		return err
	}
	return a.Config.Save(cfg)
}

// New 创建业务内核。
func New(cfg config.Store, secrets credential.Store, logs audit.Logger) *App {
	if logs == nil {
		logs = audit.Nop{}
	}
	if secrets == nil {
		secrets = nopSecrets{}
	}
	return &App{
		Config:   cfg,
		Secrets:  secrets,
		Audit:    logs,
		sessions: make(map[string]domain.Session),
	}
}

// NewID 生成一个本地唯一 ID。
func NewID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("id-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

// Close 关闭全部会话，应在程序退出时调用。
func (a *App) Close() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	var firstErr error
	for id, s := range a.sessions {
		if err := s.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
		delete(a.sessions, id)
	}
	return firstErr
}

// DropSession 使某个连接的缓存会话失效（配置变更或手动断开时调用）。
func (a *App) DropSession(id string) {
	a.mu.Lock()
	s, ok := a.sessions[id]
	if ok {
		delete(a.sessions, id)
	}
	a.mu.Unlock()
	if ok {
		_ = s.Close()
	}
}

// CloseConnection 关闭指定连接的缓存会话并释放底层连接池，用于手动节省资源。
func (a *App) CloseConnection(ctx context.Context, nameOrID string) error {
	c, err := a.cfgOf(nameOrID)
	if err != nil {
		return err
	}
	a.DropSession(c.ID)
	return nil
}

// OpenConnections 返回当前已建立会话（已打开）的连接名列表，供前端资源管理器展示。
func (a *App) OpenConnections(ctx context.Context) ([]string, error) {
	a.mu.Lock()
	ids := make([]string, 0, len(a.sessions))
	for id := range a.sessions {
		ids = append(ids, id)
	}
	a.mu.Unlock()
	if len(ids) == 0 {
		return []string{}, nil
	}
	cfg, err := a.loadConfig()
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(ids))
	for _, id := range ids {
		if c, ok := config.FindConnection(cfg, id); ok {
			names = append(names, c.Name)
		}
	}
	sort.Strings(names)
	return names, nil
}

func (a *App) loadConfig() (*config.Config, error) {
	return a.Config.Load()
}

func (a *App) cfgOf(nameOrID string) (domain.ConnectionConfig, error) {
	cfg, err := a.loadConfig()
	if err != nil {
		return domain.ConnectionConfig{}, err
	}
	c, ok := config.FindConnection(cfg, nameOrID)
	if !ok {
		return domain.ConnectionConfig{}, domain.NewError(domain.CodeNotFound, "connection",
			"连接 %q 不存在", nameOrID)
	}
	return c, nil
}

func (a *App) withPassword(c domain.ConnectionConfig) (domain.ConnectionConfig, error) {
	pw, err := a.Secrets.Get(c.SecretKey())
	if errors.Is(err, credential.ErrNotFound) {
		c.Password = ""
		return c, nil
	}
	if err != nil {
		return c, domain.WrapError(domain.CodePermission, "credential", err,
			"读取连接 %q 的凭证失败", c.Name)
	}
	c.Password = pw
	return c, nil
}

// Session 取得（或懒建立）一个连接会话。相同连接复用同一个会话与连接池。
func (a *App) Session(ctx context.Context, nameOrID string) (domain.Session, error) {
	c, err := a.cfgOf(nameOrID)
	if err != nil {
		return nil, err
	}

	a.mu.Lock()
	if s, ok := a.sessions[c.ID]; ok {
		a.mu.Unlock()
		return s, nil
	}
	a.mu.Unlock()

	full, err := a.withPassword(c)
	if err != nil {
		return nil, err
	}
	openCtx, cancel := context.WithTimeout(ctx, full.EffectiveTimeout())
	defer cancel()

	s, err := provider.Open(openCtx, full)
	if err != nil {
		a.Audit.Log(audit.Entry{
			Connection: c.Name, Kind: string(c.Kind), Action: "connect", OK: false, Error: err.Error(),
		})
		return nil, err
	}

	a.mu.Lock()
	if existing, ok := a.sessions[c.ID]; ok {
		a.mu.Unlock()
		_ = s.Close()
		return existing, nil
	}
	a.sessions[c.ID] = s
	a.mu.Unlock()

	a.Audit.Log(audit.Entry{
		Connection: c.Name, Kind: string(c.Kind), Action: "connect", OK: true,
	})
	return s, nil
}

// ValidateConnection 校验连接表单。
func ValidateConnection(c domain.ConnectionConfig) error {
	if c.Name == "" {
		return domain.NewError(domain.CodeValidation, "connection", "连接名称不能为空")
	}
	if !c.Kind.Valid() {
		return domain.NewError(domain.CodeValidation, "connection", "不支持的数据库类型 %q", c.Kind)
	}
	if c.Host == "" {
		return domain.NewError(domain.CodeValidation, "connection", "主机地址不能为空")
	}
	// 文件型数据库（SQLite）用 Host 保存文件路径，不需要端口。
	if !c.Kind.IsFileBased() && (c.Port < 1 || c.Port > 65535) {
		return domain.NewError(domain.CodeValidation, "connection", "端口必须在 1~65535 之间")
	}
	return nil
}

type nopSecrets struct{}

func (nopSecrets) Get(string) (string, error) { return "", credential.ErrNotFound }
func (nopSecrets) Put(string, string) error   { return nil }
func (nopSecrets) Delete(string) error        { return nil }
func (nopSecrets) Keys() ([]string, error)    { return nil, nil }
func (nopSecrets) Name() string               { return "none" }
