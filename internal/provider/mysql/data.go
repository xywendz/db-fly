package mysql

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"

	"github.com/db-fly/db-fly/internal/domain"
)

// dataEditor 把结构化编辑请求翻译成参数化 SQL，绝不拼接用户值。
type dataEditor struct {
	db *sql.DB
}

type paramStatement struct {
	sql  string
	args []any
	text string // 带字面量的可读预览
}

func (e *dataEditor) Preview(ctx context.Context, req domain.EditRequest) ([]string, error) {
	statements, err := e.build(req)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(statements))
	for _, s := range statements {
		out = append(out, s.text)
	}
	return out, nil
}

func (e *dataEditor) Apply(ctx context.Context, req domain.EditRequest) (domain.EditResult, error) {
	statements, err := e.build(req)
	if err != nil {
		return domain.EditResult{}, err
	}
	result := domain.EditResult{Statements: make([]string, 0, len(statements))}

	var executor interface {
		ExecContext(context.Context, string, ...any) (sql.Result, error)
	}
	if req.Transaction {
		tx, err := e.db.BeginTx(ctx, nil)
		if err != nil {
			return result, mapQueryError(err)
		}
		defer tx.Rollback()
		executor = tx
		for _, s := range statements {
			if _, err := executor.ExecContext(ctx, s.sql, s.args...); err != nil {
				return result, mapQueryError(err)
			}
			result.Statements = append(result.Statements, s.text)
			result.Applied++
		}
		if err := tx.Commit(); err != nil {
			return result, mapQueryError(err)
		}
		return result, nil
	}

	executor = e.db
	for _, s := range statements {
		if _, err := executor.ExecContext(ctx, s.sql, s.args...); err != nil {
			return result, mapQueryError(err)
		}
		result.Statements = append(result.Statements, s.text)
		result.Applied++
	}
	return result, nil
}

func (e *dataEditor) build(req domain.EditRequest) ([]paramStatement, error) {
	qualified, err := quoteQualified(req.Ref.Database, req.Ref.Name)
	if err != nil {
		return nil, err
	}
	if len(req.Edits) == 0 {
		return nil, domain.NewError(domain.CodeValidation, "mysql.edit", "没有待提交的变更")
	}
	out := make([]paramStatement, 0, len(req.Edits))
	for i, edit := range req.Edits {
		var stmt paramStatement
		switch edit.Kind {
		case domain.EditUpdate:
			stmt, err = buildUpdate(qualified, edit)
		case domain.EditDelete:
			stmt, err = buildDelete(qualified, edit)
		case domain.EditInsert:
			stmt, err = buildInsert(qualified, edit)
		default:
			err = domain.NewError(domain.CodeUnsupported, "mysql.edit", "不支持的编辑类型 %q", edit.Kind)
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

func quotedColumns(names []string) ([]string, error) {
	out := make([]string, 0, len(names))
	for _, n := range names {
		q, err := quoteIdent(n)
		if err != nil {
			return nil, err
		}
		out = append(out, q)
	}
	return out, nil
}

func literal(value *string) string {
	if value == nil {
		return "NULL"
	}
	return "'" + escapeLiteral(*value) + "'"
}

func whereClause(key map[string]*string) (string, []any, string, error) {
	if len(key) == 0 {
		return "", nil, "", domain.NewError(domain.CodeValidation, "mysql.edit",
			"更新或删除必须提供主键/唯一键值")
	}
	names := sortedKeys(key)
	cols, err := quotedColumns(names)
	if err != nil {
		return "", nil, "", err
	}
	parts := make([]string, 0, len(names))
	textParts := make([]string, 0, len(names))
	var args []any
	for i, name := range names {
		v := key[name]
		if v == nil {
			parts = append(parts, cols[i]+" IS NULL")
			textParts = append(textParts, cols[i]+" IS NULL")
			continue
		}
		parts = append(parts, cols[i]+" = ?")
		textParts = append(textParts, cols[i]+" = "+literal(v))
		args = append(args, *v)
	}
	return strings.Join(parts, " AND "), args, strings.Join(textParts, " AND "), nil
}

func buildUpdate(qualified string, edit domain.RowEdit) (paramStatement, error) {
	if len(edit.Values) == 0 {
		return paramStatement{}, domain.NewError(domain.CodeValidation, "mysql.edit", "更新内容为空")
	}
	where, whereArgs, whereText, err := whereClause(edit.Key)
	if err != nil {
		return paramStatement{}, err
	}
	names := sortedKeys(edit.Values)
	cols, err := quotedColumns(names)
	if err != nil {
		return paramStatement{}, err
	}
	sets := make([]string, 0, len(names))
	setTexts := make([]string, 0, len(names))
	var args []any
	for i, name := range names {
		v := edit.Values[name]
		sets = append(sets, cols[i]+" = ?")
		setTexts = append(setTexts, cols[i]+" = "+literal(v))
		if v == nil {
			args = append(args, nil)
		} else {
			args = append(args, *v)
		}
	}
	args = append(args, whereArgs...)
	return paramStatement{
		sql:  "UPDATE " + qualified + " SET " + strings.Join(sets, ", ") + " WHERE " + where,
		args: args,
		text: "UPDATE " + qualified + " SET " + strings.Join(setTexts, ", ") + " WHERE " + whereText + ";",
	}, nil
}

func buildDelete(qualified string, edit domain.RowEdit) (paramStatement, error) {
	where, args, whereText, err := whereClause(edit.Key)
	if err != nil {
		return paramStatement{}, err
	}
	return paramStatement{
		sql:  "DELETE FROM " + qualified + " WHERE " + where,
		args: args,
		text: "DELETE FROM " + qualified + " WHERE " + whereText + ";",
	}, nil
}

func buildInsert(qualified string, edit domain.RowEdit) (paramStatement, error) {
	if len(edit.Values) == 0 {
		return paramStatement{}, domain.NewError(domain.CodeValidation, "mysql.edit", "插入内容为空")
	}
	names := sortedKeys(edit.Values)
	cols, err := quotedColumns(names)
	if err != nil {
		return paramStatement{}, err
	}
	placeholders := make([]string, len(names))
	values := make([]string, len(names))
	var args []any
	for i, name := range names {
		placeholders[i] = "?"
		v := edit.Values[name]
		values[i] = literal(v)
		if v == nil {
			args = append(args, nil)
		} else {
			args = append(args, *v)
		}
	}
	return paramStatement{
		sql:  "INSERT INTO " + qualified + " (" + strings.Join(cols, ", ") + ") VALUES (" + strings.Join(placeholders, ", ") + ")",
		args: args,
		text: "INSERT INTO " + qualified + " (" + strings.Join(cols, ", ") + ") VALUES (" + strings.Join(values, ", ") + ");",
	}, nil
}
