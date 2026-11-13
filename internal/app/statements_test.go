package app

import (
	"testing"
	"time"

	"github.com/db-fly/db-fly/internal/domain"
)

func TestSplitStatements(t *testing.T) {
	input := "SELECT ';' AS a; -- 注释里的 ; 不应切分\nSELECT 2; /* 块注释 ; */ SELECT 3"
	got := SplitStatements(input)
	want := 3
	if len(got) != want {
		t.Fatalf("语句数 = %d, 期望 %d, 实际: %#v", len(got), want, got)
	}
	if got[0] != "SELECT ';' AS a" {
		t.Errorf("第 1 条被错误切分: %q", got[0])
	}
	if got[2] != "SELECT 3" {
		t.Errorf("第 3 条不正确: %q", got[2])
	}
}

func TestStatementAt(t *testing.T) {
	text := "SELECT 1;\nSELECT 2;\nSELECT 3"
	cases := []struct {
		pos  int
		want string
	}{
		{3, "SELECT 1"},
		{len("SELECT 1;\nSELECT 2"), "SELECT 2"},
	}
	for _, c := range cases {
		if got := StatementAt(text, c.pos); got != c.want {
			t.Errorf("pos=%d 得到 %q, 期望 %q", c.pos, got, c.want)
		}
	}
}

func TestSqlHasCreateStatement(t *testing.T) {
	cases := []struct {
		name string
		sql  string
		want bool
	}{
		{"建库", "CREATE DATABASE mydb;", true},
		{"建模式", "CREATE SCHEMA s", true},
		{"建表", "CREATE TABLE t (id INT)", true},
		{"临时表", "CREATE TEMPORARY TABLE tmp (id INT)", true},
		{"if not exists", "CREATE TABLE IF NOT EXISTS t (id INT)", true},
		{"仅插入", "INSERT INTO t VALUES (1); INSERT INTO t VALUES (2)", false},
		{"仅查询", "SELECT * FROM t", false},
		{"注释加建表", "-- 说明\nCREATE TABLE t (id INT)", true},
		{"大小写", "create table t (id int)", true},
	}
	for _, c := range cases {
		if got := sqlHasCreateStatement(SplitStatements(c.sql)); got != c.want {
			t.Errorf("%s: 期望 %v, 实际 %v (sql=%q)", c.name, c.want, got, c.sql)
		}
	}
}

func TestCheckReadOnly(t *testing.T) {
	for _, stmt := range []string{
		"SELECT * FROM t", "  show tables", "WITH cte AS (SELECT 1) SELECT * FROM cte",
		"/* 注释 */ EXPLAIN SELECT 1",
	} {
		if err := CheckReadOnly(stmt); err != nil {
			t.Errorf("只读语句 %q 被拒绝: %v", stmt, err)
		}
	}
	for _, stmt := range []string{
		"INSERT INTO t VALUES (1)", "UPDATE t SET a=1", "DROP TABLE t",
		"CREATE DATABASE d", "DELETE FROM t",
	} {
		if err := CheckReadOnly(stmt); err == nil {
			t.Errorf("写语句 %q 未被拦截", stmt)
		}
	}
	if err := CheckReadOnly(""); err != nil {
		t.Errorf("空语句不应报错: %v", err)
	}
	if err := CheckReadOnly("("); err != nil {
		t.Errorf("纯符号不应 panic 或报错: %v", err)
	}
}

type fakeResultSet struct {
	pages   []domain.ResultPage
	page    int
	row     int
	current domain.Row
}

func (f *fakeResultSet) Columns() []domain.Column {
	return f.pages[f.page].Columns
}

func (f *fakeResultSet) Next() bool {
	if f.page >= len(f.pages) || f.row >= len(f.pages[f.page].Rows) {
		return false
	}
	f.current = f.pages[f.page].Rows[f.row]
	f.row++
	return true
}

func (f *fakeResultSet) Row() domain.Row                  { return f.current }
func (f *fakeResultSet) Document() string                 { return "" }
func (f *fakeResultSet) Err() error                       { return nil }
func (f *fakeResultSet) Affected() int64                  { return 0 }
func (f *fakeResultSet) Messages() []domain.ServerMessage { return nil }
func (f *fakeResultSet) Close() error                     { return nil }

func (f *fakeResultSet) NextResultSet() bool {
	if f.page+1 >= len(f.pages) {
		return false
	}
	f.page++
	f.row = 0
	return true
}

func makePage(cols int, rows int) domain.ResultPage {
	p := domain.ResultPage{Columns: make([]domain.Column, cols)}
	for i := 0; i < cols; i++ {
		p.Columns[i] = domain.Column{Name: "c" + string(rune('a'+i))}
	}
	for r := 0; r < rows; r++ {
		row := make(domain.Row, cols)
		for c := range row {
			row[c] = domain.NewTextCell("v")
		}
		p.Rows = append(p.Rows, row)
	}
	return p
}

func TestConsumeResultMaxRows(t *testing.T) {
	rs := &fakeResultSet{pages: []domain.ResultPage{makePage(2, 500)}}
	sink := &CollectSink{}
	out, err := consumeResult(sink, rs, nil, domain.Query{MaxRows: 120}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Pages) != 1 || len(out.Pages[0].Rows) != 120 {
		t.Fatalf("行数限制失效: %d 页 / %d 行", len(out.Pages), len(out.Pages[0].Rows))
	}
	if !out.Summary.Truncated {
		t.Error("应标记为结果被截断")
	}
	if len(sink.Outcome.Pages[0].Rows) != 120 {
		t.Errorf("sink 未收到全部批次: %d 行", len(sink.Outcome.Pages[0].Rows))
	}
}

func TestConsumeResultMultiPage(t *testing.T) {
	rs := &fakeResultSet{pages: []domain.ResultPage{makePage(1, 3), makePage(2, 2)}}
	out, err := consumeResult(&CollectSink{}, rs, nil, domain.Query{MaxRows: 0}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Pages) != 2 || out.Summary.Pages != 2 || out.Summary.Rows != 5 {
		t.Fatalf("多结果集处理错误: %+v", out.Summary)
	}
	if len(out.Pages[1].Columns) != 2 {
		t.Error("第二个结果集列信息未刷新")
	}
}
