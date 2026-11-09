package app

import (
	"context"

	"github.com/dbclient/dbclient/internal/domain"
	"github.com/dbclient/dbclient/internal/platform/audit"
)

// 结构变更一律走「生成脚本 → 预览确认 → ApplyScript 执行」，不做静默 DDL。

func (a *App) PreviewCreateDatabase(ctx context.Context, conn string, spec domain.DatabaseSpec) (domain.Script, error) {
	s, err := a.Session(ctx, conn)
	if err != nil {
		return domain.Script{}, err
	}
	return s.Schema().CreateDatabaseScript(ctx, spec)
}

func (a *App) PreviewAlterDatabase(ctx context.Context, conn, name string, spec domain.DatabaseSpec) (domain.Script, error) {
	s, err := a.Session(ctx, conn)
	if err != nil {
		return domain.Script{}, err
	}
	return s.Schema().AlterDatabaseScript(ctx, name, spec)
}

func (a *App) PreviewCreateTable(ctx context.Context, conn string, def domain.TableDefinition) (domain.Script, error) {
	s, err := a.Session(ctx, conn)
	if err != nil {
		return domain.Script{}, err
	}
	return s.Schema().CreateTableScript(ctx, def)
}

func (a *App) PreviewAlterTable(ctx context.Context, conn string, ref domain.ObjectRef, changes []domain.SchemaChange) (domain.Script, error) {
	if len(changes) == 0 {
		return domain.Script{}, nil
	}
	s, err := a.Session(ctx, conn)
	if err != nil {
		return domain.Script{}, err
	}
	return s.Schema().AlterTableScript(ctx, ref, changes)
}

func (a *App) PreviewDropObject(ctx context.Context, conn string, ref domain.ObjectRef) (domain.Script, error) {
	s, err := a.Session(ctx, conn)
	if err != nil {
		return domain.Script{}, err
	}
	return s.Schema().DropObjectScript(ctx, ref)
}

func (a *App) ShowCreate(ctx context.Context, conn string, ref domain.ObjectRef) (string, error) {
	s, err := a.Session(ctx, conn)
	if err != nil {
		return "", err
	}
	return s.Schema().ShowCreate(ctx, ref)
}

// ApplyScript 执行已经过用户确认的脚本。只读连接一律拒绝 DDL/DML 脚本。
func (a *App) ApplyScript(ctx context.Context, conn string, script domain.Script) (domain.ApplyResult, error) {
	if len(script.Statements) == 0 {
		return domain.ApplyResult{}, nil
	}
	c, err := a.cfgOf(conn)
	if err != nil {
		return domain.ApplyResult{}, err
	}
	if c.ReadOnly {
		return domain.ApplyResult{}, domain.NewError(domain.CodeReadOnly, "apply_script",
			"只读连接禁止执行结构变更脚本")
	}

	s, err := a.Session(ctx, conn)
	if err != nil {
		return domain.ApplyResult{}, err
	}
	result, err := s.Schema().Apply(ctx, script)
	a.Audit.Log(audit.Entry{
		Connection: c.Name, Kind: string(c.Kind), Action: "apply_script",
		Statement: script.Statements[0], OK: err == nil,
		Error: errorText(err),
	})
	if err != nil {
		return domain.ApplyResult{}, err
	}
	return result, nil
}

func errorText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
