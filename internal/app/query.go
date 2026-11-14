package app

import (
	"context"
	"strings"
	"time"

	"github.com/db-fly/db-fly/internal/domain"
	"github.com/db-fly/db-fly/internal/platform/audit"
	"github.com/db-fly/db-fly/internal/platform/config"
	"github.com/db-fly/db-fly/internal/provider"
)

// ResultSink 是流式结果的呈现端口。GUI 实现为分批刷新表格，CLI 实现为累积输出。
type ResultSink interface {
	OnResultStart(index int, cols []domain.Column)
	OnRows(index int, rows []domain.Row)
	OnFinish(outcome domain.QueryOutcome)
}

// CollectSink 把全部结果累积到内存，适用于 CLI、导出与单元测试。
type CollectSink struct {
	Outcome domain.QueryOutcome
}

func (s *CollectSink) ensure(index int) {
	for len(s.Outcome.Pages) <= index {
		s.Outcome.Pages = append(s.Outcome.Pages, domain.ResultPage{})
	}
}

func (s *CollectSink) OnResultStart(index int, cols []domain.Column) {
	s.ensure(index)
	s.Outcome.Pages[index].Columns = cols
}

func (s *CollectSink) OnRows(index int, rows []domain.Row) {
	s.ensure(index)
	s.Outcome.Pages[index].Rows = append(s.Outcome.Pages[index].Rows, rows...)
}

func (s *CollectSink) OnFinish(outcome domain.QueryOutcome) { s.Outcome = outcome }

func (a *App) prepareQuery(ctx context.Context, conn string, q domain.Query) (domain.Session, domain.ConnectionConfig, domain.Query, time.Duration, error) {
	c, err := a.cfgOf(conn)
	if err != nil {
		return nil, c, q, 0, err
	}
	if q.Language == "" {
		// 由模块能力声明决定默认语言，通用层不按 Kind 分支。
		q.Language = domain.LangSQL
		if caps, capErr := provider.Capabilities(c.Kind); capErr == nil {
			switch {
			case caps.Has(domain.CapKeyValues):
				q.Language = domain.LangRedis
			case caps.Has(domain.CapMongoFilter):
				q.Language = domain.LangMongoFilter
			case caps.Has(domain.CapESDSL):
				q.Language = domain.LangESDSL
			}
		}
	}
	// Filter 非空且未指定对象时是「全库扫描/键匹配」模式（如 Redis SCAN），
	// 由模块自行决定是否支持；通用层只负责放行请求。
	if q.Text == "" && q.Ref.Name == "" && strings.TrimSpace(q.Filter) == "" && len(q.Conditions) == 0 {
		return nil, c, q, 0, domain.NewError(domain.CodeValidation, "query", "查询内容不能为空")
	}
	if q.Offset < 0 {
		return nil, c, q, 0, domain.NewError(domain.CodeValidation, "query", "分页偏移不能为负数")
	}
	if q.Database == "" && q.Ref.Database != "" {
		q.Database = q.Ref.Database
	}
	if q.MaxRows == 0 {
		q.MaxRows = domain.DefaultMaxRows
		if cfg, cfgErr := a.loadConfig(); cfgErr == nil && cfg.Preferences.MaxRows > 0 {
			q.MaxRows = cfg.Preferences.MaxRows
		}
	}
	q.ReadOnly = q.ReadOnly || c.ReadOnly
	if q.ReadOnly && q.Language == domain.LangSQL && q.Text != "" {
		for _, stmt := range SplitStatements(q.Text) {
			if err := CheckReadOnly(stmt); err != nil {
				return nil, c, q, 0, err
			}
		}
	}
	sec := q.TimeoutSec
	if sec <= 0 {
		sec = c.TimeoutSec
	}
	if sec <= 0 {
		sec = 60
	}
	sess, err := a.Session(ctx, conn)
	if err != nil {
		return nil, c, q, 0, err
	}
	return sess, c, q, time.Duration(sec) * time.Second, nil
}

// Execute 执行一条查询并把结果流式推送给 sink。
func (a *App) Execute(ctx context.Context, conn string, q domain.Query, sink ResultSink) (domain.QueryOutcome, error) {
	sess, c, q, timeout, err := a.prepareQuery(ctx, conn, q)
	if err != nil {
		return domain.QueryOutcome{}, err
	}
	execCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	start := time.Now()
	rs, rsErr := sess.Query().Execute(execCtx, q)
	outcome, err := consumeResult(sink, rs, rsErr, q, start)
	a.Audit.Log(audit.Entry{
		Connection: c.Name, Kind: string(c.Kind), Action: "query",
		Statement: q.Text, DurationMs: outcome.Summary.DurationMs,
		Rows: outcome.Summary.Rows, OK: err == nil, Error: errorText(err),
	})
	a.recordHistory(config.QueryHistoryEntry{
		Connection: c.Name, Kind: string(c.Kind), Action: "query",
		Database: q.Database, Collection: q.Collection, Language: string(q.Language),
		Text: q.Text, Rows: outcome.Summary.Rows, DurationMs: outcome.Summary.DurationMs,
		OK: err == nil, Error: errorText(err),
	})
	return outcome, err
}

// Explain 执行执行计划查询。
func (a *App) Explain(ctx context.Context, conn string, q domain.Query, sink ResultSink) (domain.QueryOutcome, error) {
	sess, c, q, timeout, err := a.prepareQuery(ctx, conn, q)
	if err != nil {
		return domain.QueryOutcome{}, err
	}
	execCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	start := time.Now()
	rs, rsErr := sess.Query().Explain(execCtx, q)
	outcome, err := consumeResult(sink, rs, rsErr, q, start)
	a.Audit.Log(audit.Entry{
		Connection: c.Name, Kind: string(c.Kind), Action: "explain",
		Statement: q.Text, DurationMs: outcome.Summary.DurationMs,
		Rows: outcome.Summary.Rows, OK: err == nil, Error: errorText(err),
	})
	a.recordHistory(config.QueryHistoryEntry{
		Connection: c.Name, Kind: string(c.Kind), Action: "explain",
		Database: q.Database, Collection: q.Collection, Language: string(q.Language),
		Text: q.Text, Rows: outcome.Summary.Rows, DurationMs: outcome.Summary.DurationMs,
		OK: err == nil, Error: errorText(err),
	})
	return outcome, err
}

// ExecStatement 执行单条无结果集语句（DDL / DML），用于 CLI 与结果集编辑回写。
func (a *App) ExecStatement(ctx context.Context, conn, statement, database string) (domain.ExecResult, error) {
	return a.execStatement(ctx, conn, statement, database, true)
}

// execStatement 的 record=false 供批量 SQL 导入使用：导入整体只记一条历史。
func (a *App) execStatement(ctx context.Context, conn, statement, database string, record bool) (domain.ExecResult, error) {
	c, err := a.cfgOf(conn)
	if err != nil {
		return domain.ExecResult{}, err
	}
	if c.ReadOnly {
		if err := CheckReadOnly(statement); err != nil {
			return domain.ExecResult{}, err
		}
	}
	s, err := a.Session(ctx, conn)
	if err != nil {
		return domain.ExecResult{}, err
	}
	execCtx, cancel := context.WithTimeout(ctx, time.Duration(maxInt(c.TimeoutSec, 60))*time.Second)
	defer cancel()

	result, err := s.Query().Exec(execCtx, statement, database)
	a.Audit.Log(audit.Entry{
		Connection: c.Name, Kind: string(c.Kind), Action: "exec",
		Statement: statement, Rows: result.RowsAffected, OK: err == nil, Error: errorText(err),
	})
	if record {
		a.recordHistory(config.QueryHistoryEntry{
			Connection: c.Name, Kind: string(c.Kind), Action: "exec",
			Database: database, Language: string(domain.LangSQL),
			Text: statement, Rows: result.RowsAffected, OK: err == nil, Error: errorText(err),
		})
	}
	return result, err
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// ExecuteStatements 按语句拆分顺序执行多条 SQL，任一失败即停止。
func (a *App) ExecuteStatements(ctx context.Context, conn, text string, q domain.Query, sink ResultSink) ([]domain.QueryOutcome, error) {
	statements := SplitStatements(text)
	outcomes := make([]domain.QueryOutcome, 0, len(statements))
	for _, stmt := range statements {
		q.Text = stmt
		out, err := a.Execute(ctx, conn, q, sink)
		if err != nil {
			return outcomes, err
		}
		outcomes = append(outcomes, out)
	}
	return outcomes, nil
}

// consumeResult 把流式 ResultSet 分批推送给 sink，并施加最大行数限制。
func consumeResult(sink ResultSink, rs domain.ResultSet, rsErr error, q domain.Query, start time.Time) (domain.QueryOutcome, error) {
	if rsErr != nil {
		return domain.QueryOutcome{}, rsErr
	}
	defer rs.Close()
	if sink == nil {
		sink = &CollectSink{}
	}

	outcome := domain.QueryOutcome{}
	limit := q.MaxRows
	for pageIndex := 0; ; pageIndex++ {
		cols := rs.Columns()
		if cols == nil {
			cols = []domain.Column{}
		}
		// 空结果集序列化为 [] 而不是 null，避免表现层读取 columns/rows.length 时抛异常。
		page := domain.ResultPage{Columns: cols, Rows: []domain.Row{}}
		sink.OnResultStart(pageIndex, cols)

		batch := make([]domain.Row, 0, domain.ResultBatchSize)
		for rs.Next() {
			if limit > 0 && len(page.Rows) >= limit {
				page.Truncated = true
				break
			}
			row := rs.Row()
			page.Rows = append(page.Rows, row)
			batch = append(batch, row)
			if doc := rs.Document(); doc != "" {
				page.JSONDocuments = append(page.JSONDocuments, doc)
			}
			if len(batch) >= domain.ResultBatchSize {
				sink.OnRows(pageIndex, batch)
				batch = make([]domain.Row, 0, domain.ResultBatchSize)
			}
		}
		if len(batch) > 0 {
			sink.OnRows(pageIndex, batch)
		}
		if err := rs.Err(); err != nil {
			return outcome, err
		}
		outcome.Pages = append(outcome.Pages, page)
		outcome.Summary.Rows += int64(len(page.Rows))
		outcome.Summary.Truncated = outcome.Summary.Truncated || page.Truncated

		if !rs.NextResultSet() {
			break
		}
	}
	outcome.Summary.Pages = len(outcome.Pages)
	outcome.Summary.Affected = rs.Affected()
	outcome.Summary.Messages = rs.Messages()
	outcome.Summary.DurationMs = time.Since(start).Milliseconds()
	sink.OnFinish(outcome)
	return outcome, nil
}
