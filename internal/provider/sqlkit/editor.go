package sqlkit

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"

	"github.com/db-fly/db-fly/internal/domain"
)

// BindStyle 描述方言的参数占位符与参数绑定方式。
type BindStyle struct {
	Placeholder func(index int) string
	Arg         func(index int, value any) any
}

// QuestionMarkBind 用于 MySQL / SQLite 的 ? 占位符。
func QuestionMarkBind() BindStyle {
	return BindStyle{
		Placeholder: func(int) string { return "?" },
		Arg:         func(_ int, v any) any { return v },
	}
}

// DollarBind 用于 PostgreSQL 的 $1、$2 占位符。
func DollarBind() BindStyle {
	return BindStyle{
		Placeholder: func(i int) string { return fmt.Sprintf("$%d", i) },
		Arg:         func(_ int, v any) any { return v },
	}
}

// ColonNamedBind 用于 Oracle 的 :p1、:p2 命名占位符。
func ColonNamedBind() BindStyle {
	name := func(i int) string { return fmt.Sprintf("p%d", i) }
	return BindStyle{
		Placeholder: func(i int) string { return ":" + name(i) },
		Arg: func(i int, v any) any {
			return sql.Named(name(i), v)
		},
	}
}

type paramStatement struct {
	sql  string
	args []any
	text string
}

type binder struct {
	style BindStyle
	count int
	args  []any
}

func (b *binder) next(value any) string {
	b.count++
	name := b.style.Placeholder(b.count)
	if b.style.Arg != nil {
		b.args = append(b.args, b.style.Arg(b.count, value))
	} else {
		b.args = append(b.args, value)
	}
	return name
}

// Editor 把结构化编辑请求翻译成参数化 SQL，绝不拼接用户值。
type Editor struct {
	DB        *sql.DB
	DBFor     func(ctx context.Context, database string) (*sql.DB, error)
	Quoter    Quoter
	Qualified func(ref domain.ObjectRef) (string, error)
	Bind      BindStyle
	Op        string
	MapErr    func(error) error
}

func (e Editor) mapErr(err error) error {
	if e.MapErr != nil {
		return e.MapErr(err)
	}
	return MapError(e.Op, err)
}

// Preview 返回将要执行的语句文本（带字面量，便于人工复核），不修改数据。
func (e Editor) Preview(_ context.Context, req domain.EditRequest) ([]string, error) {
	statements, err := e.build(req)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(statements))
	for _, stmt := range statements {
		out = append(out, stmt.text)
	}
	return out, nil
}

// Apply 执行编辑；Transaction 为 true 时使用事务。
func (e Editor) Apply(ctx context.Context, req domain.EditRequest) (domain.EditResult, error) {
	statements, err := e.build(req)
	if err != nil {
		return domain.EditResult{}, err
	}
	result := domain.EditResult{Statements: make([]string, 0, len(statements))}
	db := e.DB
	if e.DBFor != nil {
		db, err = e.DBFor(ctx, req.Ref.Database)
		if err != nil {
			return result, err
		}
	}
	if db == nil {
		return result, domain.NewError(domain.CodeConnection, e.Op, "没有可用的数据库连接")
	}

	var executor interface {
		ExecContext(context.Context, string, ...any) (sql.Result, error)
	}
	if req.Transaction {
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return result, e.mapErr(err)
		}
		defer tx.Rollback()
		executor = tx
		for _, stmt := range statements {
			if _, err := executor.ExecContext(ctx, stmt.sql, stmt.args...); err != nil {
				return result, e.mapErr(err)
			}
			result.Statements = append(result.Statements, stmt.text)
			result.Applied++
		}
		if err := tx.Commit(); err != nil {
			return result, e.mapErr(err)
		}
		return result, nil
	}

	executor = db
	for _, stmt := range statements {
		if _, err := executor.ExecContext(ctx, stmt.sql, stmt.args...); err != nil {
			return result, e.mapErr(err)
		}
		result.Statements = append(result.Statements, stmt.text)
		result.Applied++
	}
	return result, nil
}

func (e Editor) build(req domain.EditRequest) ([]paramStatement, error) {
	if e.Qualified == nil {
		return nil, domain.NewError(domain.CodeValidation, e.Op, "未配置限定名生成器")
	}
	qualified, err := e.Qualified(req.Ref)
	if err != nil {
		return nil, err
	}
	if len(req.Edits) == 0 {
		return nil, domain.NewError(domain.CodeValidation, e.Op, "没有待提交的变更")
	}
	if e.Bind.Placeholder == nil {
		e.Bind = QuestionMarkBind()
	}
	out := make([]paramStatement, 0, len(req.Edits))
	for i, edit := range req.Edits {
		var stmt paramStatement
		switch edit.Kind {
		case domain.EditUpdate:
			stmt, err = e.buildUpdate(qualified, edit)
		case domain.EditDelete:
			stmt, err = e.buildDelete(qualified, edit)
		case domain.EditInsert:
			stmt, err = e.buildInsert(qualified, edit)
		default:
			err = domain.NewError(domain.CodeUnsupported, e.Op, "不支持的编辑类型 %q", edit.Kind)
		}
		if err != nil {
			return nil, fmt.Errorf("第 %d 条变更无效: %w", i+1, err)
		}
		out = append(out, stmt)
	}
	return out, nil
}

func sortedKeys(m map[string]*string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func (e Editor) quotedColumns(names []string) ([]string, error) {
	out := make([]string, 0, len(names))
	for _, name := range names {
		quoted, err := e.Quoter.Ident(name)
		if err != nil {
			return nil, err
		}
		out = append(out, quoted)
	}
	return out, nil
}

func literal(value *string) string {
	if value == nil {
		return "NULL"
	}
	return "'" + EscapeLiteral(*value) + "'"
}

func scalar(value *string) any {
	if value == nil {
		return nil
	}
	return *value
}

func (e Editor) whereClause(b *binder, key map[string]*string) (string, string, error) {
	if len(key) == 0 {
		return "", "", domain.NewError(domain.CodeValidation, e.Op, "更新或删除必须提供主键/唯一键值")
	}
	names := sortedKeys(key)
	cols, err := e.quotedColumns(names)
	if err != nil {
		return "", "", err
	}
	parts := make([]string, 0, len(names))
	texts := make([]string, 0, len(names))
	for i, name := range names {
		value := key[name]
		if value == nil {
			parts = append(parts, cols[i]+" IS NULL")
			texts = append(texts, cols[i]+" IS NULL")
			continue
		}
		parts = append(parts, cols[i]+" = "+b.next(scalar(value)))
		texts = append(texts, cols[i]+" = "+literal(value))
	}
	return strings.Join(parts, " AND "), strings.Join(texts, " AND "), nil
}

func (e Editor) buildUpdate(qualified string, edit domain.RowEdit) (paramStatement, error) {
	if len(edit.Values) == 0 {
		return paramStatement{}, domain.NewError(domain.CodeValidation, e.Op, "更新内容为空")
	}
	b := &binder{style: e.Bind}
	names := sortedKeys(edit.Values)
	cols, err := e.quotedColumns(names)
	if err != nil {
		return paramStatement{}, err
	}
	sets := make([]string, 0, len(names))
	texts := make([]string, 0, len(names))
	for i, name := range names {
		value := edit.Values[name]
		sets = append(sets, cols[i]+" = "+b.next(scalar(value)))
		texts = append(texts, cols[i]+" = "+literal(value))
	}
	where, whereText, err := e.whereClause(b, edit.Key)
	if err != nil {
		return paramStatement{}, err
	}
	return paramStatement{
		sql:  "UPDATE " + qualified + " SET " + strings.Join(sets, ", ") + " WHERE " + where,
		args: b.args,
		text: "UPDATE " + qualified + " SET " + strings.Join(texts, ", ") + " WHERE " + whereText + ";",
	}, nil
}

func (e Editor) buildDelete(qualified string, edit domain.RowEdit) (paramStatement, error) {
	b := &binder{style: e.Bind}
	where, whereText, err := e.whereClause(b, edit.Key)
	if err != nil {
		return paramStatement{}, err
	}
	return paramStatement{
		sql:  "DELETE FROM " + qualified + " WHERE " + where,
		args: b.args,
		text: "DELETE FROM " + qualified + " WHERE " + whereText + ";",
	}, nil
}

func (e Editor) buildInsert(qualified string, edit domain.RowEdit) (paramStatement, error) {
	if len(edit.Values) == 0 {
		return paramStatement{}, domain.NewError(domain.CodeValidation, e.Op, "插入内容为空")
	}
	b := &binder{style: e.Bind}
	names := sortedKeys(edit.Values)
	cols, err := e.quotedColumns(names)
	if err != nil {
		return paramStatement{}, err
	}
	placeholders := make([]string, len(names))
	values := make([]string, len(names))
	for i, name := range names {
		value := edit.Values[name]
		placeholders[i] = b.next(scalar(value))
		values[i] = literal(value)
	}
	return paramStatement{
		sql:  "INSERT INTO " + qualified + " (" + strings.Join(cols, ", ") + ") VALUES (" + strings.Join(placeholders, ", ") + ")",
		args: b.args,
		text: "INSERT INTO " + qualified + " (" + strings.Join(cols, ", ") + ") VALUES (" + strings.Join(values, ", ") + ");",
	}, nil
}
