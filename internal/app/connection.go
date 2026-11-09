package app

import (
	"context"
	"errors"

	"github.com/dbclient/dbclient/internal/domain"
	"github.com/dbclient/dbclient/internal/platform/audit"
	"github.com/dbclient/dbclient/internal/platform/config"
	"github.com/dbclient/dbclient/internal/platform/credential"
	"github.com/dbclient/dbclient/internal/provider"
)

// ListConnections 返回全部连接定义（不含密码）。
func (a *App) ListConnections() ([]domain.ConnectionConfig, error) {
	cfg, err := a.loadConfig()
	if err != nil {
		return nil, err
	}
	out := make([]domain.ConnectionConfig, len(cfg.Connections))
	copy(out, cfg.Connections)
	for i := range out {
		out[i].Password = ""
	}
	return out, nil
}

// Preferences 返回当前用户偏好。
func (a *App) Preferences() (config.Preferences, error) {
	cfg, err := a.loadConfig()
	if err != nil {
		return config.Preferences{}, err
	}
	return cfg.Preferences, nil
}

// SaveConnection 新增或更新连接。
// password 为空且 replacePassword=false 时保留已有凭证（适用于编辑表单不重填密码）。
// replacePassword=true 且 password 为空时表示清空密码。
func (a *App) SaveConnection(c domain.ConnectionConfig, password string, replacePassword bool) (domain.ConnectionConfig, error) {
	if err := ValidateConnection(c); err != nil {
		return domain.ConnectionConfig{}, err
	}
	if _, err := provider.Get(c.Kind); err != nil {
		return domain.ConnectionConfig{}, err
	}

	a.cfgMu.Lock()
	defer a.cfgMu.Unlock()
	cfg, err := a.loadConfig()
	if err != nil {
		return domain.ConnectionConfig{}, err
	}
	isNew := c.ID == ""
	if isNew {
		c.ID = NewID()
	}
	for _, existing := range cfg.Connections {
		if existing.Name == c.Name && existing.ID != c.ID {
			return domain.ConnectionConfig{}, domain.NewError(domain.CodeValidation, "connection",
				"连接名称 %q 已存在", c.Name)
		}
	}
	if c.TimeoutSec <= 0 {
		c.TimeoutSec = 15
	}
	c.Password = ""

	if replacePassword || password != "" {
		if password == "" {
			if err := a.Secrets.Delete(c.SecretKey()); err != nil && !errors.Is(err, credential.ErrNotFound) {
				return domain.ConnectionConfig{}, err
			}
		} else if err := a.Secrets.Put(c.SecretKey(), password); err != nil {
			return domain.ConnectionConfig{}, err
		}
	} else if isNew {
		if err := a.Secrets.Put(c.SecretKey(), ""); err != nil {
			return domain.ConnectionConfig{}, err
		}
	}

	config.UpsertConnection(cfg, c)
	if err := a.Config.Save(cfg); err != nil {
		return domain.ConnectionConfig{}, err
	}
	a.DropSession(c.ID)
	a.Audit.Log(audit.Entry{Connection: c.Name, Kind: string(c.Kind), Action: "save_connection", OK: true})
	return c, nil
}

// RemoveConnection 删除连接及其凭证。
func (a *App) RemoveConnection(nameOrID string) error {
	c, err := a.cfgOf(nameOrID)
	if err != nil {
		return err
	}
	a.cfgMu.Lock()
	defer a.cfgMu.Unlock()
	cfg, err := a.loadConfig()
	if err != nil {
		return err
	}
	if !config.RemoveConnection(cfg, c.ID) {
		return domain.NewError(domain.CodeNotFound, "connection", "连接 %q 不存在", nameOrID)
	}
	if err := a.Config.Save(cfg); err != nil {
		return err
	}
	a.DropSession(c.ID)
	if err := a.Secrets.Delete(c.SecretKey()); err != nil && !errors.Is(err, credential.ErrNotFound) {
		return err
	}
	a.Audit.Log(audit.Entry{Connection: c.Name, Kind: string(c.Kind), Action: "remove_connection", OK: true})
	return nil
}

// TestConnection 使用表单中的临时参数测试连通性，不写入配置、不进入会话缓存。
func (a *App) TestConnection(ctx context.Context, c domain.ConnectionConfig, password string) (domain.ServerInfo, error) {
	if err := ValidateConnection(c); err != nil {
		return domain.ServerInfo{}, err
	}
	c.Password = password
	openCtx, cancel := context.WithTimeout(ctx, c.EffectiveTimeout())
	defer cancel()

	s, err := provider.Open(openCtx, c)
	if err != nil {
		a.Audit.Log(audit.Entry{Connection: c.Name, Kind: string(c.Kind), Action: "test_connection", OK: false, Error: err.Error()})
		return domain.ServerInfo{}, err
	}
	defer s.Close()

	info, err := s.ServerInfo(ctx)
	if err != nil {
		return domain.ServerInfo{}, err
	}
	a.Audit.Log(audit.Entry{Connection: c.Name, Kind: string(c.Kind), Action: "test_connection", OK: true})
	return info, nil
}

// TestSavedConnection 测试已保存的连接（自动读取凭证）。
func (a *App) TestSavedConnection(ctx context.Context, nameOrID string) (domain.ServerInfo, error) {
	c, err := a.cfgOf(nameOrID)
	if err != nil {
		return domain.ServerInfo{}, err
	}
	full, err := a.withPassword(c)
	if err != nil {
		return domain.ServerInfo{}, err
	}
	return a.TestConnection(ctx, full, full.Password)
}

// KindCapabilities 返回某数据库类型的能力集合，供表现层动态生成菜单。
func KindCapabilities(k domain.Kind) (domain.CapabilitySet, error) {
	return provider.Capabilities(k)
}
