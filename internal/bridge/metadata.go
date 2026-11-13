package bridge

import "github.com/db-fly/db-fly/internal/domain"

// ServerInfo 返回已连接服务端的版本信息。
func (b *Bridge) ServerInfo(conn string) (domain.ServerInfo, error) {
	return b.app.ServerInfo(b.ctx(), conn)
}

// ConnectionCapabilities 返回指定已连接会话的能力集合。
func (b *Bridge) ConnectionCapabilities(conn string) ([]string, error) {
	caps, err := b.app.Capabilities(b.ctx(), conn)
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

// ListDatabases 列出连接下的全部库。
func (b *Bridge) ListDatabases(conn string) ([]domain.DatabaseInfo, error) {
	return b.app.ListDatabases(b.ctx(), conn)
}

// ListSchemas 列出某库下的 schema（SQL Server / PG / Oracle）。
func (b *Bridge) ListSchemas(conn, database string) ([]string, error) {
	return b.app.ListSchemas(b.ctx(), conn, database)
}

// ListObjects 列出某库（或 schema）下的表 / 视图 / 集合 / 键。
func (b *Bridge) ListObjects(conn, database, schema string) ([]domain.ObjectInfo, error) {
	return b.app.ListObjects(b.ctx(), conn, database, schema)
}

// DescribeObject 返回对象的完整结构（字段 / 索引 / 外键 / DDL）。
func (b *Bridge) DescribeObject(req ObjectRequest) (domain.TableSchema, error) {
	return b.app.Describe(b.ctx(), req.Conn, req.Ref)
}
