package app

import (
	"testing"

	"github.com/xywendz/db-fly/internal/domain"
)

func TestMultiSQLStatements(t *testing.T) {
	got := multiSQLStatements(domain.Query{
		Language: domain.LangSQL,
		Text:     "SELECT 1; SELECT ';';",
	})
	if len(got) != 2 || got[0] != "SELECT 1" || got[1] != "SELECT ';'" {
		t.Fatalf("多语句拆分错误: %#v", got)
	}
	if got := multiSQLStatements(domain.Query{Language: domain.LangSQL, Text: "SELECT 1;"}); got != nil {
		t.Errorf("单语句不应触发拆分: %#v", got)
	}
	if got := multiSQLStatements(domain.Query{Language: domain.LangMongoFilter, Text: `{"a":";"};{"b":1}`}); got != nil {
		t.Errorf("非 SQL 语言不应按分号拆分: %#v", got)
	}
}

func TestMergeOutcomes(t *testing.T) {
	out := mergeOutcomes([]domain.QueryOutcome{
		{
			Pages: []domain.ResultPage{makePage(1, 2)},
			Summary: domain.QuerySummary{
				Rows: 2, Affected: 1, DurationMs: 10, Truncated: true,
				Messages: []domain.ServerMessage{{Level: "info", Text: "one"}},
			},
		},
		{
			Pages: []domain.ResultPage{makePage(2, 1), makePage(1, 3)},
			Summary: domain.QuerySummary{
				Rows: 4, Affected: 2, DurationMs: 20,
				Messages: []domain.ServerMessage{{Level: "warn", Text: "two"}},
			},
		},
	})
	if len(out.Pages) != 3 || out.Summary.Pages != 3 {
		t.Fatalf("结果集合并错误: %d 页 / summary=%+v", len(out.Pages), out.Summary)
	}
	if out.Summary.Rows != 6 || out.Summary.Affected != 3 || out.Summary.DurationMs != 30 {
		t.Errorf("汇总信息合并错误: %+v", out.Summary)
	}
	if !out.Summary.Truncated {
		t.Error("截断标记应保留")
	}
	if len(out.Summary.Messages) != 2 || out.Summary.Messages[1].Text != "two" {
		t.Errorf("服务端消息合并错误: %#v", out.Summary.Messages)
	}
}
