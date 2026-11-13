package app

import (
	"context"

	"github.com/db-fly/db-fly/internal/domain"
	"github.com/db-fly/db-fly/internal/provider"
)

// ServerInfo 返回已连接服务端的版本信息。
func (a *App) ServerInfo(ctx context.Context, conn string) (domain.ServerInfo, error) {
	s, err := a.Session(ctx, conn)
	if err != nil {
		return domain.ServerInfo{}, err
	}
	return s.ServerInfo(ctx)
}

// Capabilities 返回指定连接的能力集合。能力由数据库类型静态决定，无需建立会话，
// 避免前端加载连接列表时为每个连接都打开一个真实连接。
func (a *App) Capabilities(ctx context.Context, conn string) (domain.CapabilitySet, error) {
	c, err := a.cfgOf(conn)
	if err != nil {
		return nil, err
	}
	return provider.Capabilities(c.Kind)
}

func (a *App) ListDatabases(ctx context.Context, conn string) ([]domain.DatabaseInfo, error) {
	s, err := a.Session(ctx, conn)
	if err != nil {
		return nil, err
	}
	return s.Metadata().ListDatabases(ctx)
}

func (a *App) ListSchemas(ctx context.Context, conn, database string) ([]string, error) {
	s, err := a.Session(ctx, conn)
	if err != nil {
		return nil, err
	}
	return s.Metadata().ListSchemas(ctx, database)
}

func (a *App) ListObjects(ctx context.Context, conn, database, schema string) ([]domain.ObjectInfo, error) {
	s, err := a.Session(ctx, conn)
	if err != nil {
		return nil, err
	}
	return s.Metadata().ListObjects(ctx, database, schema)
}

func (a *App) Describe(ctx context.Context, conn string, ref domain.ObjectRef) (domain.TableSchema, error) {
	s, err := a.Session(ctx, conn)
	if err != nil {
		return domain.TableSchema{}, err
	}
	return s.Metadata().Describe(ctx, ref)
}
