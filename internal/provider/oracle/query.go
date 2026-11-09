package oracle

import (
	"context"
	"database/sql"
	"strings"

	"github.com/db-fly/db-fly/internal/domain"
	"github.com/db-fly/db-fly/internal/provider/sqlkit"
)

// queryExecutor 复用通用执行器，但覆盖 Explain：Oracle 的 EXPLAIN PLAN 本身不返回结果集。
type queryExecutor struct {
	sqlkit.QueryRunner
	db *sql.DB
}

func (e *queryExecutor) Explain(ctx context.Context, q domain.Query) (domain.ResultSet, error) {
	statement := strings.TrimSpace(q.Text)
	if statement == "" {
		return nil, domain.NewError(domain.CodeValidation, "oracle.explain", "执行计划需要指定 SQL 语句")
	}
	if strings.HasPrefix(strings.ToUpper(statement), "EXPLAIN PLAN FOR") {
		statement = strings.TrimSpace(statement[len("EXPLAIN PLAN FOR"):])
	}
	if _, err := e.db.ExecContext(ctx, "EXPLAIN PLAN FOR "+statement); err != nil {
		return nil, sqlkit.MapError("oracle.explain", err)
	}
	rows, err := e.db.QueryContext(ctx, "SELECT * FROM TABLE(DBMS_XPLAN.DISPLAY)")
	if err != nil {
		return nil, sqlkit.MapError("oracle.explain", err)
	}
	rs, err := sqlkit.NewRowsResultSet(rows, nil, "oracle.explain")
	if err != nil {
		_ = rows.Close()
		return nil, err
	}
	return rs, nil
}
