package app

import (
	"context"
	"strings"

	"github.com/db-fly/db-fly/internal/domain"
	"github.com/db-fly/db-fly/internal/platform/audit"
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

// PreviewRenameTable 生成重命名表的方言脚本；与其他结构变更一样先预览再显式确认执行。
func (a *App) PreviewRenameTable(ctx context.Context, conn string, ref domain.ObjectRef, newName string) (domain.Script, error) {
	newName = strings.TrimSpace(newName)
	if newName == "" || newName == ref.Name {
		return domain.Script{}, domain.NewError(domain.CodeValidation, "rename_table",
			"请填写与原名称不同的新表名")
	}
	return a.PreviewAlterTable(ctx, conn, ref, []domain.SchemaChange{
		{Kind: domain.ChangeRenameTable, NewName: newName},
	})
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

// PreviewTableAction 生成 TRUNCATE / DELETE FROM 的可预览脚本。
// 清空数据属于危险操作，必须走「脚本预览 → 用户确认 → ApplyScript」流程。
func (a *App) PreviewTableAction(ctx context.Context, conn string, ref domain.ObjectRef, action domain.TableAction) (domain.Script, error) {
	if !action.Valid() {
		return domain.Script{}, domain.NewError(domain.CodeValidation, "table_action",
			"不支持的清空动作 %q（可用 truncate/empty）", action)
	}
	c, err := a.cfgOf(conn)
	if err != nil {
		return domain.Script{}, err
	}
	if c.ReadOnly {
		return domain.Script{}, domain.NewError(domain.CodeReadOnly, "table_action",
			"只读连接禁止清空表数据")
	}
	s, err := a.Session(ctx, conn)
	if err != nil {
		return domain.Script{}, err
	}
	maintenance := s.Maintenance()
	if maintenance == nil {
		return domain.Script{}, domain.NewError(domain.CodeUnsupported, "table_action",
			"当前数据库模块不支持 TRUNCATE / 清空数据操作")
	}
	return maintenance.TableActionScript(ctx, ref, action)
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
