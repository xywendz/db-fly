package bridge

import (
	"github.com/db-fly/db-fly/internal/app"
	"github.com/db-fly/db-fly/internal/domain"
)

// ExecuteQuery 执行一次查询并返回完整结果（流式结果在后端聚合后整体返回前端，
// 默认上限 1000 行；如需进度反馈，由后端 emit 事件，前端监听）。
func (b *Bridge) ExecuteQuery(req QueryRequest) (domain.QueryOutcome, error) {
	b.emit("query:started", map[string]any{"conn": req.Conn})
	sink := &app.CollectSink{}
	_, err := b.app.Execute(b.ctx(), req.Conn, req.Query, sink)
	b.emit("query:finished", map[string]any{"conn": req.Conn, "ok": err == nil})
	return sink.Outcome, err
}

// ExplainQuery 执行执行计划查询。
func (b *Bridge) ExplainQuery(req QueryRequest) (domain.QueryOutcome, error) {
	sink := &app.CollectSink{}
	_, err := b.app.Explain(b.ctx(), req.Conn, req.Query, sink)
	return sink.Outcome, err
}

// ExecStatement 执行单条无结果集语句（DDL / DML），用于结果集编辑回写等。
func (b *Bridge) ExecStatement(req ExecRequest) (domain.ExecResult, error) {
	return b.app.ExecStatement(b.ctx(), req.Conn, req.Statement, req.Database)
}

// ExecuteScript 按语句拆分顺序执行一段脚本，任一失败即停止，返回每个语句的结果。
func (b *Bridge) ExecuteScript(req ScriptRequest) ([]domain.QueryOutcome, error) {
	sink := &app.CollectSink{}
	return b.app.ExecuteStatements(b.ctx(), req.Conn, req.Text, req.Query, sink)
}
