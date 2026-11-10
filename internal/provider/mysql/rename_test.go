package mysql

import (
	"context"
	"strings"
	"testing"

	"github.com/db-fly/db-fly/internal/domain"
)

func TestRenameTableScript(t *testing.T) {
	ref := domain.ObjectRef{Database: "app", Name: "users"}
	script, err := (&schemaEditor{}).AlterTableScript(context.Background(), ref,
		[]domain.SchemaChange{{Kind: domain.ChangeRenameTable, NewName: "members"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(script.Statements) != 1 || script.Statements[0] != "RENAME TABLE `app`.`users` TO `app`.`members`" {
		t.Fatalf("重命名脚本不正确: %+v", script.Statements)
	}
	if len(script.Rollback) != 1 || script.Rollback[0] != "RENAME TABLE `app`.`members` TO `app`.`users`" {
		t.Fatalf("回滚脚本不正确: %+v", script.Rollback)
	}
	if _, err := (&schemaEditor{}).AlterTableScript(context.Background(), ref,
		[]domain.SchemaChange{
			{Kind: domain.ChangeRenameTable, NewName: "members"},
			{Kind: domain.ChangeAddColumn, Column: domain.ColumnSpec{Name: "age", Type: "INT"}},
		}); err == nil || !strings.Contains(err.Error(), "单独提交") {
		t.Fatalf("重命名与其他变更混合应被拒绝: %v", err)
	}
}
