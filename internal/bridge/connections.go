package bridge

import (
	"github.com/db-fly/db-fly/internal/app"
	"github.com/db-fly/db-fly/internal/domain"
	"github.com/db-fly/db-fly/internal/provider"
)

// ListConnections 返回全部连接定义（已剔除密码）。
func (b *Bridge) ListConnections() ([]domain.ConnectionConfig, error) {
	return b.app.ListConnections()
}

// SaveConnection 新增或更新连接。password 为空且 replacePassword=false 时保留已有凭证。
func (b *Bridge) SaveConnection(c domain.ConnectionConfig, password string, replacePassword bool) (domain.ConnectionConfig, error) {
	return b.app.SaveConnection(c, password, replacePassword)
}

// RemoveConnection 删除连接及其凭证。
func (b *Bridge) RemoveConnection(nameOrID string) (bool, error) {
	if err := b.app.RemoveConnection(nameOrID); err != nil {
		return false, err
	}
	return true, nil
}

// TestConnection 用表单参数临时测试连通性，不落库。
func (b *Bridge) TestConnection(c domain.ConnectionConfig, password string) (domain.ServerInfo, error) {
	return b.app.TestConnection(b.ctx(), c, password)
}

// TestSavedConnection 测试已保存连接（自动读取凭证）。
func (b *Bridge) TestSavedConnection(nameOrID string) (domain.ServerInfo, error) {
	return b.app.TestSavedConnection(b.ctx(), nameOrID)
}

// CloseConnection 关闭指定连接的缓存会话并释放底层连接池，用于手动节省资源。
func (b *Bridge) CloseConnection(nameOrID string) error {
	return b.app.CloseConnection(b.ctx(), nameOrID)
}

// OpenConnections 返回当前已建立会话（已打开）的连接名列表。
func (b *Bridge) OpenConnections() ([]string, error) {
	return b.app.OpenConnections(b.ctx())
}

// ListKinds 返回当前已注册的全部数据库类型及其能力，供连接类型下拉与菜单动态生成。
func (b *Bridge) ListKinds() ([]KindInfo, error) {
	var out []KindInfo
	for _, k := range provider.Kinds() {
		caps, err := provider.Capabilities(k)
		if err != nil {
			return nil, err
		}
		names := make([]string, 0, len(caps))
		for name, ok := range caps {
			if ok {
				names = append(names, string(name))
			}
		}
		out = append(out, KindInfo{
			Kind:         string(k),
			Label:        labelOf(k),
			DefaultPort:  domain.DefaultPort(k),
			Relational:   k.IsRelational(),
			FileBased:    k.IsFileBased(),
			Capabilities: names,
			Languages:    languagesOf(caps),
		})
	}
	return out, nil
}

// KindCapabilities 返回指定类型的能力集合（字符串列表）。
func (b *Bridge) KindCapabilities(kind string) ([]string, error) {
	caps, err := provider.Capabilities(domain.Kind(kind))
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(caps))
	for name, ok := range caps {
		if ok {
			names = append(names, string(name))
		}
	}
	return names, nil
}

// ValidateConnection 校验连接表单。返回 (true, nil) 表示通过。
func (b *Bridge) ValidateConnection(c domain.ConnectionConfig) (bool, error) {
	if err := app.ValidateConnection(c); err != nil {
		return false, err
	}
	return true, nil
}
