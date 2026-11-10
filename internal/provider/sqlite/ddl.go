package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/db-fly/db-fly/internal/domain"
	"github.com/db-fly/db-fly/internal/provider/sqlkit"
)

type ddlDriver struct {
	db *sql.DB
}

func (d *ddlDriver) Name() string { return "SQLite" }

func (d *ddlDriver) Qualified(ref domain.ObjectRef) (string, error) { return qualified(ref) }

func (d *ddlDriver) DatabaseOf(domain.ObjectRef) string { return "" }

// columnDef 遵循 SQLite 的 AUTOINCREMENT 约束：必须是 INTEGER PRIMARY KEY AUTOINCREMENT。
func (d *ddlDriver) columnDef(c domain.ColumnSpec, inlinePK bool) (string, error) {
	name, err := ansi.Ident(c.Name)
	if err != nil {
		return "", err
	}
	typ := strings.TrimSpace(c.Type)
	if typ == "" {
		return "", domain.NewError(domain.CodeValidation, "sqlite.script", "字段 %s 缺少类型", c.Name)
	}
	if err := sqlkit.ValidateFragment(typ, "字段类型"); err != nil {
		return "", err
	}
	if c.AutoIncrement && c.PrimaryKey {
		return name + " INTEGER PRIMARY KEY AUTOINCREMENT", nil
	}
	def := name + " " + typ
	if inlinePK && c.PrimaryKey {
		def += " PRIMARY KEY"
	}
	if c.Nullable {
		def += " NULL"
	} else {
		def += " NOT NULL"
	}
	if c.Default != nil {
		value := strings.TrimSpace(*c.Default)
		if err := sqlkit.ValidateFragment(value, "默认值"); err != nil {
			return "", err
		}
		def += " DEFAULT " + value
	}
	return def, nil
}

func (d *ddlDriver) CreateTable(def domain.TableDefinition) ([]string, error) {
	qualifiedName, err := qualified(def.Ref)
	if err != nil {
		return nil, err
	}
	if len(def.Columns) == 0 {
		return nil, domain.NewError(domain.CodeValidation, "sqlite.script", "建表至少需要定义一个字段")
	}
	primary := 0
	hasAutoPK := false
	for _, c := range def.Columns {
		if c.PrimaryKey {
			primary++
			if c.AutoIncrement {
				hasAutoPK = true
			}
		}
	}
	if hasAutoPK && primary > 1 {
		return nil, domain.NewError(domain.CodeValidation, "sqlite.script",
			"SQLite 的 AUTOINCREMENT 只能用于单列 INTEGER PRIMARY KEY")
	}
	inlinePK := primary == 1 && !hasAutoPK
	lines := make([]string, 0, len(def.Columns)+1)
	composite := make([]string, 0)
	for _, c := range def.Columns {
		line, err := d.columnDef(c, inlinePK)
		if err != nil {
			return nil, err
		}
		lines = append(lines, "    "+line)
		if c.PrimaryKey && !inlinePK && !hasAutoPK {
			quoted, _ := ansi.Ident(c.Name)
			composite = append(composite, quoted)
		}
	}
	if len(composite) > 0 {
		lines = append(lines, "    PRIMARY KEY ("+strings.Join(composite, ", ")+")")
	}
	stmt := "CREATE TABLE "
	if def.IfNotExists {
		stmt += "IF NOT EXISTS "
	}
	stmt += qualifiedName + " (\n" + strings.Join(lines, ",\n") + "\n)"
	statements := []string{stmt}
	for _, idx := range def.Indexes {
		index, err := d.createIndex(def.Ref, idx)
		if err != nil {
			return nil, err
		}
		statements = append(statements, index)
	}
	return statements, nil
}

func (d *ddlDriver) createIndex(ref domain.ObjectRef, idx domain.IndexSpec) (string, error) {
	name, err := ansi.Ident(idx.Name)
	if err != nil {
		return "", err
	}
	qualifiedName, err := qualified(ref)
	if err != nil {
		return "", err
	}
	columns, err := sqlkit.ColumnNames(ansi, idx.Columns)
	if err != nil {
		return "", err
	}
	kind := "CREATE INDEX"
	if idx.Unique {
		kind = "CREATE UNIQUE INDEX"
	}
	return kind + " IF NOT EXISTS " + name + " ON " + qualifiedName + " (" + columns + ")", nil
}

func (d *ddlDriver) AlterTable(ref domain.ObjectRef, changes []domain.SchemaChange) ([]string, []string, []string, error) {
	if len(changes) == 1 && changes[0].Kind == domain.ChangeRenameTable {
		return d.renameTable(ref, changes[0].NewName)
	}
	qualifiedName, err := qualified(ref)
	if err != nil {
		return nil, nil, nil, err
	}
	var statements, rollbacks, warnings []string
	for _, ch := range changes {
		switch ch.Kind {
		case domain.ChangeAddColumn:
			def, err := d.columnDef(ch.Column, true)
			if err != nil {
				return nil, nil, nil, err
			}
			if ch.Column.Default != nil {
				warnings = append(warnings, "SQLite 新增带默认值的列时不允许使用易变表达式。")
			}
			statements = append(statements, "ALTER TABLE "+qualifiedName+" ADD COLUMN "+def)
			col, _ := ansi.Ident(ch.Column.Name)
			rollbacks = append(rollbacks, "ALTER TABLE "+qualifiedName+" DROP COLUMN "+col)
			if ch.Column.After != "" {
				warnings = append(warnings, "SQLite 不支持 AFTER 指定列位置，新列会追加到末尾。")
			}

		case domain.ChangeModifyColumn:
			return nil, nil, nil, domain.NewError(domain.CodeUnsupported, "sqlite.script",
				"SQLite 不支持 MODIFY COLUMN，请新建表、迁移数据后重建（可在预览中查看生成的 CREATE TABLE）")

		case domain.ChangeRenameColumn:
			if strings.TrimSpace(ch.OldName) == "" {
				return nil, nil, nil, domain.NewError(domain.CodeValidation, "sqlite.script", "重命名字段需要填写原字段名")
			}
			old, err := ansi.Ident(ch.OldName)
			if err != nil {
				return nil, nil, nil, err
			}
			next, err := ansi.Ident(ch.Column.Name)
			if err != nil {
				return nil, nil, nil, err
			}
			statements = append(statements, "ALTER TABLE "+qualifiedName+" RENAME COLUMN "+old+" TO "+next)
			warnings = append(warnings, "重命名字段后请检查触发器、视图与索引定义。")

		case domain.ChangeDropColumn:
			col, err := ansi.Ident(ch.Column.Name)
			if err != nil {
				return nil, nil, nil, err
			}
			statements = append(statements, "ALTER TABLE "+qualifiedName+" DROP COLUMN "+col)
			warnings = append(warnings,
				fmt.Sprintf("删除字段 %s 需要 SQLite 3.35+，且会丢失全部数据，不可自动回滚。", ch.Column.Name))

		case domain.ChangeAddIndex:
			index, err := d.createIndex(ref, ch.Index)
			if err != nil {
				return nil, nil, nil, err
			}
			statements = append(statements, index)
			name, _ := ansi.Ident(ch.Index.Name)
			rollbacks = append(rollbacks, "DROP INDEX IF EXISTS "+name)

		case domain.ChangeDropIndex:
			name, err := ansi.Ident(ch.IndexName)
			if err != nil {
				return nil, nil, nil, err
			}
			statements = append(statements, "DROP INDEX IF EXISTS "+name)
			warnings = append(warnings, fmt.Sprintf("删除索引 %s 需人工恢复定义。", ch.IndexName))

		case domain.ChangeRenameTable:
			return nil, nil, nil, domain.NewError(domain.CodeValidation, "sqlite.script",
				"重命名表必须单独提交，不能与其他结构变更合并")

		case domain.ChangeTableOptions:
			warnings = append(warnings, "SQLite 不支持表级存储引擎/字符集/注释选项，已忽略。")

		default:
			return nil, nil, nil, domain.NewError(domain.CodeUnsupported, "sqlite.script",
				"SQLite 不支持变更类型 %q", ch.Kind)
		}
	}
	return statements, rollbacks, warnings, nil
}

func (d *ddlDriver) DropObject(ref domain.ObjectRef) ([]string, error) {
	qualifiedName, err := qualified(ref)
	if err != nil {
		return nil, err
	}
	return []string{"DROP TABLE IF EXISTS " + qualifiedName}, nil
}

// ShowCreate 直接返回 sqlite_master 中保存的原始建表语句。
func (d *ddlDriver) ShowCreate(ctx context.Context, ref domain.ObjectRef) (string, error) {
	var raw any
	if err := d.db.QueryRowContext(ctx,
		"SELECT sql FROM sqlite_master WHERE name = ? AND type IN ('table','view')",
		ref.Name).Scan(&raw); err != nil {
		if err == sql.ErrNoRows {
			return "", domain.NewError(domain.CodeNotFound, "sqlite.script",
				"对象 %s 不存在", ref.String())
		}
		return "", sqlkit.MapError("sqlite.script", err)
	}
	return trimSemicolon(sqlkit.Stringify(raw)), nil
}

// Truncate SQLite 没有 TRUNCATE，用 DELETE + 重置 sqlite_sequence 达到等效效果。
// renameTable 生成 ALTER TABLE ... RENAME TO 语句与改回原名的回滚脚本。
func (d *ddlDriver) renameTable(ref domain.ObjectRef, newName string) ([]string, []string, []string, error) {
	source, err := qualified(ref)
	if err != nil {
		return nil, nil, nil, err
	}
	newIdent, err := ansi.Ident(newName)
	if err != nil {
		return nil, nil, nil, err
	}
	target, err := qualified(domain.ObjectRef{Database: ref.Database, Name: newName})
	if err != nil {
		return nil, nil, nil, err
	}
	oldIdent, err := ansi.Ident(ref.Name)
	if err != nil {
		return nil, nil, nil, err
	}
	return []string{"ALTER TABLE " + source + " RENAME TO " + newIdent},
		[]string{"ALTER TABLE " + target + " RENAME TO " + oldIdent},
		[]string{"重命名表后请检查视图、触发器与索引定义，SQLite 不会自动迁移外部引用。"}, nil
}

func (d *ddlDriver) Truncate(qualifiedName string) []string {
	return []string{
		"DELETE FROM " + qualifiedName,
		"DELETE FROM sqlite_sequence WHERE name = '" + sqlkit.EscapeLiteral(unquoteName(qualifiedName)) + "'",
	}
}

func (d *ddlDriver) Empty(qualifiedName string) []string {
	return []string{"DELETE FROM " + qualifiedName}
}

func unquoteName(s string) string {
	if i := strings.LastIndexByte(s, '.'); i >= 0 {
		s = s[i+1:]
	}
	return strings.Trim(s, `"`)
}
