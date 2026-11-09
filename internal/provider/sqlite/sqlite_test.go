package sqlite

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/db-fly/db-fly/internal/domain"
)

func openTemp(t *testing.T) (domain.Session, domain.ObjectRef) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "client.db")
	cfg := domain.ConnectionConfig{
		Name: "test", Kind: domain.KindSQLite, Host: path, Port: 0,
		Database: "main", TimeoutSec: 15,
	}
	sess, err := providerImpl{}.Open(context.Background(), cfg)
	if err != nil {
		t.Fatalf("打开 SQLite 失败: %v", err)
	}
	t.Cleanup(func() { _ = sess.Close() })
	return sess, domain.ObjectRef{Database: "main", Name: "users"}
}

func collect(t *testing.T, rs domain.ResultSet) [][]string {
	t.Helper()
	defer rs.Close()
	var out [][]string
	for rs.Next() {
		row := make([]string, 0)
		for _, cell := range rs.Row() {
			row = append(row, cell.Display())
		}
		out = append(out, row)
	}
	if err := rs.Err(); err != nil {
		t.Fatalf("读取结果集失败: %v", err)
	}
	return out
}

func TestSQLiteEndToEnd(t *testing.T) {
	ctx := context.Background()
	sess, ref := openTemp(t)
	editor := sess.Schema()

	def := domain.TableDefinition{
		Ref: ref,
		Columns: []domain.ColumnSpec{
			{Name: "id", Type: "INTEGER", PrimaryKey: true, AutoIncrement: true},
			{Name: "name", Type: "TEXT"},
			{Name: "age", Type: "INTEGER"},
		},
		Indexes: []domain.IndexSpec{{Name: "idx_name", Columns: []string{"name"}}},
	}
	create, err := editor.CreateTableScript(ctx, def)
	if err != nil {
		t.Fatalf("生成建表脚本失败: %v", err)
	}
	if len(create.Statements) != 2 || !strings.Contains(create.Statements[0], "INTEGER PRIMARY KEY AUTOINCREMENT") {
		t.Fatalf("建表脚本不正确: %+v", create.Statements)
	}
	if _, err := editor.Apply(ctx, create); err != nil {
		t.Fatalf("执行建表脚本失败: %v", err)
	}

	for _, stmt := range []string{
		`INSERT INTO users (name, age) VALUES ('ann', 18)`,
		`INSERT INTO users (name, age) VALUES ('bob', 30)`,
		`INSERT INTO users (name, age) VALUES ('cindy', 25)`,
	} {
		if _, err := sess.Query().Exec(ctx, stmt, "main"); err != nil {
			t.Fatalf("插入数据失败: %v", err)
		}
	}

	// 表数据浏览：filter + sort 必须生效
	rs, err := sess.Query().Execute(ctx, domain.Query{
		Database: "main", Ref: ref, Filter: "age >= 20", Sort: "age DESC", MaxRows: 10,
	})
	if err != nil {
		t.Fatalf("带条件预览失败: %v", err)
	}
	rows := collect(t, rs)
	if len(rows) != 2 || rows[0][2] != "30" || rows[1][2] != "25" {
		t.Fatalf("filter/sort 结果不正确: %+v", rows)
	}

	// 查看建表 SQL
	ddl, err := editor.ShowCreate(ctx, ref)
	if err != nil {
		t.Fatalf("ShowCreate 失败: %v", err)
	}
	if !strings.Contains(ddl, "CREATE TABLE") || !strings.Contains(ddl, "AUTOINCREMENT") {
		t.Fatalf("建表 SQL 不正确: %s", ddl)
	}

	// 修改表结构时生成的 SQL 必须可见，且可执行
	alter, err := editor.AlterTableScript(ctx, ref, []domain.SchemaChange{
		{Kind: domain.ChangeAddColumn, Column: domain.ColumnSpec{Name: "email", Type: "TEXT", Nullable: true}},
	})
	if err != nil {
		t.Fatalf("生成改表脚本失败: %v", err)
	}
	if len(alter.Statements) != 1 || !strings.Contains(alter.Statements[0], "ADD COLUMN") {
		t.Fatalf("改表 SQL 不正确: %+v", alter.Statements)
	}
	if _, err := editor.Apply(ctx, alter); err != nil {
		t.Fatalf("执行改表脚本失败: %v", err)
	}
	schema, err := sess.Metadata().Describe(ctx, ref)
	if err != nil {
		t.Fatalf("读取元数据失败: %v", err)
	}
	if len(schema.Columns) != 4 || schema.Columns[3].Name != "email" {
		t.Fatalf("新字段未生效: %+v", schema.Columns)
	}

	// TRUNCATE：SQLite 用 DELETE + 重置 sqlite_sequence 实现
	truncate, err := sess.Maintenance().TableActionScript(ctx, ref, domain.TableActionTruncate)
	if err != nil {
		t.Fatalf("生成 TRUNCATE 脚本失败: %v", err)
	}
	if len(truncate.Statements) != 2 || !strings.Contains(truncate.Statements[1], "sqlite_sequence") {
		t.Fatalf("TRUNCATE 脚本不正确: %+v", truncate.Statements)
	}
	if _, err := editor.Apply(ctx, truncate); err != nil {
		t.Fatalf("执行 TRUNCATE 失败: %v", err)
	}
	count := queryScalar(t, sess, `SELECT COUNT(*) FROM users`)
	if count != "0" {
		t.Fatalf("TRUNCATE 后仍有数据: %s", count)
	}

	// EMPTY：DELETE FROM 且不重置自增序列
	if _, err := sess.Query().Exec(ctx, `INSERT INTO users (name, age) VALUES ('dave', 40)`, "main"); err != nil {
		t.Fatal(err)
	}
	empty, err := sess.Maintenance().TableActionScript(ctx, ref, domain.TableActionEmpty)
	if err != nil {
		t.Fatalf("生成 EMPTY 脚本失败: %v", err)
	}
	if empty.Statements[0] != `DELETE FROM "users"` {
		t.Fatalf("EMPTY 脚本不正确: %+v", empty.Statements)
	}
	if _, err := editor.Apply(ctx, empty); err != nil {
		t.Fatalf("执行 EMPTY 失败: %v", err)
	}
	if count = queryScalar(t, sess, `SELECT COUNT(*) FROM users`); count != "0" {
		t.Fatalf("EMPTY 后仍有数据: %s", count)
	}
}

func queryScalar(t *testing.T, sess domain.Session, stmt string) string {
	t.Helper()
	rs, err := sess.Query().Execute(context.Background(), domain.Query{
		Language: domain.LangSQL, Text: stmt, Database: "main",
	})
	if err != nil {
		t.Fatalf("查询失败: %v", err)
	}
	rows := collect(t, rs)
	if len(rows) != 1 || len(rows[0]) != 1 {
		t.Fatalf("标量查询返回异常: %+v", rows)
	}
	return rows[0][0]
}
