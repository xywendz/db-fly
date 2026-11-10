package sqlkit

import (
	"strconv"
	"strings"
	"testing"

	"github.com/db-fly/db-fly/internal/domain"
)

func TestParseFilterSort(t *testing.T) {
	fs, err := ParseFilterSort("WHERE age > 18 AND status = 'A'", "ORDER BY created_at DESC, id ASC", ANSIQuoter)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if fs.Where != "age > 18 AND status = 'A'" {
		t.Fatalf("WHERE 片段不正确: %q", fs.Where)
	}
	want := `"created_at" DESC, "id" ASC`
	if fs.OrderBy != want {
		t.Fatalf("ORDER BY 片段不正确: %q，期望 %q", fs.OrderBy, want)
	}

	fs, err = ParseFilterSort("", "schema.name", MySQLQuoter)
	if err != nil {
		t.Fatalf("解析限定列失败: %v", err)
	}
	if fs.OrderBy != "`schema`.`name`" {
		t.Fatalf("限定列引用错误: %q", fs.OrderBy)
	}
}

func TestParseFilterSortRejectsInjection(t *testing.T) {
	cases := []struct {
		name, filter, sort string
	}{
		{"语句分隔符", "id = 1; DROP TABLE users", ""},
		{"行注释", "id = 1 -- x", ""},
		{"块注释", "id = 1 /* x */", ""},
		{"非法方向", "", "id SIDEWAYS"},
		{"非法列名", "", "id); DROP TABLE t"},
		{"空排序项", "", "id ASC, , name"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := ParseFilterSort(tc.filter, tc.sort, ANSIQuoter); err == nil {
				t.Fatalf("应当拒绝 %s", tc.name)
			}
		})
	}
}

func TestBuildPreviewDialects(t *testing.T) {
	ref := domain.ObjectRef{Database: "app", Name: "users"}
	base := domain.Query{Ref: ref, Filter: "age > 18", Sort: "id DESC, name ASC", MaxRows: 25}

	mysql, err := BuildPreview(SelectDialect{
		Quoter: MySQLQuoter,
		Qualified: func(r domain.ObjectRef) (string, error) {
			return MySQLQuoter.Qualified(r.Database, r.Name)
		},
		LimitSuffix: func(n, offset int) string { return " LIMIT " + strconv.Itoa(n) },
	}, base)
	if err != nil {
		t.Fatal(err)
	}
	if mysql != "SELECT * FROM `app`.`users` WHERE age > 18 ORDER BY `id` DESC, `name` ASC LIMIT 25" {
		t.Fatalf("MySQL 预览语句错误: %s", mysql)
	}

	mssql, err := BuildPreview(SelectDialect{
		Quoter: TSQLQuoter,
		Qualified: func(r domain.ObjectRef) (string, error) {
			return TSQLQuoter.Qualified("dbo", r.Name)
		},
		TopPrefix: func(n, offset int) string { return "TOP " + strconv.Itoa(n) + " " },
	}, base)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(mssql, "SELECT TOP 25 * FROM [dbo].[users] WHERE age > 18") {
		t.Fatalf("SQL Server 预览语句错误: %s", mssql)
	}

	oracle, err := BuildPreview(SelectDialect{
		Quoter: ANSIQuoter,
		Qualified: func(r domain.ObjectRef) (string, error) {
			return ANSIQuoter.Qualified(r.Database, r.Name)
		},
		LimitSuffix: func(n, offset int) string { return " FETCH FIRST " + strconv.Itoa(n) + " ROWS ONLY" },
	}, base)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(oracle, "FETCH FIRST 25 ROWS ONLY") {
		t.Fatalf("Oracle 预览语句错误: %s", oracle)
	}
}

func TestBuildConditionsAndOrderTerms(t *testing.T) {
	where, err := BuildConditions([]domain.FilterCondition{
		{Column: "name", Operator: "LIKE", Value: "A'%"},
		{Column: "age", Operator: ">", Value: "18", Join: "AND"},
		{Column: "status", Operator: "IN", Value: "A, B", Join: "OR"},
		{Column: "deleted_at", Operator: "IS NULL", Join: "AND"},
	}, MySQLQuoter)
	if err != nil {
		t.Fatal(err)
	}
	want := "`name` LIKE 'A''%' AND `age` > '18' OR `status` IN ('A', 'B') AND `deleted_at` IS NULL"
	if where != want {
		t.Fatalf("结构化过滤条件错误:\n got: %s\nwant: %s", where, want)
	}
	order, err := BuildOrderTerms([]domain.SortTerm{
		{Column: "id", Desc: true}, {Column: "name"},
	}, MySQLQuoter)
	if err != nil {
		t.Fatal(err)
	}
	if order != "`id` DESC, `name` ASC" {
		t.Fatalf("结构化排序错误: %s", order)
	}
	if _, err := BuildConditions([]domain.FilterCondition{
		{Column: "id", Operator: "SLEEP(1)", Value: "1"},
	}, MySQLQuoter); err == nil {
		t.Fatal("非法操作符应被拒绝")
	}
}

func TestBuildPreviewPaging(t *testing.T) {
	ref := domain.ObjectRef{Database: "app", Name: "users"}
	q := domain.Query{
		Ref: ref, MaxRows: 50, Offset: 100,
		Conditions: []domain.FilterCondition{{Column: "age", Operator: ">", Value: "18"}},
		OrderTerms: []domain.SortTerm{{Column: "id", Desc: true}},
	}
	mysql, err := BuildPreview(SelectDialect{
		Quoter: MySQLQuoter,
		Qualified: func(r domain.ObjectRef) (string, error) {
			return MySQLQuoter.Qualified(r.Database, r.Name)
		},
		LimitSuffix: func(n, offset int) string {
			return " LIMIT " + strconv.Itoa(n) + " OFFSET " + strconv.Itoa(offset)
		},
	}, q)
	if err != nil {
		t.Fatal(err)
	}
	want := "SELECT * FROM `app`.`users` WHERE `age` > '18' ORDER BY `id` DESC LIMIT 50 OFFSET 100"
	if mysql != want {
		t.Fatalf("分页语句错误:\n got: %s\nwant: %s", mysql, want)
	}

	tsql, err := BuildPreview(SelectDialect{
		Quoter:      TSQLQuoter,
		Qualified:   func(r domain.ObjectRef) (string, error) { return TSQLQuoter.Qualified(r.Name) },
		TopPrefix:   func(n, offset int) string { return "" },
		PageOrderBy: "(SELECT NULL)",
		LimitSuffix: func(n, offset int) string {
			return " OFFSET " + strconv.Itoa(offset) + " ROWS FETCH NEXT " + strconv.Itoa(n) + " ROWS ONLY"
		},
	}, domain.Query{Ref: ref, MaxRows: 20, Offset: 40})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(tsql, "ORDER BY (SELECT NULL) OFFSET 40 ROWS FETCH NEXT 20 ROWS ONLY") {
		t.Fatalf("T-SQL 分页语句错误: %s", tsql)
	}
}

func TestBuildPreviewRejectsFilterWithCustomSQL(t *testing.T) {
	_, err := BuildPreview(SelectDialect{
		Quoter:    ANSIQuoter,
		Qualified: func(r domain.ObjectRef) (string, error) { return ANSIQuoter.Ident(r.Name) },
	}, domain.Query{Text: "SELECT 1", Filter: "id > 1"})
	if err == nil {
		t.Fatal("自定义 SQL 与 filter 同时出现时应报错")
	}
}

func TestColumnDefinition(t *testing.T) {
	def, err := ColumnDefinition(ColumnConfig{
		Quoter: ANSIQuoter, Op: "test",
		AutoIncrementSuffix: "GENERATED BY DEFAULT AS IDENTITY",
	}, domain.ColumnSpec{Name: "id", Type: "bigint", PrimaryKey: true, AutoIncrement: true})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(def, `"id" bigint NOT NULL GENERATED BY DEFAULT AS IDENTITY`) {
		t.Fatalf("字段定义错误: %s", def)
	}
	value := "O'Brien"
	def, err = ColumnDefinition(ColumnConfig{Quoter: ANSIQuoter, Op: "test"},
		domain.ColumnSpec{Name: "name", Type: "text", Nullable: true, Default: &value})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(def, "DEFAULT 'O''Brien'") {
		t.Fatalf("默认值转义错误: %s", def)
	}
}

func TestTableOps(t *testing.T) {
	ops := TableOps{
		Name:      "test",
		Qualified: func(r domain.ObjectRef) (string, error) { return ANSIQuoter.Qualified(r.Database, r.Name) },
		Statements: func(a domain.TableAction, _ domain.ObjectRef, q string) ([]string, error) {
			if a == domain.TableActionTruncate {
				return []string{"TRUNCATE TABLE " + q}, nil
			}
			return []string{"DELETE FROM " + q}, nil
		},
		Database: func(r domain.ObjectRef) string { return r.Database },
	}
	truncate, err := ops.TableActionScript(nil, domain.ObjectRef{Database: "app", Name: "users"}, domain.TableActionTruncate)
	if err != nil {
		t.Fatal(err)
	}
	if truncate.Statements[0] != `TRUNCATE TABLE "app"."users"` || truncate.Database != "app" || len(truncate.Warnings) == 0 {
		t.Fatalf("TRUNCATE 脚本错误: %+v", truncate)
	}
	empty, err := ops.TableActionScript(nil, domain.ObjectRef{Database: "app", Name: "users"}, domain.TableActionEmpty)
	if err != nil {
		t.Fatal(err)
	}
	if empty.Statements[0] != `DELETE FROM "app"."users"` {
		t.Fatalf("EMPTY 脚本错误: %+v", empty)
	}
	if _, err := ops.TableActionScript(nil, domain.ObjectRef{Name: "t"}, "bogus"); err == nil {
		t.Fatal("非法动作应被拒绝")
	}
}

func TestRenderCreateTable(t *testing.T) {
	schema := domain.TableSchema{
		Ref: domain.ObjectRef{Name: "users"},
		Columns: []domain.ColumnInfo{
			{Name: "id", RawType: "bigint", AutoIncrement: true},
			{Name: "name", RawType: "varchar(64)", Nullable: true, PrimaryKey: false},
		},
		Indexes: []domain.IndexInfo{{Name: "idx_name", Columns: []string{"name"}, Unique: true}},
	}
	text := RenderCreateTable(ANSIQuoter, schema, `"public"."users"`, "测试")
	if !strings.Contains(text, "CREATE TABLE \"public\".\"users\"") ||
		!strings.Contains(text, `CREATE UNIQUE INDEX "idx_name" ON "public"."users" ("name")`) ||
		!strings.HasPrefix(text, "-- 测试") {
		t.Fatalf("渲染结果错误:\n%s", text)
	}
}
