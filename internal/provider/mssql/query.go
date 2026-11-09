package mssql

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/dbclient/dbclient/internal/domain"
)

type queryExecutor struct {
	db  *sql.DB
	cfg domain.ConnectionConfig
}

// acquire 取得独占连接并切换数据库上下文（T-SQL 的 USE 只在当前连接生效）。
func (e *queryExecutor) acquire(ctx context.Context, database string) (*sql.Conn, error) {
	conn, err := e.db.Conn(ctx)
	if err != nil {
		return nil, mapQueryError(err)
	}
	if database != "" && database != e.cfg.Database {
		q, err := quoteIdent(database)
		if err != nil {
			_ = conn.Close()
			return nil, err
		}
		if _, err := conn.ExecContext(ctx, "USE "+q); err != nil {
			_ = conn.Close()
			return nil, mapQueryError(err)
		}
	}
	return conn, nil
}

func (e *queryExecutor) Execute(ctx context.Context, q domain.Query) (domain.ResultSet, error) {
	if q.Language != "" && q.Language != domain.LangSQL {
		return nil, domain.NewError(domain.CodeUnsupported, "sqlserver.query",
			"SQL Server 模块只支持 T-SQL，收到 %q", q.Language)
	}
	if strings.TrimSpace(q.Text) == "" && q.Ref.Name != "" {
		qualified, err := quoteQualified("", q.Ref.Schema, q.Ref.Name)
		if err != nil {
			return nil, err
		}
		top := q.MaxRows
		if top <= 0 {
			top = domain.DefaultMaxRows
		}
		q.Text = fmt.Sprintf("SELECT TOP %d * FROM %s", top, qualified)
	}
	batches := splitBatches(q.Text)
	if len(batches) == 0 {
		return nil, domain.NewError(domain.CodeValidation, "sqlserver.query", "查询内容不能为空")
	}
	conn, err := e.acquire(ctx, q.Database)
	if err != nil {
		return nil, err
	}

	var (
		sets     []*sql.Rows
		affected int64
		messages []domain.ServerMessage
	)
	fail := func(err error) (domain.ResultSet, error) {
		for _, rows := range sets {
			_ = rows.Close()
		}
		_ = conn.Close()
		return nil, mapQueryError(err)
	}

	for _, batch := range batches {
		if isResultBatch(batch) {
			rows, err := conn.QueryContext(ctx, batch)
			if err != nil {
				return fail(err)
			}
			sets = append(sets, rows)
			continue
		}
		r, err := conn.ExecContext(ctx, batch)
		if err != nil {
			return fail(err)
		}
		n, _ := r.RowsAffected()
		affected += n
		messages = append(messages, domain.ServerMessage{
			Level: "info", Text: fmt.Sprintf("影响 %d 行", n),
		})
	}
	rs, err := newBatchResultSet(sets, conn, affected, messages, nil)
	if err != nil {
		return fail(err)
	}
	return rs, nil
}

func (e *queryExecutor) Exec(ctx context.Context, statement, database string) (domain.ExecResult, error) {
	batches := splitBatches(statement)
	if len(batches) == 0 {
		return domain.ExecResult{}, domain.NewError(domain.CodeValidation, "sqlserver.exec", "语句不能为空")
	}
	conn, err := e.acquire(ctx, database)
	if err != nil {
		return domain.ExecResult{}, err
	}
	defer conn.Close()

	var affected int64
	for _, batch := range batches {
		r, err := conn.ExecContext(ctx, batch)
		if err != nil {
			return domain.ExecResult{}, mapQueryError(err)
		}
		n, _ := r.RowsAffected()
		affected += n
	}
	return domain.ExecResult{
		Statement:    statement,
		RowsAffected: affected,
		Message:      fmt.Sprintf("执行成功，影响 %d 行", affected),
	}, nil
}

// Explain 使用 SET SHOWPLAN_ALL 返回文本执行计划，不实际执行查询。
func (e *queryExecutor) Explain(ctx context.Context, q domain.Query) (domain.ResultSet, error) {
	if q.Language != "" && q.Language != domain.LangSQL {
		return nil, domain.NewError(domain.CodeUnsupported, "sqlserver.explain", "执行计划仅支持 T-SQL")
	}
	if strings.TrimSpace(q.Text) == "" {
		return nil, domain.NewError(domain.CodeValidation, "sqlserver.explain", "查询内容不能为空")
	}
	conn, err := e.acquire(ctx, q.Database)
	if err != nil {
		return nil, err
	}
	if _, err := conn.ExecContext(ctx, "SET SHOWPLAN_ALL ON"); err != nil {
		_ = conn.Close()
		return nil, mapQueryError(err)
	}
	rows, err := conn.QueryContext(ctx, q.Text)
	if err != nil {
		_, _ = conn.ExecContext(ctx, "SET SHOWPLAN_ALL OFF")
		_ = conn.Close()
		return nil, mapQueryError(err)
	}
	cleanup := func(context.Context) { _, _ = conn.ExecContext(context.Background(), "SET SHOWPLAN_ALL OFF") }
	rs, err := newBatchResultSet([]*sql.Rows{rows}, conn, 0, nil, cleanup)
	if err != nil {
		cleanup(ctx)
		_ = rows.Close()
		_ = conn.Close()
		return nil, err
	}
	return rs, nil
}
