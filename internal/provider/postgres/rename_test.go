package postgres

import (
	"strings"
	"testing"

	"github.com/xywendz/db-fly/internal/domain"
)

func TestRenameTableScript(t *testing.T) {
	ref := domain.ObjectRef{Database: "app", Name: "public.users"}
	statements, rollbacks, _, err := (&ddlDriver{}).AlterTable(ref,
		[]domain.SchemaChange{{Kind: domain.ChangeRenameTable, NewName: "members"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(statements) != 1 || statements[0] != `ALTER TABLE "public"."users" RENAME TO "members"` {
		t.Fatalf("重命名脚本不正确: %+v", statements)
	}
	if len(rollbacks) != 1 || rollbacks[0] != `ALTER TABLE "public"."members" RENAME TO "users"` {
		t.Fatalf("回滚脚本不正确: %+v", rollbacks)
	}
	_, _, _, err = (&ddlDriver{}).AlterTable(ref, []domain.SchemaChange{
		{Kind: domain.ChangeRenameTable, NewName: "members"},
		{Kind: domain.ChangeAddColumn, Column: domain.ColumnSpec{Name: "age", Type: "INT"}},
	})
	if err == nil || !strings.Contains(err.Error(), "单独提交") {
		t.Fatalf("重命名与其他变更混合应被拒绝: %v", err)
	}
}

func TestDropDatabaseScriptForce(t *testing.T) {
	script := dropDatabaseScript(150000, "db1", `"db1"`, "postgres")
	if script.Database != "postgres" {
		t.Fatalf("删库脚本应绑定到维护库: %q", script.Database)
	}
	if len(script.Statements) != 1 || script.Statements[0] != `DROP DATABASE "db1" WITH (FORCE)` {
		t.Fatalf("PG13+ 删库脚本不正确: %+v", script.Statements)
	}
	if !strings.Contains(strings.Join(script.Warnings, " "), "终止") {
		t.Fatalf("缺少会话中断提示: %+v", script.Warnings)
	}
}

func TestDropDatabaseScriptLegacy(t *testing.T) {
	script := dropDatabaseScript(120000, "db1", `"db1"`, "postgres")
	if len(script.Statements) != 3 ||
		!strings.Contains(script.Statements[0], "pg_terminate_backend") ||
		!strings.Contains(script.Statements[0], "datname = 'db1'") ||
		script.Statements[2] != `DROP DATABASE "db1"` {
		t.Fatalf("旧版 PostgreSQL 删库脚本不正确: %+v", script.Statements)
	}

	escaped := dropDatabaseScript(120000, "db'1", `"db'1"`, "postgres")
	if !strings.Contains(escaped.Statements[0], "datname = 'db''1'") {
		t.Fatalf("库名未正确转义: %s", escaped.Statements[0])
	}
}
