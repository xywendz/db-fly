package sqlkit

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/db-fly/db-fly/internal/domain"
)

// QueryRunner 是基于 database/sql 的通用查询执行器，方言差异由 SelectDialect 与 DBFor 注入。
type QueryRunner struct {
	DB      *sql.DB
	DBFor   func(ctx context.Context, database string) (*sql.DB, error)
	Dialect SelectDialect
	Op      string
	MapErr  func(error) error
	// ExplainPrefix 把语句包装为执行计划查询（如 "EXPLAIN " / "EXPLAIN QUERY PLAN "）。
	ExplainPrefix string
}

func (r QueryRunner) mapErr(err error) error {
	if r.MapErr != nil {
		return r.MapErr(err)
	}
	return MapError(r.Op, err)
}

func (r QueryRunner) dbFor(ctx context.Context, database string) (*sql.DB, error) {
	if r.DBFor != nil {
		db, err := r.DBFor(ctx, database)
		if err != nil {
			return nil, err
		}
		return db, nil
	}
	return r.DB, nil
}

// Execute 执行查询（或表数据浏览预览）并返回流式结果集。
func (r QueryRunner) Execute(ctx context.Context, q domain.Query) (domain.ResultSet, error) {
	statement, err := BuildPreview(r.Dialect, q)
	if err != nil {
		return nil, err
	}
	db, err := r.dbFor(ctx, q.Database)
	if err != nil {
		return nil, err
	}
	if db == nil {
		return nil, domain.NewError(domain.CodeConnection, r.Op, "没有可用的数据库连接")
	}
	rows, err := db.QueryContext(ctx, statement)
	if err != nil {
		return nil, r.mapErr(err)
	}
	rs, err := NewRowsResultSet(rows, nil, r.Op)
	if err != nil {
		_ = rows.Close()
		return nil, err
	}
	return rs, nil
}

// Exec 执行无结果集语句。
func (r QueryRunner) Exec(ctx context.Context, statement, database string) (domain.ExecResult, error) {
	stmt := strings.TrimSpace(statement)
	if stmt == "" {
		return domain.ExecResult{}, domain.NewError(domain.CodeValidation, r.Op, "语句不能为空")
	}
	db, err := r.dbFor(ctx, database)
	if err != nil {
		return domain.ExecResult{}, err
	}
	if db == nil {
		return domain.ExecResult{}, domain.NewError(domain.CodeConnection, r.Op, "没有可用的数据库连接")
	}
	res, err := db.ExecContext(ctx, stmt)
	if err != nil {
		return domain.ExecResult{}, r.mapErr(err)
	}
	affected, _ := res.RowsAffected()
	return domain.ExecResult{
		Statement:    stmt,
		RowsAffected: affected,
		Message:      fmt.Sprintf("执行成功，影响 %d 行", affected),
	}, nil
}

// Explain 返回执行计划结果集。
func (r QueryRunner) Explain(ctx context.Context, q domain.Query) (domain.ResultSet, error) {
	if strings.TrimSpace(q.Text) == "" {
		return nil, domain.NewError(domain.CodeValidation, r.Op, "执行计划需要指定 SQL 语句")
	}
	prefix := r.ExplainPrefix
	if prefix == "" {
		prefix = "EXPLAIN "
	}
	upper := strings.ToUpper(strings.TrimSpace(q.Text))
	if !strings.HasPrefix(upper, strings.ToUpper(strings.TrimSpace(prefix))) {
		q.Text = prefix + strings.TrimSpace(q.Text)
	}
	return r.Execute(ctx, q)
}
