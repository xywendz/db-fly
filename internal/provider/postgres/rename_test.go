package postgres

import (
	"strings"
	"testing"

	"github.com/db-fly/db-fly/internal/domain"
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
