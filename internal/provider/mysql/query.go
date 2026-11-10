package mysql

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/db-fly/db-fly/internal/domain"
	"github.com/db-fly/db-fly/internal/provider/sqlkit"
)

type queryExecutor struct {
	db  *sql.DB
	cfg domain.ConnectionConfig
}

// acquire 在需要切换数据库时独占一条连接，先执行 USE 再执行目标语句。
// database/sql 的连接池无法保证「USE」与后续语句落在同一连接上，
// 因此这里显式使用 *sql.Conn，并把连接生命周期绑定到结果集。
func (e *queryExecutor) acquire(ctx context.Context, database string) (*sql.Conn, error) {
	if database == "" || database == e.cfg.Database {
		return nil, nil
	}
	conn, err := e.db.Conn(ctx)
	if err != nil {
		return nil, mapQueryError(err)
	}
	quoted, err := quoteIdent(database)
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	if _, err := conn.ExecContext(ctx, "USE "+quoted); err != nil {
		_ = conn.Close()
		return nil, mapQueryError(err)
	}
	return conn, nil
}

func (e *queryExecutor) Execute(ctx context.Context, q domain.Query) (domain.ResultSet, error) {
	stmt, err := prepareStatement(q)
	if err != nil {
		return nil, err
	}
	conn, err := e.acquire(ctx, q.Database)
	if err != nil {
		return nil, err
	}

	var rows *sql.Rows
	if conn != nil {
		rows, err = conn.QueryContext(ctx, stmt)
	} else {
		rows, err = e.db.QueryContext(ctx, stmt)
	}
	if err != nil {
		if conn != nil {
			_ = conn.Close()
		}
		return nil, mapQueryError(err)
	}
	rs, err := newResultSet(rows, conn)
	if err != nil {
		_ = rows.Close()
		if conn != nil {
			_ = conn.Close()
		}
		return nil, err
	}
	return rs, nil
}

func (e *queryExecutor) Exec(ctx context.Context, statement, database string) (domain.ExecResult, error) {
	stmt := strings.TrimSpace(statement)
	if stmt == "" {
		return domain.ExecResult{}, domain.NewError(domain.CodeValidation, "mysql.exec", "语句不能为空")
	}
	conn, err := e.acquire(ctx, database)
	if err != nil {
		return domain.ExecResult{}, err
	}
	if conn != nil {
		defer conn.Close()
	}

	var r sql.Result
	if conn != nil {
		r, err = conn.ExecContext(ctx, stmt)
	} else {
		r, err = e.db.ExecContext(ctx, stmt)
	}
	if err != nil {
		return domain.ExecResult{}, mapQueryError(err)
	}
	affected, _ := r.RowsAffected()
	lastID, _ := r.LastInsertId()
	return domain.ExecResult{
		Statement:    stmt,
		RowsAffected: affected,
		LastInsertID: lastID,
		Message:      fmt.Sprintf("执行成功，影响 %d 行", affected),
	}, nil
}

func (e *queryExecutor) Explain(ctx context.Context, q domain.Query) (domain.ResultSet, error) {
	if _, err := prepareStatement(q); err != nil {
		return nil, err
	}
	text := strings.TrimSpace(q.Text)
	if !strings.HasPrefix(strings.ToUpper(text), "EXPLAIN") {
		q.Text = "EXPLAIN " + text
	}
	return e.Execute(ctx, q)
}

// prepareStatement 生成表数据浏览语句（支持 filter / sort），或原样返回自定义 SQL。
func prepareStatement(q domain.Query) (string, error) {
	return sqlkit.BuildPreview(sqlkit.SelectDialect{
		Quoter: sqlkit.MySQLQuoter,
		Qualified: func(ref domain.ObjectRef) (string, error) {
			return quoteQualified(ref.Database, ref.Name)
		},
		LimitSuffix: func(limit, offset int) string {
			if offset > 0 {
				return fmt.Sprintf(" LIMIT %d OFFSET %d", limit, offset)
			}
			return fmt.Sprintf(" LIMIT %d", limit)
		},
	}, q)
}
