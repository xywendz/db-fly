package mysql

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/dbclient/dbclient/internal/domain"
)

// schemaEditor 只在本模块内部把「结构化编辑」翻译成 MySQL 方言 DDL。
type schemaEditor struct {
	db *sql.DB
}

var (
	numericDefault = regexp.MustCompile(`^-?\d+(\.\d+)?$`)
	keywordDefault = regexp.MustCompile(`(?i)^(NULL|CURRENT_TIMESTAMP(\(\))?|NOW\(\)|UUID\(\)|TRUE|FALSE)$`)
)

func (e *schemaEditor) CreateDatabaseScript(_ context.Context, spec domain.DatabaseSpec) (domain.Script, error) {
	name, err := quoteIdent(spec.Name)
	if err != nil {
		return domain.Script{}, err
	}
	if err := safeToken(spec.Charset, "字符集"); err != nil {
		return domain.Script{}, err
	}
	if err := safeToken(spec.Collation, "排序规则"); err != nil {
		return domain.Script{}, err
	}

	var b strings.Builder
	b.WriteString("CREATE DATABASE ")
	if spec.IfNotExists {
		b.WriteString("IF NOT EXISTS ")
	}
	b.WriteString(name)
	if spec.Charset != "" {
		b.WriteString(" CHARACTER SET " + spec.Charset)
	}
	if spec.Collation != "" {
		b.WriteString(" COLLATE " + spec.Collation)
	}
	return domain.Script{
		Statements: []string{b.String()},
		Rollback:   []string{"DROP DATABASE " + name},
		Warnings:   []string{"回滚脚本会删除整个数据库及其全部数据，请谨慎使用。"},
	}, nil
}

func (e *schemaEditor) AlterDatabaseScript(_ context.Context, name string, spec domain.DatabaseSpec) (domain.Script, error) {
	quoted, err := quoteIdent(name)
	if err != nil {
		return domain.Script{}, err
	}
	if err := safeToken(spec.Charset, "字符集"); err != nil {
		return domain.Script{}, err
	}
	if err := safeToken(spec.Collation, "排序规则"); err != nil {
		return domain.Script{}, err
	}
	parts := make([]string, 0, 2)
	if spec.Charset != "" {
		parts = append(parts, "CHARACTER SET = "+spec.Charset)
	}
	if spec.Collation != "" {
		parts = append(parts, "COLLATE = "+spec.Collation)
	}
	if len(parts) == 0 {
		return domain.Script{}, domain.NewError(domain.CodeValidation, "mysql.script",
			"没有需要修改的数据库选项")
	}
	return domain.Script{
		Statements: []string{"ALTER DATABASE " + quoted + " " + strings.Join(parts, " ")},
		Warnings:   []string{"修改已有库的字符集不会自动转换存量数据；无法自动生成回滚脚本。"},
	}, nil
}

func (e *schemaEditor) CreateTableScript(_ context.Context, def domain.TableDefinition) (domain.Script, error) {
	qualified, err := quoteQualified(def.Ref.Database, def.Ref.Name)
	if err != nil {
		return domain.Script{}, err
	}
	if len(def.Columns) == 0 {
		return domain.Script{}, domain.NewError(domain.CodeValidation, "mysql.script", "建表至少需要定义一个字段")
	}

	lines := make([]string, 0, len(def.Columns)+len(def.Indexes)+1)
	primaryKeys := make([]string, 0)
	for _, c := range def.Columns {
		line, err := columnDefinition(c)
		if err != nil {
			return domain.Script{}, err
		}
		lines = append(lines, "  "+line)
		if c.PrimaryKey {
			q, err := quoteIdent(c.Name)
			if err != nil {
				return domain.Script{}, err
			}
			primaryKeys = append(primaryKeys, q)
		}
	}
	for _, idx := range def.Indexes {
		line, err := indexDefinition(idx)
		if err != nil {
			return domain.Script{}, err
		}
		lines = append(lines, "  "+line)
	}
	if len(primaryKeys) > 0 {
		lines = append(lines, "  PRIMARY KEY ("+strings.Join(primaryKeys, ", ")+")")
	}

	stmt := "CREATE TABLE "
	if def.IfNotExists {
		stmt += "IF NOT EXISTS "
	}
	stmt += qualified + " (\n" + strings.Join(lines, ",\n") + "\n)"
	if options, err := tableOptionParts(domain.SchemaChange{
		Engine: def.Engine, Charset: def.Charset, Collation: def.Collation, Comment: def.Comment,
	}); err != nil {
		return domain.Script{}, err
	} else if len(options) > 0 {
		stmt += " " + strings.Join(options, " ")
	}

	return domain.Script{
		Statements: []string{stmt},
		Rollback:   []string{"DROP TABLE IF EXISTS " + qualified},
		Warnings:   []string{"回滚脚本会删除整张表及全部数据。"},
	}, nil
}

func (e *schemaEditor) AlterTableScript(_ context.Context, ref domain.ObjectRef, changes []domain.SchemaChange) (domain.Script, error) {
	qualified, err := quoteQualified(ref.Database, ref.Name)
	if err != nil {
		return domain.Script{}, err
	}
	var (
		clauses   []string
		rollbacks []string
		warnings  []string
	)
	for _, ch := range changes {
		switch ch.Kind {
		case domain.ChangeAddColumn:
			def, err := columnDefinition(ch.Column)
			if err != nil {
				return domain.Script{}, err
			}
			clause := "ADD COLUMN " + def
			if ch.Column.After != "" {
				after, err := quoteIdent(ch.Column.After)
				if err != nil {
					return domain.Script{}, err
				}
				clause += " AFTER " + after
			}
			clauses = append(clauses, clause)
			col, _ := quoteIdent(ch.Column.Name)
			rollbacks = append(rollbacks, "ALTER TABLE "+qualified+" DROP COLUMN "+col)

		case domain.ChangeModifyColumn:
			def, err := columnDefinition(ch.Column)
			if err != nil {
				return domain.Script{}, err
			}
			clauses = append(clauses, "MODIFY COLUMN "+def)
			warnings = append(warnings, fmt.Sprintf("字段 %s 的原定义未纳入回滚脚本，请在执行前备份。", ch.Column.Name))

		case domain.ChangeRenameColumn:
			if strings.TrimSpace(ch.OldName) == "" {
				return domain.Script{}, domain.NewError(domain.CodeValidation, "mysql.script", "重命名字段需要填写原字段名")
			}
			old, err := quoteIdent(ch.OldName)
			if err != nil {
				return domain.Script{}, err
			}
			def, err := columnDefinition(ch.Column)
			if err != nil {
				return domain.Script{}, err
			}
			clauses = append(clauses, "CHANGE COLUMN "+old+" "+def)
			warnings = append(warnings, fmt.Sprintf("字段 %s 重命名需人工编写回滚脚本。", ch.OldName))

		case domain.ChangeDropColumn:
			col, err := quoteIdent(ch.Column.Name)
			if err != nil {
				return domain.Script{}, err
			}
			clauses = append(clauses, "DROP COLUMN "+col)
			warnings = append(warnings, fmt.Sprintf("删除字段 %s 会丢失该列全部数据，不可自动回滚。", ch.Column.Name))

		case domain.ChangeAddIndex:
			def, err := indexDefinition(ch.Index)
			if err != nil {
				return domain.Script{}, err
			}
			clauses = append(clauses, "ADD "+def)
			name, _ := quoteIdent(ch.Index.Name)
			rollbacks = append(rollbacks, "ALTER TABLE "+qualified+" DROP INDEX "+name)

		case domain.ChangeDropIndex:
			name, err := quoteIdent(ch.IndexName)
			if err != nil {
				return domain.Script{}, err
			}
			clauses = append(clauses, "DROP INDEX "+name)
			warnings = append(warnings, fmt.Sprintf("删除索引 %s 需人工恢复定义。", ch.IndexName))

		case domain.ChangeTableOptions:
			parts, err := tableOptionParts(ch)
			if err != nil {
				return domain.Script{}, err
			}
			clauses = append(clauses, parts...)

		default:
			return domain.Script{}, domain.NewError(domain.CodeUnsupported, "mysql.script",
				"MySQL 不支持变更类型 %q", ch.Kind)
		}
	}
	if len(clauses) == 0 {
		return domain.Script{}, nil
	}
	for i, j := 0, len(rollbacks)-1; i < j; i, j = i+1, j-1 {
		rollbacks[i], rollbacks[j] = rollbacks[j], rollbacks[i]
	}
	return domain.Script{
		Statements: []string{"ALTER TABLE " + qualified + "\n  " + strings.Join(clauses, ",\n  ")},
		Rollback:   rollbacks,
		Warnings:   warnings,
	}, nil
}

func (e *schemaEditor) DropObjectScript(_ context.Context, ref domain.ObjectRef) (domain.Script, error) {
	qualified, err := quoteQualified(ref.Database, ref.Name)
	if err != nil {
		return domain.Script{}, err
	}
	return domain.Script{
		Statements: []string{"DROP TABLE IF EXISTS " + qualified},
		Warnings:   []string{"删除表不可恢复；若要删除视图请改用 DROP VIEW。"},
	}, nil
}

func (e *schemaEditor) ShowCreate(ctx context.Context, ref domain.ObjectRef) (string, error) {
	qualified, err := quoteQualified(ref.Database, ref.Name)
	if err != nil {
		return "", err
	}
	rows, err := e.db.QueryContext(ctx, "SHOW CREATE TABLE "+qualified)
	if err != nil {
		return "", mapQueryError(err)
	}
	data, err := scanAllStrings(rows)
	if err != nil {
		return "", mapQueryError(err)
	}
	if len(data) == 0 || len(data[0]) < 2 {
		return "", domain.NewError(domain.CodeNotFound, "mysql.script", "对象 %s 不存在", ref.String())
	}
	return data[0][1], nil
}

// Apply 逐条执行脚本。MySQL DDL 会隐式提交，因此不包裹事务，失败时不自动回滚已执行语句。
func (e *schemaEditor) Apply(ctx context.Context, script domain.Script) (domain.ApplyResult, error) {
	result := domain.ApplyResult{Outcomes: make([]domain.ExecOutcome, 0, len(script.Statements))}
	for _, stmt := range script.Statements {
		execCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
		r, err := e.db.ExecContext(execCtx, stmt)
		cancel()
		if err != nil {
			return result, mapQueryError(err)
		}
		affected, _ := r.RowsAffected()
		result.Outcomes = append(result.Outcomes, domain.ExecOutcome{
			Statement: stmt, RowsAffected: affected, Message: "执行成功",
		})
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
		return "", domain.NewError(domain.CodeValidation, "mysql.script", "字段 %s 缺少类型", c.Name)
	}
	if err := validateFragment(typ, "字段类型"); err != nil {
		return "", err
	}
	def := name + " " + typ
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
			def += " DEFAULT " + value
		} else {
			def += " DEFAULT '" + escapeLiteral(value) + "'"
		}
	}
	if c.AutoIncrement {
		def += " AUTO_INCREMENT"
	}
	if c.Comment != "" {
		def += " COMMENT '" + escapeLiteral(c.Comment) + "'"
	}
	return def, nil
}

func indexDefinition(idx domain.IndexSpec) (string, error) {
	name, err := quoteIdent(idx.Name)
	if err != nil {
		return "", err
	}
	if len(idx.Columns) == 0 {
		return "", domain.NewError(domain.CodeValidation, "mysql.script", "索引 %s 至少需要一列", idx.Name)
	}
	cols := make([]string, 0, len(idx.Columns))
	for _, c := range idx.Columns {
		q, err := quoteIdent(c)
		if err != nil {
			return "", err
		}
		cols = append(cols, q)
	}
	kind := "INDEX"
	if idx.Unique {
		kind = "UNIQUE INDEX"
	}
	return kind + " " + name + " (" + strings.Join(cols, ", ") + ")", nil
}

func tableOptionParts(ch domain.SchemaChange) ([]string, error) {
	if err := safeToken(ch.Engine, "存储引擎"); err != nil {
		return nil, err
	}
	if err := safeToken(ch.Charset, "字符集"); err != nil {
		return nil, err
	}
	if err := safeToken(ch.Collation, "排序规则"); err != nil {
		return nil, err
	}
	parts := make([]string, 0, 4)
	if ch.Engine != "" {
		parts = append(parts, "ENGINE = "+ch.Engine)
	}
	if ch.Charset != "" {
		parts = append(parts, "DEFAULT CHARACTER SET = "+ch.Charset)
	}
	if ch.Collation != "" {
		parts = append(parts, "DEFAULT COLLATE = "+ch.Collation)
	}
	if ch.Comment != "" {
		parts = append(parts, "COMMENT = '"+escapeLiteral(ch.Comment)+"'")
	}
	return parts, nil
}
