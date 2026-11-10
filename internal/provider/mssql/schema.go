package mssql

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"strings"

	"github.com/db-fly/db-fly/internal/domain"
)

// schemaEditor 把结构化编辑翻译成 T-SQL 脚本。
// 生成的脚本首条语句为 USE [db]，Apply 会在独占连接上顺序执行以保证上下文一致。
type schemaEditor struct {
	db *sql.DB
}

var (
	numericDefault = regexp.MustCompile(`^-?\d+(\.\d+)?$`)
	keywordDefault = regexp.MustCompile(`(?i)^(NULL|GETDATE\(\)|SYSDATETIME\(\)|NEWID\(\)|CURRENT_TIMESTAMP|TRUE|FALSE|0|1)$`)
)

func useStatement(database string) ([]string, error) {
	if database == "" {
		return nil, nil
	}
	q, err := quoteIdent(database)
	if err != nil {
		return nil, err
	}
	return []string{"USE " + q}, nil
}

func (e *schemaEditor) CreateDatabaseScript(ctx context.Context, spec domain.DatabaseSpec) (domain.Script, error) {
	name, err := quoteIdent(spec.Name)
	if err != nil {
		return domain.Script{}, err
	}
	if err := safeToken(spec.Collation, "排序规则"); err != nil {
		return domain.Script{}, err
	}
	create := "CREATE DATABASE " + name
	if spec.Collation != "" {
		create += " COLLATE " + spec.Collation
	}
	if spec.Charset != "" {
		return domain.Script{}, domain.NewError(domain.CodeUnsupported, "sqlserver.script",
			"SQL Server 使用排序规则（COLLATE）而非字符集，请改用 --collation")
	}
	stmt := create
	if spec.IfNotExists {
		stmt = "IF DB_ID(N'" + escapeLiteral(spec.Name) + "') IS NULL\nBEGIN\n    " + create + "\nEND"
	}
	return domain.Script{
		Format:     "sql",
		Statements: []string{stmt},
		Rollback:   []string{"DROP DATABASE " + name},
		Warnings:   []string{"回滚脚本会删除整个数据库及其全部数据。"},
	}, nil
}

func (e *schemaEditor) AlterDatabaseScript(ctx context.Context, name string, spec domain.DatabaseSpec) (domain.Script, error) {
	quoted, err := quoteIdent(name)
	if err != nil {
		return domain.Script{}, err
	}
	if err := safeToken(spec.Collation, "排序规则"); err != nil {
		return domain.Script{}, err
	}
	if spec.Collation == "" {
		return domain.Script{}, domain.NewError(domain.CodeValidation, "sqlserver.script",
			"请指定新的排序规则（SQL Server 通过 COLLATE 修改库属性）")
	}
	return domain.Script{
		Format:     "sql",
		Statements: []string{"ALTER DATABASE " + quoted + " COLLATE " + spec.Collation},
		Warnings:   []string{"修改排序规则不会自动转换存量数据；无法自动生成回滚脚本。"},
	}, nil
}

func (e *schemaEditor) CreateTableScript(ctx context.Context, def domain.TableDefinition) (domain.Script, error) {
	schema := defaultSchema(def.Ref.Schema)
	qualified, err := quoteQualified("", schema, def.Ref.Name)
	if err != nil {
		return domain.Script{}, err
	}
	if len(def.Columns) == 0 {
		return domain.Script{}, domain.NewError(domain.CodeValidation, "sqlserver.script", "建表至少需要定义一个字段")
	}
	use, err := useStatement(def.Ref.Database)
	if err != nil {
		return domain.Script{}, err
	}

	lines := make([]string, 0, len(def.Columns)+len(def.Indexes)+1)
	primaryKeys := make([]string, 0)
	for _, c := range def.Columns {
		line, err := columnDefinition(c)
		if err != nil {
			return domain.Script{}, err
		}
		lines = append(lines, "    "+line)
		if c.PrimaryKey {
			q, err := quoteIdent(c.Name)
			if err != nil {
				return domain.Script{}, err
			}
			primaryKeys = append(primaryKeys, q)
		}
	}
	if len(primaryKeys) > 0 {
		pkName, err := quoteIdent("PK_" + def.Ref.Name)
		if err != nil {
			return domain.Script{}, err
		}
		lines = append(lines, "    CONSTRAINT "+pkName+" PRIMARY KEY ("+strings.Join(primaryKeys, ", ")+")")
	}
	create := "CREATE TABLE " + qualified + " (\n" + strings.Join(lines, ",\n") + "\n)"
	if def.IfNotExists {
		create = "IF OBJECT_ID(N'" + escapeLiteral(qualified) + "', N'U') IS NULL\nBEGIN\n" + create + "\nEND"
	}
	statements := append(use, create)

	// SQL Server 的索引是独立语句，放在建表之后。
	for _, idx := range def.Indexes {
		stmt, err := createIndexStatement(qualified, idx)
		if err != nil {
			return domain.Script{}, err
		}
		statements = append(statements, stmt)
	}
	return domain.Script{
		Format:     "sql",
		Statements: statements,
		Rollback:   []string{"DROP TABLE " + qualified},
		Warnings:   []string{"脚本首条 USE 语句用于确保表创建在目标数据库中。"},
	}, nil
}

func (e *schemaEditor) AlterTableScript(ctx context.Context, ref domain.ObjectRef, changes []domain.SchemaChange) (domain.Script, error) {
	if len(changes) == 1 && changes[0].Kind == domain.ChangeRenameTable {
		return e.renameTableScript(ref, changes[0].NewName)
	}
	schema := defaultSchema(ref.Schema)
	qualified, err := quoteQualified("", schema, ref.Name)
	if err != nil {
		return domain.Script{}, err
	}
	use, err := useStatement(ref.Database)
	if err != nil {
		return domain.Script{}, err
	}
	statements := append([]string{}, use...)
	var rollbacks []string
	var warnings []string

	for _, ch := range changes {
		switch ch.Kind {
		case domain.ChangeAddColumn:
			def, err := columnDefinition(ch.Column)
			if err != nil {
				return domain.Script{}, err
			}
			statements = append(statements, "ALTER TABLE "+qualified+" ADD "+def)
			col, _ := quoteIdent(ch.Column.Name)
			rollbacks = append(rollbacks, "ALTER TABLE "+qualified+" DROP COLUMN "+col)

		case domain.ChangeModifyColumn:
			def, err := columnDefinition(ch.Column)
			if err != nil {
				return domain.Script{}, err
			}
			statements = append(statements, "ALTER TABLE "+qualified+" ALTER COLUMN "+def)
			warnings = append(warnings,
				fmt.Sprintf("修改字段 %s 前可能需要删除其默认约束或索引；原定义无法自动回滚。", ch.Column.Name))

		case domain.ChangeRenameColumn:
			if strings.TrimSpace(ch.OldName) == "" {
				return domain.Script{}, domain.NewError(domain.CodeValidation, "sqlserver.script", "重命名字段需要填写原字段名")
			}
			statements = append(statements,
				"EXEC sp_rename N'"+escapeLiteral(qualified+"."+ch.OldName)+"', N'"+escapeLiteral(ch.Column.Name)+"', N'COLUMN'")
			warnings = append(warnings, "sp_rename 不会自动更新引用该字段的视图、存储过程与脚本。")

		case domain.ChangeDropColumn:
			col, err := quoteIdent(ch.Column.Name)
			if err != nil {
				return domain.Script{}, err
			}
			statements = append(statements, "ALTER TABLE "+qualified+" DROP COLUMN "+col)
			warnings = append(warnings, fmt.Sprintf("删除字段 %s 会丢失全部数据，不可自动回滚。", ch.Column.Name))

		case domain.ChangeAddIndex:
			stmt, err := createIndexStatement(qualified, ch.Index)
			if err != nil {
				return domain.Script{}, err
			}
			statements = append(statements, stmt)
			name, _ := quoteIdent(ch.Index.Name)
			rollbacks = append(rollbacks, "DROP INDEX "+name+" ON "+qualified)

		case domain.ChangeDropIndex:
			name, err := quoteIdent(ch.IndexName)
			if err != nil {
				return domain.Script{}, err
			}
			statements = append(statements, "DROP INDEX "+name+" ON "+qualified)
			warnings = append(warnings, fmt.Sprintf("删除索引 %s 需人工恢复定义。", ch.IndexName))

		case domain.ChangeRenameTable:
			return domain.Script{}, domain.NewError(domain.CodeValidation, "sqlserver.script",
				"重命名表必须单独提交，不能与其他结构变更合并")

		case domain.ChangeTableOptions:
			if ch.Comment != "" {
				name, err := quoteIdent(ref.Name)
				if err != nil {
					return domain.Script{}, err
				}
				statements = append(statements,
					"EXEC sys.sp_addextendedproperty @name=N'MS_Description', @value=N'"+escapeLiteral(ch.Comment)+
						"', @level0type=N'SCHEMA', @level0name=N'"+escapeLiteral(schema)+
						"', @level1type=N'TABLE', @level1name=N'"+escapeLiteral(ref.Name)+"'"+
						" -- "+name)
			}
			if ch.Engine != "" || ch.Charset != "" {
				warnings = append(warnings, "SQL Server 不支持存储引擎与表级字符集选项，已忽略。")
			}

		default:
			return domain.Script{}, domain.NewError(domain.CodeUnsupported, "sqlserver.script",
				"SQL Server 不支持变更类型 %q", ch.Kind)
		}
	}
	for i, j := 0, len(rollbacks)-1; i < j; i, j = i+1, j-1 {
		rollbacks[i], rollbacks[j] = rollbacks[j], rollbacks[i]
	}
	return domain.Script{
		Format:     "sql",
		Statements: statements,
		Rollback:   rollbacks,
		Warnings:   warnings,
	}, nil
}

// renameTableScript 通过 sp_rename 重命名表；sp_rename 的第一个参数是「schema.表名」字符串。
func (e *schemaEditor) renameTableScript(ref domain.ObjectRef, newName string) (domain.Script, error) {
	if _, err := quoteIdent(newName); err != nil {
		return domain.Script{}, err
	}
	use, err := useStatement(ref.Database)
	if err != nil {
		return domain.Script{}, err
	}
	schema := defaultSchema(ref.Schema)
	oldFull := schema + "." + ref.Name
	newFull := schema + "." + newName
	rename := func(from, to string) string {
		return "EXEC sp_rename N'" + escapeLiteral(from) + "', N'" + escapeLiteral(to) + "', N'OBJECT'"
	}
	statements := append(append([]string{}, use...), rename(oldFull, newName))
	return domain.Script{
		Format:     "sql",
		Statements: statements,
		Rollback:   []string{rename(newFull, ref.Name)},
		Warnings: []string{
			"sp_rename 不会自动更新引用该表的视图、存储过程与脚本，请人工复核。",
		},
	}, nil
}

func (e *schemaEditor) DropObjectScript(ctx context.Context, ref domain.ObjectRef) (domain.Script, error) {
	qualified, err := quoteQualified("", defaultSchema(ref.Schema), ref.Name)
	if err != nil {
		return domain.Script{}, err
	}
	use, err := useStatement(ref.Database)
	if err != nil {
		return domain.Script{}, err
	}
	return domain.Script{
		Format:     "sql",
		Statements: append(use, "DROP TABLE "+qualified),
		Warnings:   []string{"删除表不可恢复；若要删除视图请改用 DROP VIEW。"},
	}, nil
}

// ShowCreate 由元数据反向生成建表脚本（SQL Server 没有 SHOW CREATE TABLE）。
func (e *schemaEditor) ShowCreate(ctx context.Context, ref domain.ObjectRef) (string, error) {
	schema, err := (&metadataBrowser{db: e.db}).Describe(ctx, ref)
	if err != nil {
		return "", err
	}
	return renderCreateTable(schema), nil
}

func renderCreateTable(schema domain.TableSchema) string {
	qualified, err := quoteQualified("", defaultSchema(schema.Ref.Schema), schema.Ref.Name)
	if err != nil {
		return ""
	}
	lines := make([]string, 0, len(schema.Columns)+1)
	for _, c := range schema.Columns {
		col, err := quoteIdent(c.Name)
		if err != nil {
			continue
		}
		line := col + " " + strings.ToUpper(c.DataType)
		if c.Length > 0 && strings.Contains(strings.ToUpper(c.DataType), "CHAR") {
			line += "(" + fmt.Sprintf("%d", c.Length) + ")"
		}
		if c.AutoIncrement {
			line += " IDENTITY(1,1)"
		}
		if c.Nullable {
			line += " NULL"
		} else {
			line += " NOT NULL"
		}
		if c.HasDefault && c.Default != "" {
			line += " DEFAULT " + c.Default
		}
		lines = append(lines, "    "+line)
	}
	pk := make([]string, 0)
	for _, c := range schema.Columns {
		if c.PrimaryKey {
			col, err := quoteIdent(c.Name)
			if err == nil {
				pk = append(pk, col)
			}
		}
	}
	if len(pk) > 0 {
		lines = append(lines, "    CONSTRAINT [PK_"+schema.Ref.Name+"] PRIMARY KEY ("+strings.Join(pk, ", ")+")")
	}
	return "CREATE TABLE " + qualified + " (\n" + strings.Join(lines, ",\n") + "\n)"
}

// Apply 在独占连接上顺序执行脚本，并对每条语句做 GO 拆批。
func (e *schemaEditor) Apply(ctx context.Context, script domain.Script) (domain.ApplyResult, error) {
	result := domain.ApplyResult{Outcomes: make([]domain.ExecOutcome, 0, len(script.Statements))}
	if len(script.Statements) == 0 {
		return result, nil
	}
	conn, err := e.db.Conn(ctx)
	if err != nil {
		return result, mapQueryError(err)
	}
	defer conn.Close()

	for _, statement := range script.Statements {
		for _, batch := range splitBatches(statement) {
			r, err := conn.ExecContext(ctx, batch)
			if err != nil {
				return result, mapQueryError(err)
			}
			affected, _ := r.RowsAffected()
			result.Outcomes = append(result.Outcomes, domain.ExecOutcome{
				Statement: batch, RowsAffected: affected, Message: "执行成功",
			})
		}
	}
	return result, nil
}

func columnDefinition(c domain.ColumnSpec) (string, error) {
	name, err := quoteIdent(c.Name)
	if err != nil {
		return "", err
	}
	typ := strings.TrimSpace(c.Type)
	if typ == "" {
		return "", domain.NewError(domain.CodeValidation, "sqlserver.script", "字段 %s 缺少类型", c.Name)
	}
	if err := validateFragment(typ, "字段类型"); err != nil {
		return "", err
	}
	def := name + " " + typ
	if c.AutoIncrement {
		def += " IDENTITY(1,1)"
	}
	if c.Nullable {
		def += " NULL"
	} else {
		def += " NOT NULL"
	}
	if c.Default != nil {
		value := strings.TrimSpace(*c.Default)
		if err := validateFragment(value, "默认值"); err != nil {
			return "", err
		}
		if numericDefault.MatchString(value) || keywordDefault.MatchString(value) {
			def += " DEFAULT (" + value + ")"
		} else {
			def += " DEFAULT (N'" + escapeLiteral(value) + "')"
		}
	}
	return def, nil
}

func createIndexStatement(qualified string, idx domain.IndexSpec) (string, error) {
	name, err := quoteIdent(idx.Name)
	if err != nil {
		return "", err
	}
	if len(idx.Columns) == 0 {
		return "", domain.NewError(domain.CodeValidation, "sqlserver.script", "索引 %s 至少需要一列", idx.Name)
	}
	cols, err := quotedColumns(idx.Columns)
	if err != nil {
		return "", err
	}
	kind := "CREATE INDEX"
	if idx.Unique {
		kind = "CREATE UNIQUE INDEX"
	}
	return kind + " " + name + " ON " + qualified + " (" + strings.Join(cols, ", ") + ")", nil
}
