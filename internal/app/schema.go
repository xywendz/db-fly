package app

import (
	"context"
	"fmt"
	"regexp"
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

var relationalKinds = map[string]bool{
	"mysql": true, "postgresql": true, "sqlserver": true, "oracle": true, "sqlite": true,
}

// PreviewCopyTable 生成「复制表结构」或「复制表结构和数据」的可预览脚本。
// 结构通过 SHOW CREATE TABLE 原样复制（保留默认值 / 索引 / 约束等），数据用 INSERT ... SELECT *。
func (a *App) PreviewCopyTable(ctx context.Context, conn string, req domain.CopyTableRequest) (domain.Script, error) {
	ref := req.Ref
	newName := strings.TrimSpace(req.NewName)
	withData := req.WithData
	if newName == "" || newName == ref.Name {
		return domain.Script{}, domain.NewError(domain.CodeValidation, "copy_table", "请填写与原表不同的新表名")
	}
	c, err := a.cfgOf(conn)
	if err != nil {
		return domain.Script{}, err
	}
	if c.ReadOnly {
		return domain.Script{}, domain.NewError(domain.CodeReadOnly, "copy_table", "只读连接禁止复制表")
	}
	if !relationalKinds[string(c.Kind)] {
		return domain.Script{}, domain.NewError(domain.CodeUnsupported, "copy_table", "%s 暂不支持复制表结构", string(c.Kind))
	}
	s, err := a.Session(ctx, conn)
	if err != nil {
		return domain.Script{}, err
	}
	if s.Schema() == nil {
		return domain.Script{}, domain.NewError(domain.CodeUnsupported, "copy_table", "当前数据库类型不支持复制表结构")
	}
	// 校验原表存在（同时拿到权限/存在性错误信息）。
	if _, err := a.Describe(ctx, conn, ref); err != nil {
		return domain.Script{}, err
	}
	ddl, err := s.Schema().ShowCreate(ctx, ref)
	if err != nil {
		return domain.Script{}, err
	}
	newQualified := qualifiedByKind(string(c.Kind), ref.Database, newName)
	oldQualified := qualifiedByKind(string(c.Kind), ref.Database, ref.Name)
	createStmt, err := copyRenameDDL(ddl, string(c.Kind), ref.Name, newName, ref.Database)
	if err != nil {
		return domain.Script{}, err
	}

	statements := []string{createStmt}
	warnings := []string{"回滚脚本会删除整张新表；若复制了数据则数据一并丢失。"}
	if withData {
		statements = append(statements, fmt.Sprintf("INSERT INTO %s SELECT * FROM %s", newQualified, oldQualified))
		warnings = append(warnings, "将把原表的全部数据复制到新表；大表可能耗时较长且可能产生大量日志。")
	}
	return domain.Script{
		Format:     "sql",
		Statements: statements,
		Database:   ref.Database,
		Rollback:   []string{fmt.Sprintf("DROP TABLE IF EXISTS %s", newQualified)},
		Warnings:   warnings,
	}, nil
}

func quoteIdentByKind(kind, name string) string {
	switch kind {
	case "sqlserver":
		return "[" + strings.ReplaceAll(name, "]", "]]") + "]"
	case "postgresql", "oracle", "sqlite":
		return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
	default: // mysql 等反引号风格
		return "`" + strings.ReplaceAll(name, "`", "``") + "`"
	}
}

func qualifiedByKind(kind, db, name string) string {
	if db == "" {
		return quoteIdentByKind(kind, name)
	}
	return quoteIdentByKind(kind, db) + "." + quoteIdentByKind(kind, name)
}

// copyRenameDDL 在 SHOW CREATE TABLE 的原生建表语句中，把旧表名替换为新表名，
// 并确保新表建在正确的库（db）下：DDL 若已带库前缀则整体替换，否则补上库前缀，
// 避免新建表落到当前会话默认库而导致后续 INSERT 找不到目标表。
func copyRenameDDL(ddl, kind, oldName, newName, db string) (string, error) {
	oldQuoted := quoteIdentByKind(kind, oldName)
	oldQualified := qualifiedByKind(kind, db, oldName)
	newQualified := qualifiedByKind(kind, db, newName)

	// 情况1：DDL 已带库前缀（如 `db`.`old`），整体替换为 `db`.`new`。
	if strings.Contains(ddl, oldQualified) {
		return strings.Replace(ddl, oldQualified, newQualified, 1), nil
	}
	// 情况2：DDL 仅含表名（如 `old`），替换表名并补上库前缀。
	if strings.Contains(ddl, oldQuoted) {
		return strings.Replace(ddl, oldQuoted, newQualified, 1), nil
	}
	// 回退：用正则整体替换 CREATE TABLE 后的标识符为带库前缀的新名。
	re := regexp.MustCompile(`(?i)(CREATE\s+TABLE\s+(?:IF\s+NOT\s+EXISTS\s+)?)([^\s(]+)`)
	if re.MatchString(ddl) {
		return re.ReplaceAllString(ddl, "${1}"+newQualified), nil
	}
	return "", domain.NewError(domain.CodeUnsupported, "copy_table", "无法解析原表建表语句以重命名新表")
}
