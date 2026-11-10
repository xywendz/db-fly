package sqlite

import (
	"context"
	"testing"

	"github.com/db-fly/db-fly/internal/domain"
)

func TestRenameTableEndToEnd(t *testing.T) {
	ctx := context.Background()
	sess, ref := openTemp(t)
	editor := sess.Schema()

	def := domain.TableDefinition{
		Ref: ref,
		Columns: []domain.ColumnSpec{
			{Name: "id", Type: "INTEGER", PrimaryKey: true, AutoIncrement: true},
			{Name: "name", Type: "TEXT"},
		},
	}
	create, err := editor.CreateTableScript(ctx, def)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := editor.Apply(ctx, create); err != nil {
		t.Fatal(err)
	}
	if _, err := sess.Query().Exec(ctx, `INSERT INTO users (name) VALUES ('ann')`, "main"); err != nil {
		t.Fatal(err)
	}

	script, err := editor.AlterTableScript(ctx, ref,
		[]domain.SchemaChange{{Kind: domain.ChangeRenameTable, NewName: "members"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(script.Statements) != 1 || script.Statements[0] != `ALTER TABLE "users" RENAME TO "members"` {
		t.Fatalf("重命名脚本不正确: %+v", script.Statements)
	}
	if _, err := editor.Apply(ctx, script); err != nil {
		t.Fatalf("执行重命名失败: %v", err)
	}

	rs, err := sess.Query().Execute(ctx, domain.Query{
		Ref: domain.ObjectRef{Database: "main", Name: "members"}, MaxRows: 10,
	})
	if err != nil {
		t.Fatalf("重命名后无法读取新表: %v", err)
	}
	rows := collect(t, rs)
	if len(rows) != 1 || rows[0][1] != "ann" {
		t.Fatalf("重命名后数据不正确: %+v", rows)
	}
}
