package app

import (
	"context"

	"github.com/db-fly/db-fly/internal/domain"
)

// ServerInfo 返回已连接服务端的版本信息。
func (a *App) ServerInfo(ctx context.Context, conn string) (domain.ServerInfo, error) {
	s, err := a.Session(ctx, conn)
	if err != nil {
		return domain.ServerInfo{}, err
	}
	return s.ServerInfo(ctx)
}

// Capabilities 返回当前会话所属业务模块的能力集合。
func (a *App) Capabilities(ctx context.Context, conn string) (domain.CapabilitySet, error) {
	s, err := a.Session(ctx, conn)
	if err != nil {
		return nil, err
	}
	return s.Capabilities(), nil
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
