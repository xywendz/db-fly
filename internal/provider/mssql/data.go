package mssql

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/dbclient/dbclient/internal/domain"
)

// dataEditor 把结构化编辑请求翻译成带 @pN 命名参数的 T-SQL。
type dataEditor struct {
	db *sql.DB
}

type paramStatement struct {
	sql  string
	args []any
	text string
}

type binder struct{ n int }

func (b *binder) bind(v *string) (string, any, string) {
	b.n++
	name := "p" + strconv.Itoa(b.n)
	placeholder := "@" + name
	if v == nil {
		return placeholder, sql.Named(name, nil), "NULL"
	}
	return placeholder, sql.Named(name, *v), "N'" + escapeLiteral(*v) + "'"
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

func sortedKeys(m map[string]*string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func (e *dataEditor) Preview(ctx context.Context, req domain.EditRequest) ([]string, error) {
	statements, err := e.build(req)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(statements)+1)
	if use, err := useStatement(req.Ref.Database); err == nil {
		for _, s := range use {
			out = append(out, s+";")
		}
	}
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

	conn, err := e.db.Conn(ctx)
	if err != nil {
		return result, mapQueryError(err)
	}
	defer conn.Close()
	if use, err := useStatement(req.Ref.Database); err != nil {
		return result, err
	} else {
		for _, s := range use {
			if _, err := conn.ExecContext(ctx, s); err != nil {
				return result, mapQueryError(err)
			}
		}
	}

	var executor interface {
		ExecContext(context.Context, string, ...any) (sql.Result, error)
	} = conn
	if req.Transaction {
		tx, err := conn.BeginTx(ctx, nil)
		if err != nil {
			return result, mapQueryError(err)
		}
		defer tx.Rollback()
		executor = tx
	}
	for _, s := range statements {
		if _, err := executor.ExecContext(ctx, s.sql, s.args...); err != nil {
			return result, mapQueryError(err)
		}
		result.Statements = append(result.Statements, s.text)
		result.Applied++
	}
	if req.Transaction {
		if tx, ok := executor.(*sql.Tx); ok {
			if err := tx.Commit(); err != nil {
				return result, mapQueryError(err)
			}
		}
	}
	return result, nil
}

func (e *dataEditor) build(req domain.EditRequest) ([]paramStatement, error) {
	qualified, err := quoteQualified("", defaultSchema(req.Ref.Schema), req.Ref.Name)
	if err != nil {
		return nil, err
	}
	if len(req.Edits) == 0 {
		return nil, domain.NewError(domain.CodeValidation, "sqlserver.edit", "没有待提交的变更")
	}
	out := make([]paramStatement, 0, len(req.Edits))
	for i, edit := range req.Edits {
		b := &binder{}
		var stmt paramStatement
		switch edit.Kind {
		case domain.EditUpdate:
			stmt, err = buildUpdate(b, qualified, edit)
		case domain.EditDelete:
			stmt, err = buildDelete(b, qualified, edit)
		case domain.EditInsert:
			stmt, err = buildInsert(b, qualified, edit)
		default:
			err = domain.NewError(domain.CodeUnsupported, "sqlserver.edit", "不支持的编辑类型 %q", edit.Kind)
		}
		if err != nil {
			return nil, fmt.Errorf("第 %d 条变更无效: %w", i+1, err)
		}
		out = append(out, stmt)
	}
	return out, nil
}

func whereClause(b *binder, key map[string]*string) (string, string, []any, error) {
	if len(key) == 0 {
		return "", "", nil, domain.NewError(domain.CodeValidation, "sqlserver.edit",
			"更新或删除必须提供主键/唯一键值")
	}
	names := sortedKeys(key)
	cols, err := quotedColumns(names)
	if err != nil {
		return "", "", nil, err
	}
	parts, textParts := make([]string, 0, len(names)), make([]string, 0, len(names))
	var args []any
	for i, name := range names {
		v := key[name]
		if v == nil {
			parts = append(parts, cols[i]+" IS NULL")
			textParts = append(textParts, cols[i]+" IS NULL")
			continue
		}
		placeholder, arg, text := b.bind(v)
		parts = append(parts, cols[i]+" = "+placeholder)
		textParts = append(textParts, cols[i]+" = "+text)
		args = append(args, arg)
	}
	return strings.Join(parts, " AND "), strings.Join(textParts, " AND "), args, nil
}

func buildUpdate(b *binder, qualified string, edit domain.RowEdit) (paramStatement, error) {
	if len(edit.Values) == 0 {
		return paramStatement{}, domain.NewError(domain.CodeValidation, "sqlserver.edit", "更新内容为空")
	}
	names := sortedKeys(edit.Values)
	cols, err := quotedColumns(names)
	if err != nil {
		return paramStatement{}, err
	}
	sets, texts := make([]string, 0, len(names)), make([]string, 0, len(names))
	var args []any
	for i, name := range names {
		placeholder, arg, text := b.bind(edit.Values[name])
		sets = append(sets, cols[i]+" = "+placeholder)
		texts = append(texts, cols[i]+" = "+text)
		args = append(args, arg)
	}
	where, whereText, whereArgs, err := whereClause(b, edit.Key)
	if err != nil {
		return paramStatement{}, err
	}
	args = append(args, whereArgs...)
	return paramStatement{
		sql:  "UPDATE " + qualified + " SET " + strings.Join(sets, ", ") + " WHERE " + where,
		args: args,
		text: "UPDATE " + qualified + " SET " + strings.Join(texts, ", ") + " WHERE " + whereText + ";",
	}, nil
}

func buildDelete(b *binder, qualified string, edit domain.RowEdit) (paramStatement, error) {
	where, whereText, args, err := whereClause(b, edit.Key)
	if err != nil {
		return paramStatement{}, err
	}
	return paramStatement{
		sql:  "DELETE FROM " + qualified + " WHERE " + where,
		args: args,
		text: "DELETE FROM " + qualified + " WHERE " + whereText + ";",
	}, nil
}

func buildInsert(b *binder, qualified string, edit domain.RowEdit) (paramStatement, error) {
	if len(edit.Values) == 0 {
		return paramStatement{}, domain.NewError(domain.CodeValidation, "sqlserver.edit", "插入内容为空")
	}
	names := sortedKeys(edit.Values)
	cols, err := quotedColumns(names)
	if err != nil {
		return paramStatement{}, err
	}
	placeholders, values := make([]string, len(names)), make([]string, len(names))
	var args []any
	for i, name := range names {
		placeholder, arg, text := b.bind(edit.Values[name])
		placeholders[i] = placeholder
		values[i] = text
		args = append(args, arg)
	}
	return paramStatement{
		sql:  "INSERT INTO " + qualified + " (" + strings.Join(cols, ", ") + ") VALUES (" + strings.Join(placeholders, ", ") + ")",
		args: args,
		text: "INSERT INTO " + qualified + " (" + strings.Join(cols, ", ") + ") VALUES (" + strings.Join(values, ", ") + ");",
	}, nil
}
